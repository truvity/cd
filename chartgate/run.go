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
	"strings"
	"sync"
	"time"
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
		Sources []SourceRevision // selected Git checkouts, including values refs
	}

	// Gate carries everything a run needs.
	Gate struct {
		Root     string // repository root
		CacheDir string // pulled charts live under here
		Helm     string // helm binary
		Jobs     int    // concurrent helm invocations
		// SelfRepo recognizes this repository's git URL in an Application.
		SelfRepo func(string) bool
		// Sources bind an Application's exact repoURL/targetRevision to an
		// existing checkout at an immutable commit. The gate never checks out Git.
		Sources []SourceMapping
		// MaxDepth and MaxRenders bound Application-producing chart trees.
		// Zero selects 32 levels and 10000 distinct render contexts.
		MaxDepth   int
		MaxRenders int

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

		work     string // per-run scratch for value files
		pullMu   sync.Mutex
		pulls    map[string]*pullOnce
		logins   map[string]*pullOnce // ECR host -> login outcome (err only)
		renders  map[string]*renderOnce
		sem      chan struct{}
		explicit bool
	}

	renderOnce struct {
		once   sync.Once
		result Result
		output []byte
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

func (g *Gate) renderLocal(ctx context.Context, app Application, src Source) ([]byte, error) {
	root, err := g.sourceRoot(src)
	if err != nil {
		return nil, err
	}
	chart, err := checkoutPath(root, src.Path)
	if err != nil {
		return nil, err
	}
	files, err := g.valueFiles(app, src, true, "")
	if err != nil {
		return nil, err
	}

	args := []string{"template", app.Release(src), chart}
	args = append(args, namespaceArgs(app)...)

	for _, f := range files {
		args = append(args, "-f", f)
	}

	return g.helm(ctx, args...)
}

func namespaceArgs(app Application) []string {
	if app.Namespace == "" {
		return nil
	}

	return []string{"--namespace", app.Namespace}
}

// check renders one remote chart and keeps its output for child discovery.
func (g *Gate) check(ctx context.Context, t target) (res Result, output []byte) {
	res = Result{Cluster: t.cluster, App: t.app.Name, Chart: t.chart.String()}
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

			return res, nil
		}
	}

	tgz, err := g.pull(ctx, t.chart)
	if err != nil {
		res.Status, res.Detail = StatusFail, "helm pull: "+err.Error()

		return res, nil
	}

	files, err := g.valueFiles(t.app, t.source, false, tgz)
	if err != nil {
		res.Status, res.Detail = StatusFail, err.Error()

		return res, nil
	}

	args := []string{"template", t.app.Release(t.source), tgz}
	args = append(args, namespaceArgs(t.app)...)

	for _, f := range files {
		args = append(args, "-f", f)
	}

	output, err = g.helm(ctx, args...)
	if err != nil {
		res.Status, res.Detail = StatusFail, err.Error()

		return res, nil
	}

	res.Status = StatusOK

	return res, output
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
