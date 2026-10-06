package promotiongate

// The promoted-version check of a Kargo Stage: the question a Promotion
// cannot ask for itself. Kargo's argocd-wait step compares an Application's
// STATE and has no revision field, and a Stage's own health never consults
// Argo CD, so after the pin is pushed nothing asks whether the target
// Application renders the promoted chart at all. A parent Application that is
// still mid-sync leaves the child on the previous version, Synced and
// Healthy.
//
// Run as a Kargo verification, it holds until every target Application (a)
// has the promoted chart version in its spec, (b) has been COMPARED at that
// version (status.sync.comparedTo), and (c) is Synced, Healthy and not
// mid-operation. It reads the Applications with the pod's own ServiceAccount
// through the in-cluster API.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// In-cluster defaults, as the script had them.
const (
	DefaultAPIBase   = "https://kubernetes.default.svc"
	DefaultTokenFile = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	DefaultCAFile    = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
)

// PromotedVersionConfig is the promoted-version check's input;
// [PromotedVersionConfigFromEnv] fills it from the environment the script
// read.
type PromotedVersionConfig struct {
	Apps            string // APPS: Application names, space separated
	ChartRepo       string // CHART_REPO
	ChartName       string // CHART_NAME: empty for an OCI repository that names the chart
	Expected        string // EXPECTED: the targetRevision, prefix included
	ArgoCDNamespace string // ARGOCD_NAMESPACE, default argocd
	WaitSeconds     int64  // WAIT_SECONDS
	Poll            time.Duration

	// APIBase, TokenFile and CAFile locate the API server and the pod's
	// ServiceAccount credentials; empty is the in-cluster default. The
	// token and the CA are read at every request.
	APIBase   string
	TokenFile string
	CAFile    string
	// Client reads the Applications; nil is a client that trusts CAFile
	// alone, with a 20 s limit per request.
	Client *http.Client
	// Now and Sleep are the clock; nil is the wall clock.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	// Out receives the report (the script's stdout); Log what went wrong
	// reading (its stderr). nil is os.Stdout and os.Stderr.
	Out io.Writer
	Log io.Writer
}

// PromotedVersionConfigFromEnv reads the check's environment. The script ran
// under `set -u`, so an UNSET variable is an error while an empty one is a
// value (CHART_NAME is empty for an OCI chart); lookup is os.LookupEnv's
// shape. JQ is accepted and ignored. A missing or malformed variable is an
// [ExitError] with [ExitUsage].
func PromotedVersionConfigFromEnv(lookup func(string) (string, bool)) (PromotedVersionConfig, error) {
	var (
		cfg  PromotedVersionConfig
		errs []error
	)

	set := func(name string) string {
		v, ok := lookup(name)
		if !ok {
			errs = append(errs, fmt.Errorf("%s: parameter not set", name))
		}

		return v
	}

	cfg.Apps = set("APPS")
	cfg.ChartRepo = set("CHART_REPO")
	cfg.ChartName = set("CHART_NAME")
	cfg.Expected = set("EXPECTED")

	if v, _ := lookup("ARGOCD_NAMESPACE"); v != "" {
		cfg.ArgoCDNamespace = v
	}

	if v := set("WAIT_SECONDS"); v != "" {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("WAIT_SECONDS: %q is not an integer", v))
		}

		cfg.WaitSeconds = n
	}

	if v := set("POLL_SECONDS"); v != "" {
		d, err := sleepDuration(v)
		if err != nil {
			errs = append(errs, fmt.Errorf("POLL_SECONDS: %w", err))
		}

		cfg.Poll = d
	}

	if err := errors.Join(errs...); err != nil {
		return cfg, exitf(ExitUsage, err)
	}

	return cfg, nil
}

// sleepDuration is sleep(1)'s argument: a number of seconds, fractions
// allowed, with an optional s, m, h or d suffix.
func sleepDuration(s string) (time.Duration, error) {
	t := strings.TrimSpace(s)
	unit := time.Second

	if t != "" {
		switch t[len(t)-1] {
		case 's':
			t = t[:len(t)-1]
		case 'm':
			unit, t = time.Minute, t[:len(t)-1]
		case 'h':
			unit, t = time.Hour, t[:len(t)-1]
		case 'd':
			unit, t = 24*time.Hour, t[:len(t)-1]
		}
	}

	f, err := strconv.ParseFloat(t, 64)
	if err != nil || f < 0 {
		return 0, fmt.Errorf("invalid time interval %q", s)
	}

	return time.Duration(f * float64(unit)), nil
}

// PromotedVersion runs the check to its verdict: nil when every Application
// that renders the chart is at the promoted version, an [ExitError] when none
// renders it or the wait ran out (the report is already written), ctx's
// error when cancelled.
func PromotedVersion(ctx context.Context, cfg PromotedVersionConfig) error {
	if cfg.ArgoCDNamespace == "" {
		cfg.ArgoCDNamespace = "argocd"
	}

	if cfg.APIBase == "" {
		cfg.APIBase = DefaultAPIBase
	}

	if cfg.TokenFile == "" {
		cfg.TokenFile = DefaultTokenFile
	}

	if cfg.CAFile == "" {
		cfg.CAFile = DefaultCAFile
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}

	if cfg.Out == nil {
		cfg.Out = os.Stdout
	}

	if cfg.Log == nil {
		cfg.Log = os.Stderr
	}

	started := cfg.Now().Unix()
	last := ""
	apps := strings.Fields(cfg.Apps)

	for {
		var (
			pending []string
			matched int
			report  string
		)

		for _, app := range apps {
			line := judgeApp(ctx, cfg, app)
			if ctx.Err() != nil {
				return ctx.Err()
			}

			verdict, msg := line, line
			if i := strings.IndexByte(line, ' '); i >= 0 {
				verdict, msg = line[:i], line[i+1:]
			}

			report += "\n  " + app + ": " + msg

			switch verdict {
			case "skip":
			case "ok":
				matched++
			default:
				matched++

				pending = append(pending, app)
			}
		}

		if len(pending) == 0 && matched > 0 {
			_, _ = fmt.Fprintf(cfg.Out, "promoted version %s is rendered and Synced/Healthy:%s\n", cfg.Expected, report)

			return nil
		}

		if matched == 0 {
			_, _ = fmt.Fprintf(cfg.Out, "FAIL: none of the Applications (%s) renders a source from %s -- check the Stage's applications:%s\n", cfg.Apps, cfg.ChartRepo, report)

			return exitf(ExitFail, fmt.Errorf("none of the Applications (%s) renders a source from %s", cfg.Apps, cfg.ChartRepo))
		}

		now := cfg.Now().Unix()
		if now-started >= cfg.WaitSeconds {
			_, _ = fmt.Fprintf(cfg.Out, "FAIL: after %ds the promoted version %s is not live. Not ready:%s\n", cfg.WaitSeconds, cfg.Expected, report)
			_, _ = fmt.Fprintln(cfg.Out, "If an Application still targets the previous version, the parent that renders its pin has not synced: look for a sync operation still Running on it.")

			return exitf(ExitFail, fmt.Errorf("after %ds the promoted version %s is not live", cfg.WaitSeconds, cfg.Expected))
		}

		if report != last {
			_, _ = fmt.Fprintf(cfg.Out, "waiting (%ds of %ds):%s\n", now-started, cfg.WaitSeconds, report)
			last = report
		}

		if err := cfg.Sleep(ctx, cfg.Poll); err != nil {
			return err
		}
	}
}

// judgeApp reads one Application and returns "<verdict> <message>": skip
// (renders none of this chart), ok, or wait; "" when the Application could
// not be judged (counted as waiting, like the script's empty jq output).
func judgeApp(ctx context.Context, cfg PromotedVersionConfig, app string) string {
	u := cfg.APIBase + "/apis/argoproj.io/v1alpha1/namespaces/" + cfg.ArgoCDNamespace + "/applications/" + app

	token := readFileSub(cfg.TokenFile, cfg.Log)

	client := cfg.Client
	if client == nil {
		c, err := caClient(cfg.CAFile, u, 20*time.Second)
		if err != nil {
			return "wait cannot read Application (HTTP 000)"
		}

		client = c
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return "wait cannot read Application (HTTP 000)"
	}

	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "*/*")

	r := do(client, req)
	if r.status != "200" {
		return "wait cannot read Application (HTTP " + r.status + ")"
	}

	if r.exit != 0 {
		// A body cut short: curl appended its complaint to it and jq could
		// not parse the result.
		_, _ = fmt.Fprintf(cfg.Log, "jq: error (at <stdin>:0): Cannot parse input: %v\n", r.err)

		return ""
	}

	out, _ := jqRaw(r.body, cfg.Log, func(v any) ([]string, error) {
		s, err := judge(v, cfg.ChartRepo, cfg.ChartName, cfg.Expected)
		if err != nil {
			return nil, err
		}

		return []string{s}, nil
	})

	return substitution(out)
}

// ident is a chart source's identity: the repository without scheme or
// trailing slash, plus /chart where the source names one. Infra
// Applications say repoURL registry/charts + chart X, others the whole
// oci://.../X path, and Kargo the latter for both.
func ident(repo, chart any) (string, error) {
	r, ok := repo.(string)
	if !ok {
		return "", jqErrorf("%s cannot be matched, as it is not a string", jqDescribe(repo))
	}

	id := strings.TrimRight(strings.TrimPrefix(r, "oci://"), "/")

	if !truthy(chart) {
		chart = ""
	}

	if chart == "" {
		return id, nil
	}

	suffix, err := addString("/", chart)
	if err != nil {
		return "", err
	}

	return id + suffix, nil
}

// mine is the sources (`x.sources // [x.source]`) of obj under key whose
// identity is the checked chart's.
func mine(obj any, keys []string, want string) ([]any, error) {
	list, err := path(obj, slices.Concat(keys, []string{"sources"})...)
	if err != nil {
		return nil, err
	}

	if !truthy(list) {
		one, err := path(obj, slices.Concat(keys, []string{"source"})...)
		if err != nil {
			return nil, err
		}

		list = []any{one}
	}

	items, err := iterate(list)
	if err != nil {
		return nil, err
	}

	var out []any

	for _, it := range items {
		if it == nil {
			continue
		}

		repo, err := field(it, "repoURL")
		if err != nil {
			return nil, err
		}

		chart, err := field(it, "chart")
		if err != nil {
			return nil, err
		}

		id, err := ident(repo, chart)
		if err != nil {
			return nil, err
		}

		if id == want {
			out = append(out, it)
		}
	}

	return out, nil
}

// revs is `map(.targetRevision // "")`.
func revs(srcs []any) ([]any, error) {
	out := make([]any, 0, len(srcs))

	for _, s := range srcs {
		r, err := field(s, "targetRevision")
		if err != nil {
			return nil, err
		}

		if !truthy(r) {
			r = ""
		}

		out = append(out, r)
	}

	return out, nil
}

func allEqual(vs []any, want string) bool {
	for _, v := range vs {
		if s, ok := v.(string); !ok || s != want {
			return false
		}
	}

	return true
}

// orDefault is `x // d` over a path.
func orDefault(obj any, d any, keys ...string) (any, error) {
	v, err := path(obj, keys...)
	if err != nil {
		return nil, err
	}

	if !truthy(v) {
		return d, nil
	}

	return v, nil
}

// judge is the script's jq program over one Application.
func judge(app any, repo, chart, exp string) (string, error) {
	want, err := ident(repo, chart)
	if err != nil {
		return "", err
	}

	spec, err := mine(app, []string{"spec"}, want)
	if err != nil {
		return "", err
	}

	cmp, err := mine(app, []string{"status", "sync", "comparedTo"}, want)
	if err != nil {
		return "", err
	}

	if len(spec) == 0 {
		return "skip renders no source from " + repo, nil
	}

	specRevs, err := revs(spec)
	if err != nil {
		return "", err
	}

	if !allEqual(specRevs, exp) {
		j, err := joinComma(specRevs)
		if err != nil {
			return "", err
		}

		return "wait spec still targets " + j + ", want " + exp + " (parent app-of-apps not synced?)", nil
	}

	cmpRevs, err := revs(cmp)
	if err != nil {
		return "", err
	}

	if len(cmp) == 0 || !allEqual(cmpRevs, exp) {
		j, err := joinComma(cmpRevs)
		if err != nil {
			return "", err
		}

		if j == "" {
			j = "nothing"
		}

		return "wait spec is at " + exp + " but last comparison was at " + j, nil
	}

	phase, err := orDefault(app, "", "status", "operationState", "phase")
	if err != nil {
		return "", err
	}

	if phase == "Running" {
		return "wait a sync operation is still Running", nil
	}

	sync, err := orDefault(app, "", "status", "sync", "status")
	if err != nil {
		return "", err
	}

	if sync != "Synced" {
		s, _ := orDefault(app, "unknown", "status", "sync", "status")

		return "wait sync status " + interp(s), nil
	}

	health, err := orDefault(app, "", "status", "health", "status")
	if err != nil {
		return "", err
	}

	if health != "Healthy" {
		h, _ := orDefault(app, "unknown", "status", "health", "status")

		return "wait health " + interp(h), nil
	}

	return "ok at " + exp, nil
}
