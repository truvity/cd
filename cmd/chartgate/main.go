// Command chartgate renders every Argo CD Helm Application of a
// repository against its pinned chart version and its real values. See
// the chartgate package.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/truvity/cd/chartgate"
	"go.yaml.in/yaml/v3"
)

var errFailures = errors.New("chart-gate failures")

func main() {
	err := run(os.Args[1:], os.Stdout)
	if errors.Is(err, flag.ErrHelp) {
		return
	}
	if errors.Is(err, errFailures) {
		os.Exit(1) // the report already said why
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "chartgate:", err)
		os.Exit(2)
	}
}

func run(args []string, out io.Writer) error {
	flags := flag.NewFlagSet("chartgate", flag.ContinueOnError)
	var applications stringList
	flags.Var(&applications, "application", "root Application YAML file (repeatable; enables explicit-input mode)")
	sources := flags.String("sources", "", "YAML list of Git source checkout/revision mappings (required with -application)")
	root := flags.String("root", ".", "repository root")
	cache := flags.String("cache", defaultCache(), "directory for pulled charts (env CHART_GATE_CACHE)")
	jobs := flags.Int("jobs", max(4, runtime.NumCPU()), "concurrent helm invocations")
	only := flags.String("cluster", "", "comma-separated clusters (default: every values/clusters/*.yaml)")
	requireECR := flags.Bool("require-ecr", envBool("CHART_GATE_REQUIRE_ECR") || envBool("GITHUB_ACTIONS"),
		"fail (instead of skip) when a private ECR chart cannot be logged in to; on by default in GitHub Actions (env CHART_GATE_REQUIRE_ECR)")
	profile := flags.String("aws-profile", os.Getenv("CHART_GATE_AWS_PROFILE"),
		"AWS profile for the ECR login; empty uses the ambient chain (CI: pod identity). Never read from AWS_PROFILE (env CHART_GATE_AWS_PROFILE)")
	selfRepo := flags.String("self-repo", "",
		"substring of this repository's git URL (required in bootstrap mode, e.g. \"example-org/repo\")")
	maxDepth := flags.Int("max-depth", 32, "maximum Application chart nesting depth")
	maxRenders := flags.Int("max-renders", 10000, "maximum distinct Application render contexts")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() > 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(flags.Args(), " "))
	}

	if len(applications) == 0 && *selfRepo == "" {
		return errors.New("-self-repo is required in bootstrap mode")
	}
	if len(applications) > 0 && (*sources == "" || *only != "") {
		return errors.New("-application requires -sources and cannot be combined with -cluster")
	}
	if len(applications) == 0 && *sources != "" {
		return errors.New("-sources requires -application")
	}
	if *jobs < 1 || *maxDepth < 1 || *maxRenders < 1 {
		return errors.New("-jobs, -max-depth and -max-renders must be positive")
	}

	helm, err := exec.LookPath("helm")
	if err != nil {
		return fmt.Errorf("helm not on PATH (run inside devbox): %w", err)
	}

	absRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}

	var clusters []string
	var apps []chartgate.Application
	var mappings []chartgate.SourceMapping
	if len(applications) > 0 {
		apps, err = readApplications(applications)
		if err == nil {
			mappings, err = readSourceMappings(*sources)
		}
	} else {
		clusters, err = clusterNames(absRoot, *only)
	}
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
		Sources: mappings, MaxDepth: *maxDepth, MaxRenders: *maxRenders,
	}

	start := time.Now()
	var results []chartgate.Result
	if len(applications) > 0 {
		results = g.RunApplications(ctx, apps)
	} else {
		results = g.Run(ctx, clusters)
	}

	if chartgate.Report(out, results, time.Since(start)) > 0 {
		return errFailures
	}

	return nil
}

type stringList []string

func (s *stringList) String() string         { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error { *s = append(*s, value); return nil }

func readApplications(paths []string) ([]chartgate.Application, error) {
	var apps []chartgate.Application
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		parsed, err := chartgate.ParseApplications(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if len(parsed) == 0 {
			return nil, fmt.Errorf("%s: no root Applications", path)
		}
		apps = append(apps, parsed...)
	}
	return apps, nil
}

func readSourceMappings(path string) ([]chartgate.SourceMapping, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	var mappings []chartgate.SourceMapping
	if err := dec.Decode(&mappings); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: expected one YAML source-mapping document", path)
	}
	for i := range mappings {
		if mappings[i].Root != "" && !filepath.IsAbs(mappings[i].Root) {
			mappings[i].Root = filepath.Join(filepath.Dir(path), mappings[i].Root)
		}
	}
	return mappings, nil
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
