package chartgate

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

// Run retains the single-checkout bootstrap mode: clusters/<cluster>.yaml.
// Remote and local chart output are both followed for child Applications.
func (g *Gate) Run(ctx context.Context, clusters []string) []Result {
	var roots []Application
	var results []Result
	for _, cluster := range clusters {
		file := filepath.Join("clusters", cluster+".yaml")
		raw, err := os.ReadFile(filepath.Join(g.Root, file))
		var apps []Application
		if err == nil {
			apps, err = ParseApplications(raw)
		}
		if err == nil && len(apps) == 0 {
			err = fmt.Errorf("no root Applications in %s", file)
		}
		if err != nil {
			results = append(results, Result{Cluster: cluster, App: "(bootstrap)", Chart: file, Status: StatusFail, Detail: err.Error()})
			continue
		}
		for i := range apps {
			apps[i].Cluster = cluster
		}
		roots = append(roots, apps...)
	}
	return g.run(ctx, roots, results, false)
}

// RunApplications checks explicit root Applications without a bootstrap layout.
// Every Git source is resolved only through Sources; SelfRepo is not a fallback.
// A Gate must not be used by two concurrent runs.
func (g *Gate) RunApplications(ctx context.Context, apps []Application) []Result {
	return g.run(ctx, apps, nil, true)
}

func gateFailure(err error) Result {
	return Result{Cluster: "-", App: "(chartgate)", Chart: "inputs", Status: StatusFail, Detail: err.Error()}
}

func (g *Gate) run(ctx context.Context, apps []Application, results []Result, explicit bool) []Result {
	if len(apps) == 0 && len(results) == 0 {
		return []Result{gateFailure(fmt.Errorf("no root Applications supplied"))}
	}
	if g.Jobs < 1 {
		g.Jobs = 4
	}
	if g.MaxDepth < 1 {
		g.MaxDepth = 32
	}
	if g.MaxRenders < 1 {
		g.MaxRenders = 10000
	}
	if g.Helm == "" {
		g.Helm = "helm"
	}
	g.explicit = explicit
	g.pulls, g.logins = map[string]*pullOnce{}, map[string]*pullOnce{}
	g.renders = map[string]*renderOnce{}
	g.sem = make(chan struct{}, g.Jobs)
	if err := g.prepareSources(ctx); err != nil {
		return append(results, gateFailure(err))
	}
	work, err := os.MkdirTemp("", "chartgate-")
	if err != nil {
		return append(results, gateFailure(err))
	}
	defer func() { _ = os.RemoveAll(work) }()
	g.work = work
	cache := g.CacheDir
	if cache == "" {
		g.CacheDir = filepath.Join(work, "cache")
		defer func() { g.CacheDir = "" }()
	}
	if err := os.MkdirAll(g.CacheDir, 0o755); err != nil {
		return append(results, gateFailure(err))
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, app := range apps {
		wg.Go(func() {
			res := g.walk(ctx, app, 0, map[string]bool{}, map[string]bool{})
			mu.Lock()
			results = append(results, res...)
			mu.Unlock()
		})
	}
	wg.Wait()
	sort.SliceStable(results, func(i, j int) bool {
		a, b := results[i], results[j]
		if a.Cluster != b.Cluster {
			return a.Cluster < b.Cluster
		}
		if a.App != b.App {
			return a.App < b.App
		}
		return a.Chart < b.Chart
	})
	return results
}

// Cache rendering, not a name/path pair: the full Application (all source refs,
// values, namespace and release) is the context. Each root's depth-first walk
// keeps ancestry separate from completed nodes so a cycle fails explicitly and
// repeated traversal cannot suppress a failure already included in its results.
func (g *Gate) walk(ctx context.Context, app Application, depth int, active, done map[string]bool) []Result {
	cluster := app.Cluster
	if cluster == "" {
		cluster = "-"
	}
	sources, err := g.selectedSources(app)
	if err != nil {
		return []Result{{Cluster: cluster, App: app.Name, Chart: "sources", Sources: sources, Status: StatusFail, Detail: err.Error()}}
	}
	if app.Name == "" || len(app.Sources) == 0 {
		return []Result{{Cluster: cluster, App: app.Name, Chart: "Application", Sources: sources, Status: StatusFail, Detail: "Application name and sources are required"}}
	}
	var results []Result
	objectsPresent := make([]bool, len(app.Sources))
	for i, source := range app.Sources {
		objectsPresent[i] = source.Helm.ValuesObject != nil
	}
	for index, src := range app.Sources {
		selfRepo := g.SelfRepo
		if g.explicit {
			selfRepo = nil
		}
		class, chart := Classify(src, selfRepo)
		if _, mapped := g.mapping(src); mapped && src.Path != "" && src.Chart == "" {
			class = ClassLocal
		}
		if class == ClassRefOnly {
			continue
		}
		raw, err := yaml.Marshal(struct {
			App   Application
			Index int
			// yaml omitempty hides both absent and explicitly empty objects;
			// they select different inline values and must not share a render.
			ObjectsPresent []bool
		}{app, index, objectsPresent})
		key := fmt.Sprintf("%x", sha256.Sum256(raw))
		res := Result{Cluster: cluster, App: app.Name, Chart: chart.String(), Sources: sources}
		if class != ClassRemote {
			res.Chart = src.RepoURL + " " + src.Path + "@" + src.TargetRevision
		}
		if err != nil {
			res.Status, res.Detail = StatusFail, fmt.Sprintf("encode render context: %v", err)
			results = append(results, res)
			continue
		}
		if active[key] {
			res.Status, res.Detail = StatusFail, "Application chart cycle detected"
			results = append(results, res)
			continue
		}
		if depth >= g.MaxDepth {
			res.Status, res.Detail = StatusFail, fmt.Sprintf("Application traversal exceeds maximum depth %d", g.MaxDepth)
			results = append(results, res)
			continue
		}
		if done[key] {
			continue
		}
		res, output := g.renderCached(ctx, key, class, target{cluster, app, src, chart}, res)
		if res.Status == StatusOK {
			children, err := ParseApplications(output)
			if err != nil {
				res.Status, res.Detail = StatusFail, err.Error()
			}
			results = append(results, res)
			active[key] = true
			for _, child := range children {
				if child.Cluster == "" {
					child.Cluster = app.Cluster
				}
				results = append(results, g.walk(ctx, child, depth+1, active, done)...)
			}
			delete(active, key)
		} else {
			results = append(results, res)
		}
		done[key] = true
	}
	return results
}

func (g *Gate) renderCached(ctx context.Context, key string, class Classification, t target, res Result) (Result, []byte) {
	g.pullMu.Lock()
	r, ok := g.renders[key]
	if !ok && len(g.renders) < g.MaxRenders {
		r = &renderOnce{}
		g.renders[key] = r
	}
	g.pullMu.Unlock()
	if r == nil {
		res.Status, res.Detail = StatusFail, fmt.Sprintf("Application traversal exceeds maximum render contexts %d", g.MaxRenders)
		return res, nil
	}
	r.once.Do(func() {
		select {
		case g.sem <- struct{}{}:
			defer func() { <-g.sem }()
		case <-ctx.Done():
			res.Status, res.Detail = StatusFail, ctx.Err().Error()
			r.result = res
			return
		}
		start := time.Now()
		var err error
		switch class {
		case ClassGit:
			res.Status, res.Detail = StatusSkip, "git source of plain manifests, not a Helm chart"
		case ClassLocal:
			r.output, err = g.renderLocal(ctx, t.app, t.source)
			res.Status = StatusOK
			if err != nil {
				res.Status, res.Detail = StatusFail, err.Error()
			}
		case ClassRemote:
			selected := res.Sources
			res, r.output = g.check(ctx, t)
			res.Sources = selected
		}
		res.Elapsed = time.Since(start)
		r.result = res
	})
	return r.result, r.output
}
