// Command chartgate renders every Argo CD Helm Application of a
// repository against its pinned chart version and its real values. See
// the chartgate package.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/truvity/cd/chartgate"
)

var errFailures = errors.New("chart-gate failures")

func main() {
	err := run()
	if errors.Is(err, errFailures) {
		os.Exit(1) // the report already said why
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "chartgate:", err)
		os.Exit(2)
	}
}

func run() error {
	root := flag.String("root", ".", "repository root")
	cache := flag.String("cache", defaultCache(), "directory for pulled charts (env CHART_GATE_CACHE)")
	jobs := flag.Int("jobs", max(4, runtime.NumCPU()), "concurrent helm invocations")
	only := flag.String("cluster", "", "comma-separated clusters (default: every values/clusters/*.yaml)")
	requireECR := flag.Bool("require-ecr", envBool("CHART_GATE_REQUIRE_ECR") || envBool("GITHUB_ACTIONS"),
		"fail (instead of skip) when a private ECR chart cannot be logged in to; on by default in GitHub Actions (env CHART_GATE_REQUIRE_ECR)")
	profile := flag.String("aws-profile", os.Getenv("CHART_GATE_AWS_PROFILE"),
		"AWS profile for the ECR login; empty uses the ambient chain (CI: pod identity). Never read from AWS_PROFILE (env CHART_GATE_AWS_PROFILE)")
	selfRepo := flag.String("self-repo", "",
		"substring of this repository's git URL, so an Application sourcing the repository itself is rendered locally (required, e.g. \"myorg/myrepo\")")
	flag.Parse()

	if *selfRepo == "" {
		return errors.New("-self-repo is required")
	}

	helm, err := exec.LookPath("helm")
	if err != nil {
		return fmt.Errorf("helm not on PATH (run inside devbox): %w", err)
	}

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}

	clusters, err := clusterNames(absRoot, *only)
	if err != nil {
		return err
	}

	if err := os.MkdirAll(*cache, 0o755); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	g := &chartgate.Gate{
		Root: absRoot, CacheDir: *cache, Helm: helm, Jobs: *jobs,
		SelfRepo:   func(u string) bool { return strings.Contains(u, *selfRepo) },
		AWSProfile: *profile, RequireECR: *requireECR,
	}

	start := time.Now()
	results := g.Run(ctx, clusters)

	if chartgate.Report(os.Stdout, results, time.Since(start)) > 0 {
		return errFailures
	}

	return nil
}

func envBool(name string) bool {
	return os.Getenv(name) == "true" || os.Getenv(name) == "1"
}

func defaultCache() string {
	if d := os.Getenv("CHART_GATE_CACHE"); d != "" {
		return d
	}

	base, err := os.UserCacheDir()
	if err != nil {
		base = os.TempDir()
	}

	return filepath.Join(base, "chartgate")
}

func clusterNames(root, only string) ([]string, error) {
	if only != "" {
		return strings.Split(only, ","), nil
	}

	m, err := filepath.Glob(filepath.Join(root, "values", "clusters", "*.yaml"))
	if err != nil || len(m) == 0 {
		return nil, fmt.Errorf("no values/clusters/*.yaml under %s", root)
	}

	names := make([]string, 0, len(m))
	for _, f := range m {
		names = append(names, strings.TrimSuffix(filepath.Base(f), ".yaml"))
	}

	sort.Strings(names)

	return names, nil
}
