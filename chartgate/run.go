package chartgate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"go.yaml.in/yaml/v3"
)

type (
	// Status of one checked item.
	Status string

	// Result is the outcome for one (cluster, Application, chart).
	Result struct {
		Cluster string
		App     string
		Chart   string
		Status  Status
		Detail  string
		Elapsed time.Duration
	}

	// Gate carries everything a run needs.
	Gate struct {
		Root     string // repository root
		CacheDir string // pulled charts live under here
		Helm     string // helm binary
		Jobs     int    // concurrent helm invocations
		// SelfRepo recognizes this repository's git URL in an Application.
		SelfRepo func(string) bool

		// AWS is the aws CLI, used to mint the ECR registry password for
		// charts in a private ECR registry. Empty means "aws" on PATH.
		AWS string
		// AWSProfile, when set, is passed to `aws --profile`. Empty keeps
		// the ambient chain, which on a CI runner is the pool's pod
		// identity. Never read from AWS_PROFILE: a laptop opts in
		// explicitly (CHART_GATE_AWS_PROFILE), so an exported profile
		// cannot change what the gate checks.
		AWSProfile string
		// RequireECR turns a failed ECR login into a FAILURE instead of
		// a "not checked" skip. CI sets it, so a broken credential can
		// never quietly drop the private charts from the gate; a laptop
		// without credentials leaves it off and still gets the skip.
		RequireECR bool

		work   string // per-run scratch for value files
		pullMu sync.Mutex
		pulls  map[string]*pullOnce
		logins map[string]*pullOnce // ECR host -> login outcome (err only)
	}

	pullOnce struct {
		once sync.Once
		path string
		err  error
	}

	target struct {
		cluster string
		app     Application
		source  Source
		chart   Chart
	}
)

const (
	// StatusOK means helm template accepted the chart and values.
	StatusOK Status = "ok"
	// StatusFail means the chart could not be pulled or refused the values.
	StatusFail Status = "FAIL"
	// StatusSkip means the item was deliberately not checked; Detail says why.
	StatusSkip Status = "not checked"
)

// Run checks every cluster and returns the results sorted by cluster,
// application, chart.
func (g *Gate) Run(ctx context.Context, clusters []string) []Result {
	if g.Jobs < 1 {
		g.Jobs = 4
	}

	g.pulls = map[string]*pullOnce{}
	g.logins = map[string]*pullOnce{}

	work, err := os.MkdirTemp("", "chartgate-")
	if err != nil {
		return []Result{{Cluster: "-", App: "(chartgate)", Chart: "temp dir", Status: StatusFail, Detail: err.Error()}}
	}

	defer func() { _ = os.RemoveAll(work) }()

	g.work = work

	var (
		mu      sync.Mutex
		results []Result
		targets []target
		wg      sync.WaitGroup
	)

	// Phase 1: walk each cluster's Application tree. Local charts render
	// here (they are fast and they emit the next level of Applications).
	for _, cluster := range clusters {
		wg.Add(1)

		go func() {
			defer wg.Done()

			res, tgts := g.crawl(ctx, cluster)

			mu.Lock()
			results = append(results, res...)
			targets = append(targets, tgts...)
			mu.Unlock()
		}()
	}

	wg.Wait()

	// Phase 2: remote charts, in parallel, pulls deduplicated.
	sem := make(chan struct{}, g.Jobs)

	for i := range targets {
		t := targets[i]

		wg.Add(1)

		go func() {
			defer wg.Done()

			sem <- struct{}{}
			defer func() { <-sem }()

			res := g.check(ctx, t)

			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}()
	}

	wg.Wait()

	sort.Slice(results, func(i, j int) bool {
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

// crawl starts from clusters/<cluster>.yaml, the hand-applied bootstrap
// Application, and follows every local chart it installs.
func (g *Gate) crawl(ctx context.Context, cluster string) ([]Result, []target) {
	var (
		results []Result
		targets []target
	)

	boot := filepath.Join(g.Root, "clusters", cluster+".yaml")

	raw, err := os.ReadFile(boot)
	if err != nil {
		return []Result{{Cluster: cluster, App: "(bootstrap)", Chart: "clusters/" + cluster + ".yaml", Status: StatusFail, Detail: err.Error()}}, nil
	}

	queue, err := ParseApplications(raw)
	if err != nil {
		return []Result{{Cluster: cluster, App: "(bootstrap)", Chart: "clusters/" + cluster + ".yaml", Status: StatusFail, Detail: err.Error()}}, nil
	}

	seen := map[string]bool{}

	for len(queue) > 0 {
		app := queue[0]
		queue = queue[1:]

		for i := range app.Sources {
			src := app.Sources[i]

			class, chart := Classify(src, g.SelfRepo)

			switch class {
			case ClassRefOnly:
			case ClassGit:
				results = append(results, Result{
					Cluster: cluster, App: app.Name, Chart: src.RepoURL + " " + src.Path + "@" + src.TargetRevision,
					Status: StatusSkip, Detail: "git source of plain manifests, not a Helm chart",
				})
			case ClassLocal:
				key := app.Name + "|" + src.Path
				if seen[key] {
					continue
				}

				seen[key] = true

				start := time.Now()
				out, err := g.renderLocal(ctx, app, src)

				res := Result{Cluster: cluster, App: app.Name, Chart: src.Path + " (this repo)", Status: StatusOK, Elapsed: time.Since(start)}
				if err != nil {
					res.Status, res.Detail = StatusFail, err.Error()
					results = append(results, res)

					continue
				}

				results = append(results, res)

				next, err := ParseApplications(out)
				if err != nil {
					results = append(results, Result{Cluster: cluster, App: app.Name, Chart: src.Path, Status: StatusFail, Detail: err.Error()})

					continue
				}

				queue = append(queue, next...)
			case ClassRemote:
				targets = append(targets, target{cluster: cluster, app: app, source: src, chart: chart})
			}
		}
	}

	return results, targets
}

func (g *Gate) renderLocal(ctx context.Context, app Application, src Source) ([]byte, error) {
	files, err := g.valueFiles(app, src, true, "")
	if err != nil {
		return nil, err
	}

	args := []string{"template", app.Release(src), filepath.Join(g.Root, src.Path)}
	args = append(args, namespaceArgs(app)...)

	for _, f := range files {
		args = append(args, "-f", f)
	}

	return g.helm(ctx, args...)
}

// valueFiles resolves an Application source's values the way Argo CD
// layers them: valueFiles in order (a missing one is dropped when
// ignoreMissingValueFiles is set, an error otherwise), then the inline
// `values`, then `valuesObject`. $values/ names this repository's root.
func (g *Gate) valueFiles(app Application, src Source, local bool, tgz string) ([]string, error) {
	var files []string

	// A local chart takes every value file. A remote chart takes the ones that
	// are this repository's (`$values/...`) and, when the pulled archive is
	// given, the ones the chart ships inside itself (`presets/health.yaml`):
	// Argo CD reads those from the chart it pulled, so the gate extracts them
	// from the archive it pulled. Without the archive they are left out. A chart
	// whose required values arrive in the repository's file (the sluis chart's
	// `documents`) refuses to render without it.
	for _, f := range src.Helm.ValueFiles {
		shipped := !local && !strings.HasPrefix(f, "$values/")
		if shipped && tgz == "" {
			continue
		}

		path := strings.Replace(f, "$values/", g.Root+"/", 1)

		if shipped {
			p, err := g.extractShipped(tgz, f)
			if err != nil {
				if errors.Is(err, os.ErrNotExist) && src.Helm.IgnoreMissing {
					continue
				}

				return nil, fmt.Errorf("value file %s: %w", f, err)
			}

			files = append(files, p)

			continue
		}

		if _, err := os.Stat(path); err != nil {
			if os.IsNotExist(err) && src.Helm.IgnoreMissing {
				continue
			}

			return nil, fmt.Errorf("value file %s: %w", f, err)
		}

		files = append(files, path)
	}

	dir, err := os.MkdirTemp(g.work, "values-")
	if err != nil {
		return nil, err
	}

	write := func(name string, body []byte) error {
		p := filepath.Join(dir, name)
		files = append(files, p)

		return os.WriteFile(p, body, 0o600)
	}

	if src.Helm.Values != "" {
		if err := write("values.yaml", []byte(src.Helm.Values)); err != nil {
			return nil, err
		}
	}

	if len(src.Helm.ValuesObject) > 0 {
		raw, err := yaml.Marshal(src.Helm.ValuesObject)
		if err != nil {
			return nil, err
		}

		if err := write("valuesObject.yaml", raw); err != nil {
			return nil, err
		}
	}

	_ = app

	return files, nil
}

func namespaceArgs(app Application) []string {
	if app.Namespace == "" {
		return nil
	}

	return []string{"--namespace", app.Namespace}
}

// check renders one remote chart.
func (g *Gate) check(ctx context.Context, t target) Result {
	res := Result{Cluster: t.cluster, App: t.app.Name, Chart: t.chart.String()}
	if rel := t.app.Release(t.source); rel != t.app.Name {
		res.App += " [" + rel + "]"
	}

	start := time.Now()
	defer func() { res.Elapsed = time.Since(start) }()

	if why, ok := t.chart.NeedsAuth(); ok {
		if err := g.login(ctx, t.chart.Host()); err != nil {
			if g.RequireECR {
				res.Status = StatusFail
				res.Detail = "ECR login required here and failed (" + why + "): " + err.Error()
			} else {
				res.Status = StatusSkip
				res.Detail = "needs auth: " + why + " (login failed: " + firstLine(err.Error()) + ")"
			}

			return res
		}
	}

	tgz, err := g.pull(ctx, t.chart)
	if err != nil {
		res.Status, res.Detail = StatusFail, "helm pull: "+err.Error()

		return res
	}

	files, err := g.valueFiles(t.app, t.source, false, tgz)
	if err != nil {
		res.Status, res.Detail = StatusFail, err.Error()

		return res
	}

	args := []string{"template", t.app.Release(t.source), tgz}
	args = append(args, namespaceArgs(t.app)...)

	for _, f := range files {
		args = append(args, "-f", f)
	}

	if _, err := g.helm(ctx, args...); err != nil {
		res.Status, res.Detail = StatusFail, err.Error()

		return res
	}

	res.Status = StatusOK

	return res
}

// pull fetches chart into the cache once per run, and not at all when an
// earlier run left it there. Chart versions are immutable, so the cache
// is keyed by chart and version only.
func (g *Gate) pull(ctx context.Context, c Chart) (string, error) {
	g.pullMu.Lock()
	p, ok := g.pulls[c.String()]

	if !ok {
		p = &pullOnce{}
		g.pulls[c.String()] = p
	}

	g.pullMu.Unlock()

	p.once.Do(func() { p.path, p.err = g.pullOnce(ctx, c) })

	return p.path, p.err
}

func (g *Gate) pullOnce(ctx context.Context, c Chart) (string, error) {
	sum := sha256.Sum256([]byte(c.String()))
	dir := filepath.Join(g.CacheDir, "charts", hex.EncodeToString(sum[:8])+"-"+sanitize(c.Version))

	if m, _ := filepath.Glob(filepath.Join(dir, "*.tgz")); len(m) == 1 {
		return m[0], nil
	}

	tmp, err := os.MkdirTemp(g.CacheDir, "pull-")
	if err != nil {
		return "", err
	}

	defer func() { _ = os.RemoveAll(tmp) }()

	args := []string{"pull"}
	if c.Kind == KindRepo {
		args = append(args, c.Name, "--repo", c.Ref)
	} else {
		args = append(args, c.Ref)
	}

	args = append(args, "--version", c.Version, "--destination", tmp)

	if _, err := g.helm(ctx, args...); err != nil {
		return "", err
	}

	m, _ := filepath.Glob(filepath.Join(tmp, "*.tgz"))
	if len(m) != 1 {
		return "", fmt.Errorf("helm pull produced %d archives, want 1", len(m))
	}

	if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
		return "", err
	}

	if err := os.Rename(tmp, dir); err != nil && !os.IsExist(err) {
		// A concurrent run won the rename; use its copy.
		if m2, _ := filepath.Glob(filepath.Join(dir, "*.tgz")); len(m2) == 1 {
			return m2[0], nil
		}

		return "", err
	}

	m, _ = filepath.Glob(filepath.Join(dir, "*.tgz"))
	if len(m) != 1 {
		return "", fmt.Errorf("cache entry %s is not one archive", dir)
	}

	return m[0], nil
}

// login authenticates helm to a private ECR registry once per run:
// `aws ecr get-login-password | helm registry login`. The credential
// lands in the gate's own isolated registry config, never the user's.
func (g *Gate) login(ctx context.Context, host string) error {
	g.pullMu.Lock()
	p, ok := g.logins[host]

	if !ok {
		p = &pullOnce{}
		g.logins[host] = p
	}

	g.pullMu.Unlock()

	p.once.Do(func() { p.err = g.ecrLogin(ctx, host) })

	return p.err
}

func (g *Gate) ecrLogin(ctx context.Context, host string) error {
	m := ecrHost.FindStringSubmatch(host)
	if m == nil {
		return fmt.Errorf("%q is not a private ECR registry host", host)
	}

	awsBin := g.AWS
	if awsBin == "" {
		awsBin = "aws"
	}

	// Bounded: a laptop with a stale SSO session must fail fast, not wait.
	actx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	args := []string{"ecr", "get-login-password", "--region", m[1]}
	if g.AWSProfile != "" {
		args = append(args, "--profile", g.AWSProfile)
	}

	cmd := exec.CommandContext(actx, awsBin, args...)

	var pw, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &pw, &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}

		return fmt.Errorf("aws ecr get-login-password: %s", msg)
	}

	if strings.TrimSpace(pw.String()) == "" {
		return fmt.Errorf("aws ecr get-login-password returned no password")
	}

	if _, err := g.helmStdin(ctx, &pw, "registry", "login", host, "--username", "AWS", "--password-stdin"); err != nil {
		return fmt.Errorf("helm registry login: %w", err)
	}

	return nil
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")

	return line
}

func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '/' || r == ':' {
			return '_'
		}

		return r
	}, s)
}

// helm runs the helm binary and returns stdout; on failure the error is
// helm's own stderr, trimmed.
func (g *Gate) helm(ctx context.Context, args ...string) ([]byte, error) {
	return g.helmStdin(ctx, nil, args...)
}

// helmStdin is helm with stdin, for `registry login --password-stdin`.
func (g *Gate) helmStdin(ctx context.Context, stdin io.Reader, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, g.Helm, args...)
	cmd.Stdin = stdin
	// An isolated Helm state: a developer's own `helm repo add` entries
	// (same URL, other name) must not change what `--repo` resolves to.
	cmd.Env = append(os.Environ(),
		"HELM_CACHE_HOME="+filepath.Join(g.CacheDir, "helm-cache"),
		"HELM_REPOSITORY_CACHE="+filepath.Join(g.CacheDir, "helm-cache", "repository"),
		"HELM_REPOSITORY_CONFIG="+filepath.Join(g.CacheDir, "helm-cache", "repositories.yaml"),
		"HELM_REGISTRY_CONFIG="+filepath.Join(g.CacheDir, "helm-cache", "registry.json"),
	)

	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr

	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}

		return nil, fmt.Errorf("%s", msg)
	}

	return stdout.Bytes(), nil
}

// extractShipped writes the file rel of the pulled chart archive tgz to the
// gate's work directory and returns its path. The archive's single top-level
// directory is the chart's name; a missing file is os.ErrNotExist.
func (g *Gate) extractShipped(tgz, rel string) (string, error) {
	f, err := os.Open(tgz)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()

	zr, err := gzip.NewReader(f)
	if err != nil {
		return "", err
	}
	defer func() { _ = zr.Close() }()

	clean := filepath.ToSlash(filepath.Clean(rel))
	if strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", fmt.Errorf("value file %s leaves the chart", rel)
	}

	tr := tar.NewReader(zr)

	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return "", os.ErrNotExist
		}

		if err != nil {
			return "", err
		}

		_, name, ok := strings.Cut(h.Name, "/")
		if !ok || name != clean || h.Typeflag != tar.TypeReg {
			continue
		}

		dir, err := os.MkdirTemp(g.work, "shipped-")
		if err != nil {
			return "", err
		}

		p := filepath.Join(dir, filepath.Base(clean))

		out, err := os.Create(p)
		if err != nil {
			return "", err
		}

		if _, err := io.Copy(out, tr); err != nil { //nolint:gosec // a values file of a chart the gate pulled
			_ = out.Close()

			return "", err
		}

		return p, out.Close()
	}
}
