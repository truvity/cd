package promotiongate

// The metrics gate: ONE monitoring check against a Prometheus-compatible
// query endpoint, in TWO phases.
//
//  1. Wait (bounded) until the promoted version is FULLY SERVING: every live
//     pod at CHART_VERSION and Ready, no live pod at any other version.
//     Failures while old pods terminate and new ones start are the roll's,
//     not the new version's, and never count.
//  2. Bake on a SAMPLE COUNT, not the wall clock: pass once MIN_JOURNEYS
//     prober journeys have been observed strictly AFTER the ready point (plus
//     a scrape-lag margin) with a failure ratio <= MAX_FAILURE_RATIO, no
//     restarts, no firing alerts and (when required) the version's own e2e
//     Job succeeded; fail early on a breach.
//
// The exit code is the verdict. Every failure message names its phase and
// the numbers behind it.
//
// THE TOKEN: the pod's ServiceAccount has no access to anything by itself. It
// trades its projected token (SA_TOKEN_FILE, read fresh every time, since the
// kubelet rotates it) at an OAuth issuer's /token endpoint, RFC 8693 token
// exchange, for a token of audience EXCHANGE_AUDIENCE, and re-exchanges
// before that one expires.
//
// THE TRUST STORES: the exchange verifies the issuer against the system trust
// store; only the metrics query uses CA_BUNDLE when it is set (a private
// root). Neither is ever disabled or merged with the other: a public issuer's
// certificate is not signed by a private root, and pinning the bundle to the
// exchange refuses it.
//
// NO DATA and 0 are different answers throughout: a query that returns no
// series is empty, never "0".

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// MetricsConfig is the metrics gate's input; [MetricsConfigFromEnv] fills it
// from the environment the script read.
type MetricsConfig struct {
	ChartVersion      string // CHART_VERSION
	ProjectName       string // PROJECT_NAME; the e2e Job is <name>-e2e-<version, dots to dashes>
	WorkloadNamespace string // WORKLOAD_NAMESPACE
	WorkloadRelease   string // WORKLOAD_RELEASE (the app.kubernetes.io/instance label)
	SATokenFile       string // SA_TOKEN_FILE
	CABundle          string // CA_BUNDLE: "" = the system trust store, for the metrics query only

	Phase1Seconds    int64 // PHASE1_SECONDS: budget for the roll
	BakeSeconds      int64 // BAKE_SECONDS: budget for the bake, from the ready point
	ScrapeLagSeconds int64 // SCRAPE_LAG_SECONDS: margin after the ready point before samples count
	PollSeconds      int64 // POLL_SECONDS
	MinJourneys      int64 // MIN_JOURNEYS
	// MaxFailureRatio (MAX_FAILURE_RATIO) is compared as awk compares it:
	// numerically when it is a number.
	MaxFailureRatio     string
	CheckFiringAlerts   bool   // CHECK_FIRING_ALERTS == "true"
	ProberJourneyMetric string // PROBER_JOURNEY_METRIC
	E2EJobRequired      bool   // E2E_JOB_REQUIRED (default true)
	// MaxIterations (MAX_ITERATIONS) bounds the poll loop for a test; 0 is
	// unlimited (the windows bound the run).
	MaxIterations int64

	TokenEndpoint    string // TOKEN_ENDPOINT, default https://<ISSUER_HOST>/token
	MetricsQueryURL  string // METRICS_QUERY_URL, default <METRICS_BASE_URL>/api/v1/query
	ExchangeClientID string // EXCHANGE_CLIENT_ID, default metrics-gate
	ExchangeAudience string // EXCHANGE_AUDIENCE, default metrics-gate

	// TokenClient makes the token exchange; nil is a client of the system
	// trust store.
	TokenClient *http.Client
	// MetricsClient makes the queries; nil is a client that trusts CABundle
	// alone when it is set (read at every call), the system store otherwise.
	MetricsClient *http.Client
	// Now and Sleep are the clock; nil is the wall clock.
	Now   func() time.Time
	Sleep func(ctx context.Context, d time.Duration) error
	// Log receives every log line (the script's stderr); nil is os.Stderr.
	Log io.Writer
}

// MetricsConfigFromEnv reads the metrics gate's environment with the script's
// defaults. JQ is accepted and ignored. A missing or malformed variable is an
// [ExitError] with [ExitUsage].
func MetricsConfigFromEnv(getenv func(string) string) (MetricsConfig, error) {
	var (
		cfg  MetricsConfig
		errs []error
	)

	req := func(name string) string {
		v := getenv(name)
		if v == "" {
			errs = append(errs, fmt.Errorf("%s: %s is required", name, name))
		}

		return v
	}
	num := func(name string) int64 {
		v := req(name)
		if v == "" {
			return 0
		}

		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %q is not an integer", name, v))
		}

		return n
	}
	def := func(name, fallback string) string {
		if v := getenv(name); v != "" {
			return v
		}

		return fallback
	}

	cfg.ChartVersion = req("CHART_VERSION")
	cfg.ProjectName = req("PROJECT_NAME")
	issuerHost := req("ISSUER_HOST")
	metricsBase := req("METRICS_BASE_URL")
	cfg.SATokenFile = req("SA_TOKEN_FILE")
	cfg.CABundle = getenv("CA_BUNDLE")
	cfg.WorkloadNamespace = req("WORKLOAD_NAMESPACE")
	cfg.WorkloadRelease = req("WORKLOAD_RELEASE")
	cfg.Phase1Seconds = num("PHASE1_SECONDS")
	cfg.BakeSeconds = num("BAKE_SECONDS")
	cfg.ScrapeLagSeconds = num("SCRAPE_LAG_SECONDS")
	cfg.PollSeconds = num("POLL_SECONDS")
	cfg.MinJourneys = num("MIN_JOURNEYS")
	cfg.MaxFailureRatio = req("MAX_FAILURE_RATIO")
	cfg.CheckFiringAlerts = req("CHECK_FIRING_ALERTS") == "true"
	cfg.ProberJourneyMetric = req("PROBER_JOURNEY_METRIC")
	cfg.E2EJobRequired = def("E2E_JOB_REQUIRED", "true") == "true"

	if v := def("MAX_ITERATIONS", "0"); v != "" {
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			errs = append(errs, fmt.Errorf("MAX_ITERATIONS: %q is not an integer", v))
		}

		cfg.MaxIterations = n
	}

	cfg.TokenEndpoint = def("TOKEN_ENDPOINT", "https://"+issuerHost+"/token")
	cfg.MetricsQueryURL = def("METRICS_QUERY_URL", metricsBase+"/api/v1/query")
	cfg.ExchangeClientID = def("EXCHANGE_CLIENT_ID", "metrics-gate")
	cfg.ExchangeAudience = def("EXCHANGE_AUDIENCE", "metrics-gate")

	if err := errors.Join(errs...); err != nil {
		return cfg, exitf(ExitUsage, err)
	}

	return cfg, nil
}

// MetricsGate runs the gate to its verdict: nil when it passed, an
// [ExitError] when it failed (already logged), ctx's error when cancelled.
func MetricsGate(ctx context.Context, cfg MetricsConfig) error {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	if cfg.Sleep == nil {
		cfg.Sleep = sleepContext
	}

	if cfg.Log == nil {
		cfg.Log = os.Stderr
	}

	if cfg.TokenClient == nil {
		cfg.TokenClient = newClient(nil, 0)
	}

	g := &metricsRun{
		cfg:     cfg,
		jobName: cfg.ProjectName + "-e2e-" + strings.ReplaceAll(cfg.ChartVersion, ".", "-"),
		plain:   newClient(nil, 0),
	}

	return g.run(ctx)
}

type metricsRun struct {
	cfg     MetricsConfig
	jobName string
	plain   *http.Client // the metrics client when none is given and CA_BUNDLE is empty

	accessToken string
	tokenExpiry int64
	haveExpiry  bool
	iterations  int64
}

func (g *metricsRun) logf(format string, a ...any) { _, _ = fmt.Fprintf(g.cfg.Log, format+"\n", a...) }

func (g *metricsRun) now() int64 { return g.cfg.Now().Unix() }

// fail logs the verdict the way the script's fail() did and ends the run.
func (g *metricsRun) fail(format string, a ...any) error {
	msg := fmt.Sprintf(format, a...)
	g.logf("metrics-gate FAILED: %s", msg)

	return exitf(ExitFail, errors.New(msg))
}

var errNoToken = errors.New("no token")

// exchange reads the CURRENT projected ServiceAccount token and trades it at
// the issuer for one of the exchange audience. The issuer is verified against
// the system trust store, never CA_BUNDLE.
func (g *metricsRun) exchange(ctx context.Context) error {
	c := g.cfg
	saToken := readFileSub(c.SATokenFile, c.Log)

	req, err := postForm(ctx, c.TokenEndpoint, form(
		[2]string{"grant_type", "urn:ietf:params:oauth:grant-type:token-exchange"},
		[2]string{"subject_token", saToken},
		[2]string{"subject_token_type", "urn:ietf:params:oauth:token-type:jwt"},
		[2]string{"audience", c.ExchangeAudience},
	))

	var r response
	if err != nil {
		r = failed(err)
	} else {
		req.SetBasicAuth(c.ExchangeClientID, "")
		r = do(c.TokenClient, req)
	}

	if ctx.Err() != nil {
		return ctx.Err()
	}

	if r.exit != 0 {
		g.logf("%s", curlLine(r))
		g.logf("token exchange at %s: curl exit %d (see curl(1) EXIT CODES), HTTP %s", c.TokenEndpoint, r.exit, r.status)

		return errNoToken
	}

	out, _ := jqRaw(r.body, c.Log, func(v any) ([]string, error) {
		t, err := field(v, "access_token")
		if err != nil || !truthy(t) {
			return nil, err
		}

		return []string{rawOut(t)}, nil
	})

	g.accessToken = substitution(out)
	if g.accessToken == "" {
		g.logf("token exchange at %s returned no access_token (HTTP %s): %s", c.TokenEndpoint, r.status, r.body)

		return errNoToken
	}

	out, _ = jqRaw(r.body, c.Log, func(v any) ([]string, error) {
		e, err := field(v, "expires_in")
		if err != nil {
			return nil, err
		}

		if !truthy(e) {
			return []string{"300"}, nil
		}

		return []string{rawOut(e)}, nil
	})

	expiresIn, err := shellInt(substitution(out))
	if err != nil {
		g.logf("%v", err)

		return exitf(ExitUsage, err)
	}

	g.tokenExpiry = g.now() + expiresIn
	g.haveExpiry = true

	return nil
}

// ensureToken re-exchanges when the current token would expire within one
// more poll interval.
func (g *metricsRun) ensureToken(ctx context.Context) error {
	if g.haveExpiry && g.tokenExpiry-g.now() > g.cfg.PollSeconds {
		return nil
	}

	err := g.exchange(ctx)
	if errors.Is(err, errNoToken) {
		return g.fail("token re-exchange")
	}

	return err
}

// query runs one instant query and returns the raw response body, logging a
// transport failure. The metrics endpoint is the one call that uses
// CA_BUNDLE.
func (g *metricsRun) query(ctx context.Context, q string) (string, error) {
	c := g.cfg

	client := c.MetricsClient
	if client == nil && c.CABundle != "" {
		var err error
		if client, err = caClient(c.CABundle, c.MetricsQueryURL, 0); err != nil {
			r := failed(err)
			g.logf("%s", curlLine(r))
			g.logf("metrics query at %s: curl exit %d (see curl(1) EXIT CODES), HTTP %s: %s", c.MetricsQueryURL, r.exit, r.status, r.body)

			return "", nil
		}
	}

	if client == nil {
		client = g.plain
	}

	var r response

	req, err := postForm(ctx, c.MetricsQueryURL, form([2]string{"query", q}))
	if err != nil {
		r = failed(err)
	} else {
		req.Header.Set("Authorization", "Bearer "+g.accessToken)
		r = do(client, req)
	}

	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	if r.exit != 0 {
		g.logf("%s", curlLine(r))
		g.logf("metrics query at %s: curl exit %d (see curl(1) EXIT CODES), HTTP %s: %s", c.MetricsQueryURL, r.exit, r.status, r.body)
	}

	return r.body, nil
}

// queryValue is the first result's scalar value, or "" when the query
// returned no series: NO DATA and 0 stay apart. A response jq could not
// read ends the run with jq's status, as `set -e` did.
func (g *metricsRun) queryValue(ctx context.Context, q string) (string, error) {
	body, err := g.query(ctx, q)
	if err != nil {
		return "", err
	}

	out, ok := jqRaw(body, g.cfg.Log, func(v any) ([]string, error) {
		x, err := path(v, "data", "result")
		if err != nil {
			return nil, err
		}

		if x, err = index(x, 0); err != nil {
			return nil, err
		}

		if x, err = field(x, "value"); err != nil {
			return nil, err
		}

		if x, err = index(x, 1); err != nil {
			return nil, err
		}

		if !truthy(x) {
			return nil, nil
		}

		return []string{rawOut(x)}, nil
	})
	if !ok {
		return "", exitf(ExitJQ, fmt.Errorf("jq could not read the response to %s", q))
	}

	return substitution(out), nil
}

// discover logs a name-search query, so a run against a store whose metric
// names were guessed wrong says what the real ones are.
func (g *metricsRun) discover(ctx context.Context, pattern string) error {
	g.logf("discovery: metrics named like %s", pattern)

	body, err := g.query(ctx, `count by (__name__) ({__name__=~"`+pattern+`"})`)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprint(g.cfg.Log, body)

	return nil
}

// checkE2EFailed: the promoted version's own e2e Job failing is a verdict in
// either phase -- waiting does not un-fail a Job. max(), not the first series:
// kube-state-metrics exposes one kube_job_status_failed series per failure
// reason, 0 for all but the real one.
func (g *metricsRun) checkE2EFailed(ctx context.Context) error {
	if !g.cfg.E2EJobRequired {
		return nil
	}

	v, err := g.queryValue(ctx, `max(kube_job_status_failed{job_name="`+g.jobName+`"})`)
	if err != nil {
		return err
	}

	if v != "" && numGE(v, "1") {
		return g.fail("e2e job %s reports kube_job_status_failed=%s -- the promoted version's own e2e suite failed (read its Pod logs: kubectl -n %s logs job/%s); that Job is immutable and stays failed for this version, so every verification re-run of %s ends here within seconds until a new version replaces it",
			g.jobName, v, g.cfg.WorkloadNamespace, g.jobName, g.cfg.ChartVersion)
	}

	return nil
}

// nextPoll sleeps one poll interval, never past deadline, and enforces the
// test-only iteration bound.
func (g *metricsRun) nextPoll(ctx context.Context, deadline int64) error {
	if g.cfg.MaxIterations > 0 && g.iterations >= g.cfg.MaxIterations {
		return g.fail("gate did not finish within MAX_ITERATIONS=%d (test bound, not the production budget)", g.cfg.MaxIterations)
	}

	sleepFor := g.cfg.PollSeconds
	if left := deadline - g.now(); left < sleepFor {
		sleepFor = left
	}

	if sleepFor > 0 {
		return g.cfg.Sleep(ctx, time.Duration(sleepFor)*time.Second)
	}

	return nil
}

func (g *metricsRun) run(ctx context.Context) error {
	c := g.cfg

	g.logf("metrics gate: %s %s: phase 1 (wait for the roll, <=%ds) then phase 2 (bake until %d journeys, <=%ds); e2e job %s",
		c.ProjectName, c.ChartVersion, c.Phase1Seconds, c.MinJourneys, c.BakeSeconds, g.jobName)

	if err := g.ensureToken(ctx); err != nil {
		return err
	}

	readyTS, err := g.phase1(ctx)
	if err != nil {
		return err
	}

	return g.phase2(ctx, readyTS)
}

// phase1 waits until the NEW version is fully serving and returns the ready
// point. The version a pod runs is its app.kubernetes.io/version label as
// kube-state-metrics' kube_pod_labels carries it (a Deployment's own counters
// look rolled out before the sync applies the new spec). Every selector names
// both the workload namespace and the Helm release, and the joins are on
// (namespace, pod), so pods that are not the release's own never count.
// Pending|Running excludes finished pods; a Terminating pod is still Running,
// so it counts as old until it is gone.
func (g *metricsRun) phase1(ctx context.Context) (int64, error) {
	c := g.cfg
	scope := fmt.Sprintf(`namespace="%s", label_app_kubernetes_io_instance="%s"`, c.WorkloadNamespace, c.WorkloadRelease)
	podsLive := fmt.Sprintf(`kube_pod_status_phase{namespace="%s", phase=~"Pending|Running"} == 1`, c.WorkloadNamespace)
	podsReady := fmt.Sprintf(`kube_pod_status_ready{namespace="%s", condition="true"} == 1`, c.WorkloadNamespace)
	qNewTotal := fmt.Sprintf(`count(kube_pod_labels{%s, label_app_kubernetes_io_version="%s"} and on(namespace, pod) (%s)) or vector(0)`, scope, c.ChartVersion, podsLive)
	qNewReady := fmt.Sprintf(`count(kube_pod_labels{%s, label_app_kubernetes_io_version="%s"} and on(namespace, pod) (%s)) or vector(0)`, scope, c.ChartVersion, podsReady)
	qOld := fmt.Sprintf(`count(kube_pod_labels{%s, label_app_kubernetes_io_version!="", label_app_kubernetes_io_version!="%s"} and on(namespace, pod) (%s)) or vector(0)`, scope, c.ChartVersion, podsLive)

	g.iterations = 0
	p1Start := g.now()
	p1Deadline := p1Start + c.Phase1Seconds

	for {
		g.iterations++

		if err := g.ensureToken(ctx); err != nil {
			return 0, err
		}

		if err := g.checkE2EFailed(ctx); err != nil {
			return 0, err
		}

		// The ready point is when the QUERY was asked, not when the answer
		// was read: the state it reports is at least that old.
		tQ := g.now()

		var vals [3]string

		for i, q := range []string{qNewTotal, qNewReady, qOld} {
			v, err := g.queryValue(ctx, q)
			if err != nil {
				return 0, err
			}

			if v == "" {
				v = "0"
			}

			vals[i] = v
		}

		newTotal, newReady, oldPods := vals[0], vals[1], vals[2]

		if numGE(newTotal, "1") && numGE(newReady, newTotal) && !numGT(oldPods, "0") {
			g.logf("phase 1 done after %ds: %s/%s pods at %s ready, %s old-version pods left", tQ-p1Start, newReady, newTotal, c.ChartVersion, oldPods)

			return tQ, nil
		}

		if g.now() >= p1Deadline {
			hint := ""

			if newTotal == "0" {
				if err := g.discover(ctx, "kube_pod_labels"); err != nil {
					return 0, err
				}

				hint = fmt.Sprintf(" (no live pod labelled app.kubernetes.io/version=%s: not rolled out yet, or kube-state-metrics does not export the label -- see metricLabelsAllowlist)", c.ChartVersion)
			}

			return 0, g.fail("phase 1 (waiting for the roll): after %ds %s/%s pods at %s ready, %s live pods at another version%s",
				c.Phase1Seconds, newReady, newTotal, c.ChartVersion, oldPods, hint)
		}

		if err := g.nextPoll(ctx, p1Deadline); err != nil {
			return 0, err
		}
	}
}

// phase2 bakes on SAMPLES counted strictly after the ready point: since =
// ready point + SCRAPE_LAG_SECONDS, and every increase() is over [now -
// since], so nothing before it is ever in scope.
func (g *metricsRun) phase2(ctx context.Context, readyTS int64) error {
	c := g.cfg
	since := readyTS + c.ScrapeLagSeconds
	p2Deadline := readyTS + c.BakeSeconds

	var (
		totalI, failureI int64
		ratio            = "0"
		succeededV       string
		haveData         bool
	)

	for {
		g.iterations++

		if err := g.ensureToken(ctx); err != nil {
			return err
		}

		if err := g.checkE2EFailed(ctx); err != nil {
			return err
		}

		if now := g.now(); now > since {
			w := now - since

			restarts, err := g.queryValue(ctx, fmt.Sprintf("sum(increase(kube_pod_container_status_restarts_total[%ds]))", w))
			if err != nil {
				return err
			}

			if restarts != "" && numGT(restarts, "0") {
				return g.fail("phase 2 (bake): container restarts increased %s in the %ds since the roll finished", restarts, w)
			}

			// Firing alerts, judged from the ready point on and never in
			// phase 1. An alert for ANOTHER version's e2e Job never counts.
			if c.CheckFiringAlerts {
				if err := g.checkAlerts(ctx, w); err != nil {
					return err
				}
			}

			totalV, err := g.queryValue(ctx, fmt.Sprintf("sum(increase(%s[%ds]))", c.ProberJourneyMetric, w))
			if err != nil {
				return err
			}

			if totalV != "" {
				haveData = true
				totalI = toInt(totalV)

				failureV, err := g.queryValue(ctx, fmt.Sprintf(`sum(increase(%s{result="failure"}[%ds]))`, c.ProberJourneyMetric, w))
				if err != nil {
					return err
				}

				failureI = toInt(failureV)
				ratio = ratio4(failureI, totalI)

				if totalI >= c.MinJourneys {
					if numGT(ratio, c.MaxFailureRatio) {
						return g.fail("phase 2 (bake): failure ratio %s exceeds %s (%d/%d journeys in the %ds since the roll finished)", ratio, c.MaxFailureRatio, failureI, totalI, w)
					}

					if succeededV, err = g.queryValue(ctx, `kube_job_status_succeeded{job_name="`+g.jobName+`"}`); err != nil {
						return err
					}

					if !c.E2EJobRequired || (succeededV != "" && numGE(succeededV, "1")) {
						g.logf("metrics gate PASSED for %s: %d journeys after the roll (%d failed, ratio %s), %ds baked", g.jobName, totalI, failureI, ratio, w)

						return nil
					}
				}
			}
		}

		if g.now() >= p2Deadline {
			if !haveData {
				if err := g.discover(ctx, "probe.*|probe_.*"); err != nil {
					return err
				}

				return g.fail("phase 2 (bake): no data for %s in %ds after the roll finished (NO DATA = FAIL)", c.ProberJourneyMetric, c.BakeSeconds)
			}

			if totalI < c.MinJourneys {
				return g.fail("phase 2 (bake): only %d journeys (%d failed) in %ds after the roll finished, want >= %d", totalI, failureI, c.BakeSeconds, c.MinJourneys)
			}

			if err := g.discover(ctx, "kube_job_status.*"); err != nil {
				return err
			}

			return g.fail("phase 2 (bake): %d journeys clean (ratio %s) but e2e job %s did not report kube_job_status_succeeded=1 within %ds (got '%s')", totalI, ratio, g.jobName, c.BakeSeconds, succeededV)
		}

		if err := g.nextPoll(ctx, p2Deadline); err != nil {
			return err
		}
	}
}

// checkAlerts fails the bake on any firing alert but an e2e Job alert of
// another version, listing what fires first.
func (g *metricsRun) checkAlerts(ctx context.Context, w int64) error {
	c := g.cfg
	alertsQuery := fmt.Sprintf(`ALERTS{alertstate="firing", job_name!~"%s-e2e-.*"} or ALERTS{alertstate="firing", job_name="%s"}`, c.ProjectName, g.jobName)

	alerts, err := g.queryValue(ctx, "count("+alertsQuery+")")
	if err != nil {
		return err
	}

	if alerts == "" || !numGT(alerts, "0") {
		return nil
	}

	g.logf("firing alert(s) after the roll finished:")

	body, err := g.query(ctx, alertsQuery)
	if err != nil {
		return err
	}

	out, ok := jqRaw(body, c.Log, alertLines)
	_, _ = fmt.Fprint(c.Log, out)

	if !ok {
		return exitf(ExitJQ, fmt.Errorf("jq could not read the response to %s", alertsQuery))
	}

	return g.fail("phase 2 (bake): %s alert(s) firing %ds after the roll finished", alerts, w)
}

// alertLines is `.data.result[] | "  alertname=" + (.metric.alertname //
// "unknown") + (if .metric.job_name then " job_name=" + .metric.job_name elif
// .metric.pod then " pod=" + .metric.pod else "" end)`.
func alertLines(v any) ([]string, error) {
	res, err := path(v, "data", "result")
	if err != nil {
		return nil, err
	}

	items, err := iterate(res)
	if err != nil {
		return nil, err
	}

	var out []string

	for _, it := range items {
		m, err := field(it, "metric")
		if err != nil {
			return out, err
		}

		name, err := field(m, "alertname")
		if err != nil {
			return out, err
		}

		if !truthy(name) {
			name = "unknown"
		}

		line, err := addString("  alertname=", name)
		if err != nil {
			return out, err
		}

		suffix := ""

		job, err := field(m, "job_name")
		if err != nil {
			return out, err
		}

		if truthy(job) {
			if suffix, err = addString(" job_name=", job); err != nil {
				return out, err
			}
		} else {
			pod, err := field(m, "pod")
			if err != nil {
				return out, err
			}

			if truthy(pod) {
				if suffix, err = addString(" pod=", pod); err != nil {
					return out, err
				}
			}
		}

		out = append(out, line+suffix)
	}

	return out, nil
}
