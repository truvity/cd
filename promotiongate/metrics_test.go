package promotiongate_test

// The metrics gate against a fake issuer and a fake Prometheus-compatible
// store. The store answers by PromQL substring -- which query the gate sent
// decides which canned response it gets -- driven per scenario by FAKE_*
// knobs, so the tests prove the gate's own query construction and parsing,
// not a simulation of it. Every scenario of the shell script's own suite is
// here, with the same knobs and the same assertions; the ones after them
// cover what the shell suite could not reach (the token's re-exchange, the
// iteration bound, an unreadable response, the trust stores over real TLS).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/truvity/cd/promotiongate"
)

type (
	// fakeStore is the issuer and the store of one scenario.
	fakeStore struct {
		t     *testing.T
		env   map[string]string
		clock *fakeClock

		mu       sync.Mutex
		polls    int
		readyAt  int64
		hasReady bool

		tokenCalls []tokenCall
		queryAuth  []string
	}

	tokenCall struct {
		user, pass   string
		hasBasicAuth bool
		form         map[string]string
	}

	// tweakFn adjusts a run's config after it is read from the environment,
	// given the clock and the scenario's fake (to serve it elsewhere).
	tweakFn func(cfg *promotiongate.MetricsConfig, clock *fakeClock, fake http.Handler)

	gateRun struct {
		code   int
		err    error
		stderr string
		store  *fakeStore
	}
)

var (
	reJobName  = regexp.MustCompile(`.*job_name="([^"]*)"`)
	reNS       = regexp.MustCompile(`.*[{ ,]namespace="([^"]*)"`)
	reInstance = regexp.MustCompile(`.*label_app_kubernetes_io_instance="([^"]*)"`)
	reWindow   = regexp.MustCompile(`.*\[([0-9]*)s\]`)
)

func lastMatch(re *regexp.Regexp, s string) string {
	if m := re.FindStringSubmatch(s); m != nil {
		return m[1]
	}

	return ""
}

func jsonValue(v string) string {
	return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,%q]}]}}`, v)
}

const jsonEmpty = `{"status":"success","data":{"resultType":"vector","result":[]}}`

func (f *fakeStore) get(k string) string { return f.env[k] }

func (f *fakeStore) getOr(k, d string) string {
	if v := f.env[k]; v != "" {
		return v
	}

	return d
}

func (f *fakeStore) now() int64 { return f.clock.Now().Unix() }

func (f *fakeStore) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", f.token)
	mux.HandleFunc("/prometheus/api/v1/query", f.query)

	return mux
}

func (f *fakeStore) token(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		f.t.Errorf("token request: %v", err)
	}

	call := tokenCall{form: map[string]string{}}
	call.user, call.pass, call.hasBasicAuth = r.BasicAuth()

	for k := range r.PostForm {
		call.form[k] = r.PostForm.Get(k)
	}

	f.mu.Lock()
	f.tokenCalls = append(f.tokenCalls, call)
	f.mu.Unlock()

	if f.get("FAKE_TOKEN_FAIL") == "1" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_client"}`))

		return
	}

	_, _ = fmt.Fprintf(w, `{"access_token":%q,"expires_in":%s}`, f.getOr("FAKE_ACCESS_TOKEN", "test-access-token"), f.getOr("FAKE_EXPIRES_IN", "3600"))
}

func (f *fakeStore) query(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		f.t.Errorf("query method %s, want POST", r.Method)
	}

	if err := r.ParseForm(); err != nil {
		f.t.Errorf("query request: %v", err)
	}

	f.mu.Lock()
	f.queryAuth = append(f.queryAuth, r.Header.Get("Authorization"))
	f.mu.Unlock()

	body, status := f.answer(r.PostForm.Get("query"))
	w.WriteHeader(status)
	_, _ = w.Write([]byte(body))
}

// answer is the shell suite's fake curl, case for case and in its order.
func (f *fakeStore) answer(q string) (string, int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if sub := f.get("FAKE_NOT_JSON_FOR"); sub != "" && strings.Contains(q, sub) {
		return "Unauthorized\n", http.StatusUnauthorized
	}

	switch {
	case strings.Contains(q, "kube_job_status_failed"):
		if name := lastMatch(reJobName, q); name != "" && name == f.get("FAKE_FAILED_JOB_NAME") {
			return jsonValue("1"), http.StatusOK
		}

		return jsonEmpty, http.StatusOK
	case strings.Contains(q, "kube_job_status_succeeded"):
		if name := lastMatch(reJobName, q); name != "" && name == f.get("FAKE_SUCCEEDED_JOB_NAME") {
			return jsonValue("1"), http.StatusOK
		}

		return jsonEmpty, http.StatusOK
	case strings.Contains(q, "kube_pod_labels"):
		return f.readiness(q), http.StatusOK
	case strings.Contains(q, "kube_pod_container_status_restarts_total"):
		if v := f.get("FAKE_RESTARTS"); v != "" {
			return jsonValue(v), http.StatusOK
		}

		return jsonEmpty, http.StatusOK
	case strings.Contains(q, `ALERTS{alertstate="firing"`):
		return f.alerts(q), http.StatusOK
	case strings.Contains(q, "count by (__name__)"):
		return jsonEmpty, http.StatusOK
	case strings.Contains(q, `result="failure"`), strings.Contains(q, f.getOr("FAKE_METRIC_NAME", "probe_journey_total")):
		return f.probes(q), http.StatusOK
	default:
		f.t.Errorf("fake store: unrecognized query: %s", q)

		return "", http.StatusInternalServerError
	}
}

// readiness: FAKE_ROLL_POLLS=n, the roll takes n polls (the old-version query
// reports FAKE_OLD_PODS pods for the first n, then 0); the first poll after
// that stamps the ready point the probe and alert answers read.
// FAKE_NEVER_READY=1 keeps the old pods forever. FAKE_EXTRA_PODS: other live
// and Ready pods, "namespace|instance|version" separated by ';', counted
// against a query only if its selector names that pod's namespace AND
// instance -- a selector missing either matches every extra pod, which is how
// an unscoped query shows.
func (f *fakeStore) readiness(q string) string {
	qNS, qInst := lastMatch(reNS, q), lastMatch(reInstance, q)
	version := f.get("CHART_VERSION")
	extraOld, extraNew := 0, 0

	for _, pod := range strings.Split(f.get("FAKE_EXTRA_PODS"), ";") {
		if pod == "" {
			continue
		}

		p := strings.Split(pod, "|")
		if qNS != "" && qNS != p[0] || qInst != "" && qInst != p[1] {
			continue
		}

		if p[2] == version {
			extraNew++
		} else {
			extraOld++
		}
	}

	if !strings.Contains(q, `version!=""`) {
		if f.get("FAKE_NEVER_READY") == "1" {
			return jsonValue("0")
		}

		return jsonValue(strconv.Itoa(2 + extraNew))
	}

	f.polls++

	rollPolls, _ := strconv.Atoi(f.getOr("FAKE_ROLL_POLLS", "0"))
	oldPods, _ := strconv.Atoi(f.getOr("FAKE_OLD_PODS", "2"))

	switch {
	case f.get("FAKE_NEVER_READY") == "1" || f.polls <= rollPolls:
		return jsonValue(strconv.Itoa(oldPods + extraOld))
	case extraOld > 0:
		return jsonValue(strconv.Itoa(extraOld))
	default:
		if !f.hasReady {
			f.readyAt, f.hasReady = f.now(), true
		}

		return jsonValue("0")
	}
}

// alerts: FAKE_ALERT_JOB_NAME names which job's alert (if any) FAKE_ALERTS
// is, unset meaning a namespace-wide alert with no job_name at all; the
// gate's own job_name filter lives in the query text. FAKE_ALERTS_UNTIL_READY
// is an alert that fires only before the ready point.
func (f *fakeStore) alerts(q string) string {
	alertsOn, alertName := f.get("FAKE_ALERTS"), f.getOr("FAKE_ALERT_NAME", "KubeJobFailed")
	if f.get("FAKE_ALERTS_UNTIL_READY") == "1" && !f.hasReady {
		alertsOn, alertName = "1", "KubePodNotReady"
	}

	promoted := lastMatch(reJobName, q)
	alertJob := f.get("FAKE_ALERT_JOB_NAME")

	firing := false
	if alertsOn != "" {
		switch {
		case alertJob == "":
			firing = true // no job_name label: always counted
		case alertJob == promoted:
			firing = true // this release's own e2e Job
		case strings.HasPrefix(alertJob, f.get("PROJECT_NAME")+"-e2e-"):
			firing = false // another version's e2e Job: excluded
		default:
			firing = true // an unrelated job_name: still counted
		}
	}

	if strings.Contains(q, `count(ALERTS{alertstate="firing"`) {
		if firing {
			return jsonValue("1")
		}

		return jsonValue("0")
	}

	if !firing {
		return jsonEmpty
	}

	if alertJob != "" {
		return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"alertname":%q,"job_name":%q},"value":[0,"1"]}]}}`, alertName, alertJob)
	}

	return fmt.Sprintf(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{"alertname":%q},"value":[0,"1"]}]}}`, alertName)
}

// probes: the store is POLLUTED before the ready point -- the roll's own 3
// failed journeys out of 31 sit in any window that reaches back past it. A
// window longer than (now - ready) + 1s, or any probe read before the ready
// point exists, sees them.
func (f *fakeStore) probes(q string) string {
	polluted := true

	if win := lastMatch(reWindow, q); f.hasReady && win != "" {
		w, _ := strconv.ParseInt(win, 10, 64)
		if w <= f.now()-f.readyAt+1 {
			polluted = false
		}
	}

	if strings.Contains(q, `result="failure"`) {
		switch {
		case polluted:
			return jsonValue("3")
		case f.get("FAKE_PROBE_FAILURE") != "":
			return jsonValue(f.get("FAKE_PROBE_FAILURE"))
		default:
			return jsonEmpty
		}
	}

	switch {
	case polluted:
		return jsonValue("31")
	case f.get("FAKE_PROBE_TOTAL") != "":
		return jsonValue(f.get("FAKE_PROBE_TOTAL"))
	default:
		return jsonEmpty
	}
}

// runMetricsGate runs the gate against a fake issuer and store with the
// scenario's environment layered over a fast, deterministic baseline (a 5s
// bake polled every second, bounded by MAX_ITERATIONS so a scenario that
// never reaches its deadline fails rather than hangs). The token endpoint
// and the store are plain HTTP unless the scenario points them elsewhere.
func runMetricsGate(t *testing.T, chartVersion string, extraEnv map[string]string, tweak ...tweakFn) gateRun {
	t.Helper()

	dir := t.TempDir()

	saTokenFile := filepath.Join(dir, "sa-token")
	if err := os.WriteFile(saTokenFile, []byte("fake-sa-jwt\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	caBundle := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caBundle, []byte("fake-ca"), 0o600); err != nil {
		t.Fatal(err)
	}

	clock := newFakeClock()
	store := &fakeStore{t: t, clock: clock}
	srv := httptest.NewServer(store.handler())
	t.Cleanup(srv.Close)

	env := map[string]string{
		"CHART_VERSION":         chartVersion,
		"PROJECT_NAME":          "url-shortener",
		"WORKLOAD_NAMESPACE":    "url-shortener",
		"WORKLOAD_RELEASE":      "url-shortener",
		"ISSUER_HOST":           "issuer.example.com",
		"METRICS_BASE_URL":      srv.URL + "/prometheus",
		"TOKEN_ENDPOINT":        srv.URL + "/token",
		"SA_TOKEN_FILE":         saTokenFile,
		"CA_BUNDLE":             caBundle,
		"PHASE1_SECONDS":        "6",
		"BAKE_SECONDS":          "5",
		"SCRAPE_LAG_SECONDS":    "1",
		"POLL_SECONDS":          "1",
		"MIN_JOURNEYS":          "10",
		"MAX_FAILURE_RATIO":     "0.05",
		"CHECK_FIRING_ALERTS":   "true",
		"PROBER_JOURNEY_METRIC": "probe_journey_total",
		"MAX_ITERATIONS":        "40",
		"JQ":                    "/opt/jq/jq",
	}
	for k, v := range extraEnv {
		env[k] = v
	}

	store.env = env

	cfg, err := promotiongate.MetricsConfigFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	var log syncBuffer

	cfg.Now, cfg.Sleep, cfg.Log = clock.Now, clock.Sleep, &log

	for _, f := range tweak {
		f(&cfg, clock, store.handler())
	}

	err = promotiongate.MetricsGate(t.Context(), cfg)

	return gateRun{code: promotiongate.ExitCode(err), err: err, stderr: log.String(), store: store}
}

// jobName mirrors the gate's own {name}-e2e-<version, dots to dashes>.
func jobName(version string) string {
	return "url-shortener-e2e-" + strings.ReplaceAll(version, ".", "-")
}

func wantCode(t *testing.T, r gateRun, code int) {
	t.Helper()

	if r.code != code {
		t.Fatalf("exit code = %d (%v), want %d\nstderr:\n%s", r.code, r.err, code, r.stderr)
	}
}

func wantLog(t *testing.T, r gateRun, subs ...string) {
	t.Helper()

	for _, s := range subs {
		if !strings.Contains(r.stderr, s) {
			t.Errorf("stderr does not contain %q:\n%s", s, r.stderr)
		}
	}
}

func TestMetricsGatePassesWhenEverythingIsHealthy(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 0)
	wantLog(t, r, "metrics gate PASSED for "+jobName(version)+": 20 journeys after the roll (0 failed, ratio 0.0000)")
}

// NO DATA for the prober counter is a FAIL, not a skip, and it logs a
// discovery query so a wrong metric-name guess is correctable from the first
// live run.
func TestMetricsGateFailsOnNoProberData(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 1)
	wantLog(t, r, "NO DATA", "discovery")
}

func TestMetricsGateFailsOnHighFailureRatio(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "100",
		"FAKE_PROBE_FAILURE":      "10", // 10% > the 5% ceiling
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 1)
	wantLog(t, r, "phase 2", "failure ratio 0.1000")
}

func TestMetricsGateFailsOnContainerRestart(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
		"FAKE_RESTARTS":           "1",
	})
	wantCode(t, r, 1)
	wantLog(t, r, "restart")
}

// A namespace-wide alert (no job_name label) firing in the bake fails the
// gate, and the failure logs the alertname.
func TestMetricsGateFailsOnFiringAlert(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
		"FAKE_ALERTS":             "1",
		"FAKE_ALERT_NAME":         "SomeNamespaceWideAlert",
	})
	wantCode(t, r, 1)
	wantLog(t, r, "firing", "alertname=SomeNamespaceWideAlert")
}

// Alerts are read only in phase 2, after the ready point: an alert that fires
// during the roll (the previous version's leftover e2e Job, say) and is gone
// by the time the version is serving must not fail it.
func TestMetricsGatePassesWhenAnAlertFiresDuringTheRollButClearsBeforeReady(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_ROLL_POLLS":         "3",
		"FAKE_ALERTS_UNTIL_READY": "1",
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 0)
}

// The roll's own failed journeys (3 of 31 before the ready point) are
// ignored: the bake counts only increments after it, and the fake serves the
// polluted numbers to any window that reaches back past it.
func TestMetricsGateIgnoresFailuresThatHappenedDuringTheRoll(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_ROLL_POLLS":         "3",
		"FAKE_PROBE_TOTAL":        "12",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 0)
	wantLog(t, r, "phase 1 done after 3s: 2/2 pods at 1.26.1 ready, 0 old-version pods left")
}

// Phase 1 never ends (old-version pods stay): fail, naming the phase and the
// pod counts, without ever judging the probes.
func TestMetricsGateFailsInPhase1WhenTheRollNeverFinishes(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"PHASE1_SECONDS":          "3",
		"FAKE_NEVER_READY":        "1",
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 1)
	wantLog(t, r, "phase 1", "0/0 pods at "+version, "discovery: metrics named like kube_pod_labels", "not rolled out yet")
}

// Ready, but the prober delivers too few journeys within the bake budget.
func TestMetricsGateFailsInPhase2OnTooFewSamples(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "4",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 1)
	wantLog(t, r, "phase 2", "only 4 journeys", "want >= 10")
}

// Enough clean samples but the e2e Job never succeeded: phase 2 fails on that
// alone, saying so.
func TestMetricsGateFailsInPhase2WhenTheE2EJobNeverSucceeds(t *testing.T) {
	t.Parallel()

	r := runMetricsGate(t, "1.26.1", map[string]string{
		"FAKE_PROBE_TOTAL":   "20",
		"FAKE_PROBE_FAILURE": "0",
	})
	wantCode(t, r, 1)
	wantLog(t, r, "phase 2", "kube_job_status_succeeded", "(got '')")
}

// A firing KubeJobFailed for an OLDER version's e2e Job must not fail THIS
// promotion's gate.
func TestMetricsGatePassesOnFiringAlertForAnOlderVersionsE2EJob(t *testing.T) {
	t.Parallel()

	promoted := "1.28.2"
	r := runMetricsGate(t, promoted, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(promoted),
		"FAKE_ALERTS":             "1",
		"FAKE_ALERT_NAME":         "KubeJobFailed",
		"FAKE_ALERT_JOB_NAME":     jobName("1.28.1"), // the previous promotion's leftover
	})
	wantCode(t, r, 0)
}

// A firing KubeJobFailed for THIS promotion's own e2e Job fails the gate via
// the alerts check itself: the Job's own status is left unset, so the
// failure can only come from the alert.
func TestMetricsGateFailsOnFiringAlertForThePromotedE2EJob(t *testing.T) {
	t.Parallel()

	promoted := "1.28.2"
	r := runMetricsGate(t, promoted, map[string]string{
		"FAKE_PROBE_TOTAL":    "20",
		"FAKE_PROBE_FAILURE":  "0",
		"FAKE_ALERTS":         "1",
		"FAKE_ALERT_NAME":     "KubeJobFailed",
		"FAKE_ALERT_JOB_NAME": jobName(promoted),
	})
	wantCode(t, r, 1)
	wantLog(t, r, "  alertname=KubeJobFailed job_name="+jobName(promoted))
}

// Any e2e Job failure fails the gate at once, whatever the traffic says.
func TestMetricsGateFailsWhenE2EJobFailed(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":     "20",
		"FAKE_PROBE_FAILURE":   "0",
		"FAKE_FAILED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 1)
	wantLog(t, r, "kube_job_status_failed=1", "kubectl -n url-shortener logs job/"+jobName(version))
}

// E2E_JOB_REQUIRED=false: no e2e Job runs as part of delivery, so a Job that
// failed or never ran neither fails the gate nor holds it back.
func TestMetricsGateIgnoresTheE2EJobWhenNotRequired(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	for name, env := range map[string]map[string]string{
		"no job at all": {},
		"a failed job":  {"FAKE_FAILED_JOB_NAME": jobName(version)},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env["FAKE_PROBE_TOTAL"] = "20"
			env["FAKE_PROBE_FAILURE"] = "0"
			env["E2E_JOB_REQUIRED"] = "false"

			wantCode(t, runMetricsGate(t, version, env), 0)
		})
	}
}

// A DIFFERENT version's e2e Job succeeding must not satisfy this gate.
func TestMetricsGateFailsWhenADifferentVersionsJobSucceeded(t *testing.T) {
	t.Parallel()

	promoted := "1.26.1"
	r := runMetricsGate(t, promoted, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName("1.26.0"),
	})
	wantCode(t, r, 1)
	wantLog(t, r, jobName(promoted))
}

func TestMetricsGateFailsWhenTokenExchangeFails(t *testing.T) {
	t.Parallel()

	r := runMetricsGate(t, "1.26.1", map[string]string{"FAKE_TOKEN_FAIL": "1"})
	wantCode(t, r, 1)
	wantLog(t, r, `returned no access_token (HTTP 400): {"error":"invalid_client"}`, "metrics-gate FAILED: token re-exchange")
}

// A TRANSPORT failure (a certificate the client does not trust) says so with
// curl's own exit code, not an empty body with no explanation.
func TestMetricsGateReportsCurlExitCodeOnTransportFailure(t *testing.T) {
	t.Parallel()

	// httptest's own certificate: signed by nothing the system trusts.
	untrusted := httptest.NewTLSServer(http.NotFoundHandler())
	t.Cleanup(untrusted.Close)

	r := runMetricsGate(t, "1.26.1", map[string]string{"TOKEN_ENDPOINT": untrusted.URL + "/token"})
	wantCode(t, r, 1)
	wantLog(t, r, "curl exit 60", "HTTP 000", "metrics-gate FAILED: token re-exchange")
}

// The token exchange (a public issuer) verifies against the system trust
// store, the metrics query (a private root) against CA_BUNDLE alone. Real TLS
// on both sides: each server's certificate is signed by the root only its
// own trust store holds, so mixing them up either way fails the run.
func TestMetricsGateExchangeUsesSystemTrustStoreButMetricsQueryUsesThePrivateCA(t *testing.T) {
	t.Parallel()

	privateCA, err := newTestCA("private test root")
	if err != nil {
		t.Fatal(err)
	}

	bundle := filepath.Join(t.TempDir(), "private-ca.crt")
	if err := os.WriteFile(bundle, privateCA.pem, 0o600); err != nil {
		t.Fatal(err)
	}

	version := "1.26.1"

	var issuer, store *httptest.Server

	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
		"CA_BUNDLE":               bundle,
	}, func(cfg *promotiongate.MetricsConfig, _ *fakeClock, h http.Handler) {
		// The same fake, served twice: the issuer under the public root,
		// the store under the private one.
		issuer = publicCA.tlsServer(t, h)
		store = privateCA.tlsServer(t, h)
		cfg.TokenEndpoint = issuer.URL + "/token"
		cfg.MetricsQueryURL = store.URL + "/prometheus/api/v1/query"
	})
	wantCode(t, r, 0)

	// And the other way round fails on each side.
	r = runMetricsGate(t, version, map[string]string{"CA_BUNDLE": bundle}, func(cfg *promotiongate.MetricsConfig, _ *fakeClock, _ http.Handler) {
		cfg.TokenEndpoint = store.URL + "/token" // private root, system store: refused
	})
	wantCode(t, r, 1)
	wantLog(t, r, "token exchange at "+store.URL+"/token: curl exit 60")

	r = runMetricsGate(t, version, map[string]string{"CA_BUNDLE": bundle, "PHASE1_SECONDS": "2"}, func(cfg *promotiongate.MetricsConfig, _ *fakeClock, _ http.Handler) {
		cfg.MetricsQueryURL = issuer.URL + "/prometheus/api/v1/query" // public root, private bundle: refused
	})
	wantCode(t, r, 1)
	wantLog(t, r, "metrics query at "+issuer.URL+"/prometheus/api/v1/query: curl exit 60")
}

// An http:// store with an empty CA_BUNDLE has nothing to verify and passes.
func TestMetricsGateHTTPHostPassesNoCACertOnTheMetricsQuery(t *testing.T) {
	t.Parallel()

	version := "1.28.1"
	r := runMetricsGate(t, version, map[string]string{
		"CA_BUNDLE":               "",
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 0)
}

// kube_pod_* series are cluster-wide: pods that are not the release's own --
// other namespaces at other versions, the release's database in the SAME
// namespace (another instance) -- must not count as old versions serving.
func TestMetricsGatePhase1IgnoresPodsOutsideTheReleaseScope(t *testing.T) {
	t.Parallel()

	version := "1.31.0"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_EXTRA_PODS":         "cert-manager|cert-manager|v1.19.0;argocd|argocd|3.1.0;url-shortener|url-shortener-infra|18;other-ns|url-shortener|0.9.0",
		"FAKE_PROBE_TOTAL":        "12",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 0)
}

// A genuinely old pod of the SAME namespace and instance still blocks.
func TestMetricsGatePhase1BlocksOnAnOldPodOfTheSameRelease(t *testing.T) {
	t.Parallel()

	version := "1.31.0"
	r := runMetricsGate(t, version, map[string]string{
		"PHASE1_SECONDS":          "3",
		"FAKE_EXTRA_PODS":         "url-shortener|url-shortener|1.30.0",
		"FAKE_PROBE_TOTAL":        "12",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 1)
	wantLog(t, r, "phase 1", "1 live pods at another version")
}

// ---------------------------------------------------------------------------
// Beyond the shell suite.

// The exchange is an RFC 8693 token exchange with the client's Basic auth
// and no secret; every query carries the minted token.
func TestMetricsGateExchangeAndQueryRequests(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
		"FAKE_ACCESS_TOKEN":       "minted",
		"EXCHANGE_AUDIENCE":       "the-audience",
	})
	wantCode(t, r, 0)

	if len(r.store.tokenCalls) != 1 {
		t.Fatalf("%d token calls, want 1 (a 3600s token outlives the run)", len(r.store.tokenCalls))
	}

	c := r.store.tokenCalls[0]
	if !c.hasBasicAuth || c.user != "metrics-gate" || c.pass != "" {
		t.Errorf("basic auth = %v %q:%q, want metrics-gate with no secret", c.hasBasicAuth, c.user, c.pass)
	}

	want := map[string]string{
		"grant_type":         "urn:ietf:params:oauth:grant-type:token-exchange",
		"subject_token":      "fake-sa-jwt",
		"subject_token_type": "urn:ietf:params:oauth:token-type:jwt",
		"audience":           "the-audience",
	}
	for k, v := range want {
		if c.form[k] != v {
			t.Errorf("token form %s = %q, want %q", k, c.form[k], v)
		}
	}

	for _, a := range r.store.queryAuth {
		if a != "Bearer minted" {
			t.Fatalf("query Authorization = %q, want Bearer minted", a)
		}
	}
}

// A token that would expire within one poll is re-exchanged, with the
// ServiceAccount token read afresh (the kubelet rotates it under the pod).
func TestMetricsGateReExchangesBeforeExpiryWithAFreshSubjectToken(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	n := 0
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_ROLL_POLLS":         "3",
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
		"FAKE_EXPIRES_IN":         "3",
		"POLL_SECONDS":            "2",
		"PHASE1_SECONDS":          "20",
	}, func(cfg *promotiongate.MetricsConfig, clock *fakeClock, _ http.Handler) {
		file := cfg.SATokenFile
		clock.onSleep = func() {
			n++
			_ = os.WriteFile(file, []byte(fmt.Sprintf("rotated-%d\n", n)), 0o600)
		}
	})
	wantCode(t, r, 0)

	calls := r.store.tokenCalls
	if len(calls) < 3 {
		t.Fatalf("%d token calls, want a re-exchange at every poll of a 3s token polled every 2s", len(calls))
	}

	if calls[0].form["subject_token"] != "fake-sa-jwt" || calls[len(calls)-1].form["subject_token"] != fmt.Sprintf("rotated-%d", n) {
		t.Errorf("subject tokens %q ... %q, want the file's content at each exchange", calls[0].form["subject_token"], calls[len(calls)-1].form["subject_token"])
	}
}

// expires_in absent is 300 seconds, so a short run exchanges once.
func TestMetricsGateDefaultsExpiresInTo300(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
		"FAKE_EXPIRES_IN":         "null",
	})
	wantCode(t, r, 0)

	if len(r.store.tokenCalls) != 1 {
		t.Errorf("%d token calls, want 1", len(r.store.tokenCalls))
	}
}

// A real 0 is not NO DATA: zero journeys is "too few", not "no data".
func TestMetricsGateTellsZeroFromNoData(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 1)
	wantLog(t, r, "only 0 journeys (0 failed)")

	if strings.Contains(r.stderr, "NO DATA") {
		t.Errorf("a 0 read as NO DATA:\n%s", r.stderr)
	}
}

// increase() extrapolates: 9.5 journeys round to 10, which is enough, and a
// failure ratio is printed with four decimals.
func TestMetricsGateRoundsJourneysAndFormatsTheRatio(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	r := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "29.6",
		"FAKE_PROBE_FAILURE":      "0.5",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	wantCode(t, r, 0)
	wantLog(t, r, "30 journeys after the roll (1 failed, ratio 0.0333)")
}

// The iteration bound ends a run that would otherwise poll its full window.
func TestMetricsGateStopsAtMaxIterations(t *testing.T) {
	t.Parallel()

	r := runMetricsGate(t, "1.26.1", map[string]string{
		"PHASE1_SECONDS":   "100000",
		"FAKE_NEVER_READY": "1",
		"MAX_ITERATIONS":   "3",
	})
	wantCode(t, r, 1)
	wantLog(t, r, "gate did not finish within MAX_ITERATIONS=3 (test bound, not the production budget)")
}

// A response that is not JSON ended the script under set -e with jq's
// status; so does the gate.
func TestMetricsGateEndsWithJQStatusOnAnUnreadableResponse(t *testing.T) {
	t.Parallel()

	r := runMetricsGate(t, "1.26.1", map[string]string{"FAKE_NOT_JSON_FOR": "kube_pod_labels"})
	wantCode(t, r, promotiongate.ExitJQ)
	wantLog(t, r, "jq: error")

	if strings.Contains(r.stderr, "FAILED") {
		t.Errorf("a jq failure logged a verdict:\n%s", r.stderr)
	}
}

// A CA bundle that cannot be read fails each https query like curl's exit
// 77, which reads as NO DATA and fails the roll's wait.
func TestMetricsGateUnreadableCABundleIsCurlExit77(t *testing.T) {
	t.Parallel()

	r := runMetricsGate(t, "1.26.1", map[string]string{
		"CA_BUNDLE":         "/nonexistent/ca.crt",
		"METRICS_QUERY_URL": "https://127.0.0.1:1/api/v1/query",
		"PHASE1_SECONDS":    "2",
	})
	wantCode(t, r, 1)
	wantLog(t, r, "curl exit 77", "phase 1 (waiting for the roll): after 2s 0/0 pods")
}

func TestMetricsConfigFromEnv(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"CHART_VERSION": "1.0.0", "PROJECT_NAME": "p", "ISSUER_HOST": "issuer.example.com",
		"METRICS_BASE_URL": "https://metrics.example.com/prometheus", "SA_TOKEN_FILE": "/t",
		"WORKLOAD_NAMESPACE": "ns", "WORKLOAD_RELEASE": "rel", "PHASE1_SECONDS": "600",
		"BAKE_SECONDS": "900", "SCRAPE_LAG_SECONDS": "30", "POLL_SECONDS": "15", "MIN_JOURNEYS": "10",
		"MAX_FAILURE_RATIO": "0.05", "CHECK_FIRING_ALERTS": "false", "PROBER_JOURNEY_METRIC": "m",
		"E2E_JOB_REQUIRED": "",
	}

	cfg, err := promotiongate.MetricsConfigFromEnv(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}

	if cfg.TokenEndpoint != "https://issuer.example.com/token" || cfg.MetricsQueryURL != "https://metrics.example.com/prometheus/api/v1/query" ||
		cfg.ExchangeClientID != "metrics-gate" || cfg.ExchangeAudience != "metrics-gate" || !cfg.E2EJobRequired ||
		cfg.CheckFiringAlerts || cfg.MaxIterations != 0 || cfg.CABundle != "" {
		t.Errorf("defaults: %+v", cfg)
	}

	delete(env, "PROBER_JOURNEY_METRIC")
	env["MIN_JOURNEYS"] = "ten"

	_, err = promotiongate.MetricsConfigFromEnv(func(k string) string { return env[k] })
	if promotiongate.ExitCode(err) != promotiongate.ExitUsage ||
		!strings.Contains(err.Error(), "PROBER_JOURNEY_METRIC is required") || !strings.Contains(err.Error(), "MIN_JOURNEYS") {
		t.Errorf("missing and malformed: %v", err)
	}
}
