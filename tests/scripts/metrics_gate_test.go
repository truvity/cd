// Package scripts_test tests charts/cd-pipeline/files/verify-metrics-gate.sh:
// the script the cd-pipeline chart's metrics gate AnalysisTemplate runs -- a
// bake-window monitoring check against a Prometheus-compatible query
// endpoint, rather than anything Argo CD or the Kubernetes API knows about.
//
// The script is exercised as a real subprocess against a fake `curl` on
// PATH (jq stays real -- the script's own JSON parsing is exactly what these
// tests prove). The fake answers by PromQL substring: which query the script
// sent decides which canned VictoriaMetrics-shaped response it gets back,
// driven by environment variables per scenario -- there is no live store to
// capture a fixture from.
package scripts_test

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot resolves the checkout root from this file's own path, the same
// way internal/rendertest's repoRoot does, so the test finds the script
// under charts/cd-pipeline/files regardless of the working directory `go test`
// is run from.
func repoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot resolve caller path")
	}

	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

// fakeCurl writes a stub that answers the two endpoints
// verify-metrics-gate.sh calls: the issuer's /token (a token exchange) and
// the observability stack's /api/v1/query (a PromQL instant query). It
// decides its answer from the request's OWN data, exactly like fakeKubectl
// decides from the jsonpath it was asked for -- so the test proves the
// SCRIPT's parsing and query construction, not a hand simulation of what a
// store would say for each case.
//
// It also simulates the TWO curl flags the script's http_request() relies
// on (`-o <file>` for the body, `-w '%{http_code}'` for the status printed
// to stdout) and, when CURL_ARGS_LOG is set, appends every invocation's
// full argv to that file (\x1f-joined, one call per line) -- so a test can
// assert on WHICH flags a specific call used, exactly what
// TestMetricsGateExchangeUsesTheSystemTrustStoreButMetricsQueryUsesThePrivateCA
// needs to tell "the token exchange passed no --cacert" apart from "the
// metrics query did".
func fakeCurl(t *testing.T, dir string) {
	t.Helper()

	// POSIX sh: find the data-urlencode value(s) and the final positional
	// arg (the URL) out of an arbitrary curl invocation, then answer by
	// substring on the query/URL. `eval "url=\${$#}"` reads the LAST
	// positional parameter without needing an array (dash has none).
	const script = `#!/bin/sh
set -eu

if [ -n "${CURL_ARGS_LOG:-}" ]; then
    { printf 'CALL'; for a in "$@"; do printf '\037%s' "$a"; done; printf '\n'; } >> "$CURL_ARGS_LOG"
fi

eval "url=\${$#}"
query=""
outfile=""
prev=""
for a in "$@"; do
    if [ "$prev" = "--data-urlencode" ]; then
        query="$query&$a"
    fi
    if [ "$prev" = "-o" ]; then
        outfile="$a"
    fi
    prev="$a"
done

# emit writes $1 (the body) to -o's file if the caller passed one,
# otherwise to stdout, then prints $2 (the HTTP status) to stdout -- the
# same split real curl's -o file -w '%{http_code}' produces, which is
# exactly what http_request()'s HTTP_STATUS capture (a command
# substitution around curl) reads back.
emit() {
    if [ -n "$outfile" ]; then
        printf '%s' "$1" > "$outfile"
    else
        printf '%s' "$1"
    fi
    printf '%s' "$2"
}

json_value() {
    # $1 = the scalar value to report as a PromQL instant-query result.
    printf '{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[0,"%s"]}]}}' "$1"
}
json_empty() {
    printf '{"status":"success","data":{"resultType":"vector","result":[]}}'
}

case "$url" in
    */token)
        if [ "${FAKE_TOKEN_TLS_FAIL:-}" = "1" ]; then
            # A TRANSPORT failure: curl never gets a response at all (the
            # shape a certificate-verification error takes) -- nothing
            # written to the body, "000" for the status curl's own -w
            # falls back to, and a NONZERO exit, so http_request()'s
            # CURL_EXIT is what tells the caller apart from an ordinary
            # HTTP error.
            [ -n "$outfile" ] && : > "$outfile"
            printf '000'
            exit 60
        fi
        if [ "${FAKE_TOKEN_FAIL:-}" = "1" ]; then
            emit '{"error":"invalid_client"}' 400
            exit 0
        fi
        emit "$(printf '{"access_token":"%s","expires_in":%s}' "${FAKE_ACCESS_TOKEN:-test-access-token}" "${FAKE_EXPIRES_IN:-3600}")" 200
        exit 0
        ;;
esac

case "$query" in
    *'kube_job_status_failed'*)
        name="$(printf '%s' "$query" | sed -n 's/.*job_name="\([^"]*\)".*/\1/p')"
        if [ -n "$name" ] && [ "$name" = "${FAKE_FAILED_JOB_NAME:-}" ]; then
            emit "$(json_value 1)" 200
        else
            emit "$(json_empty)" 200
        fi
        ;;
    *'kube_job_status_succeeded'*)
        name="$(printf '%s' "$query" | sed -n 's/.*job_name="\([^"]*\)".*/\1/p')"
        if [ -n "$name" ] && [ "$name" = "${FAKE_SUCCEEDED_JOB_NAME:-}" ]; then
            emit "$(json_value 1)" 200
        else
            emit "$(json_empty)" 200
        fi
        ;;
    *'kube_pod_labels'*)
        # Phase 1 readiness queries. FAKE_ROLL_POLLS=n: the roll takes n
        # polls (the OLD-version query reports FAKE_OLD_PODS pods for the
        # first n polls, then 0); the first poll after that stamps the
        # ready file the probe queries and the alerts case read.
        # FAKE_NEVER_READY=1 keeps the old pods forever.
        state="$(dirname "$0")"
        # FAKE_EXTRA_PODS: other live+Ready pods in the store, as
        # "namespace|instance|version" entries separated by ';'. A query
        # is counted against a pod only if its selector names that
        # pod's namespace AND instance -- a selector missing either
        # matches EVERY extra pod, which is how an unscoped query shows.
        q_ns="$(printf '%s' "$query" | sed -n 's/.*[{ ,]namespace="\([^"]*\)".*/\1/p' | head -n1)"
        q_inst="$(printf '%s' "$query" | sed -n 's/.*label_app_kubernetes_io_instance="\([^"]*\)".*/\1/p' | head -n1)"
        extra_old=0
        extra_new=0
        oldifs="$IFS"
        IFS=';'
        for pod in ${FAKE_EXTRA_PODS:-}; do
            IFS='|'
            set -- $pod
            IFS=';'
            [ -n "$q_ns" ] && [ "$q_ns" != "$1" ] && continue
            [ -n "$q_inst" ] && [ "$q_inst" != "$2" ] && continue
            if [ "$3" = "$CHART_VERSION" ]; then extra_new=$((extra_new + 1)); else extra_old=$((extra_old + 1)); fi
        done
        IFS="$oldifs"
        case "$query" in
            *'version!=""'*)
                polls=$(( $(cat "$state/polls" 2>/dev/null || echo 0) + 1 ))
                echo "$polls" > "$state/polls"
                if [ "${FAKE_NEVER_READY:-}" = "1" ] || [ "$polls" -le "${FAKE_ROLL_POLLS:-0}" ]; then
                    emit "$(json_value "$(( ${FAKE_OLD_PODS:-2} + extra_old ))")" 200
                elif [ "$extra_old" -gt 0 ]; then
                    emit "$(json_value "$extra_old")" 200
                else
                    [ -f "$state/ready-at" ] || date +%s > "$state/ready-at"
                    emit "$(json_value 0)" 200
                fi
                ;;
            *)
                if [ "${FAKE_NEVER_READY:-}" = "1" ]; then emit "$(json_value 0)" 200; else emit "$(json_value "$((2 + extra_new))")" 200; fi
                ;;
        esac
        ;;
    *'kube_pod_container_status_restarts_total'*)
        if [ -n "${FAKE_RESTARTS:-}" ]; then emit "$(json_value "$FAKE_RESTARTS")" 200; else emit "$(json_empty)" 200; fi
        ;;
    *'ALERTS{alertstate="firing"'*)
        # The script's own job_name filter lives IN the query text, not
        # in the fake: FAKE_ALERT_JOB_NAME names which job's alert (if
        # any) FAKE_ALERTS represents, unset meaning a namespace-wide
        # alert with no job_name label at all. promoted_job is pulled
        # out of the query the same way the kube_job_status_* cases
        # above do -- the exclusion clause reads job_name!~"..." (no
        # '='), so it never matches this job_name="..." pattern, only
        # the inclusion clause (the promoted job's own literal name)
        # does.
        # FAKE_ALERTS_UNTIL_READY=1: an alert that fires only while the
        # roll is still going (before the ready point) -- a gate that
        # asks in phase 1 sees it and fails.
        if [ "${FAKE_ALERTS_UNTIL_READY:-}" = "1" ] && [ ! -f "$(dirname "$0")/ready-at" ]; then FAKE_ALERTS=1; FAKE_ALERT_NAME=KubePodNotReady; fi
        promoted_job="$(printf '%s' "$query" | sed -n 's/.*job_name="\([^"]*\)".*/\1/p')"
        alert_job="${FAKE_ALERT_JOB_NAME:-}"
        alert_name="${FAKE_ALERT_NAME:-KubeJobFailed}"
        firing=0
        if [ -n "${FAKE_ALERTS:-}" ]; then
            case "$alert_job" in
                "") firing=1 ;;                        # no job_name label: always counted
                "$promoted_job") firing=1 ;;            # this release's own e2e Job
                "${PROJECT_NAME}-e2e-"*) firing=0 ;;    # an OTHER version's e2e Job: excluded
                *) firing=1 ;;                          # an unrelated job_name: still counted
            esac
        fi
        case "$query" in
            *'count(ALERTS{alertstate="firing"'*)
                if [ "$firing" = "1" ]; then emit "$(json_value 1)" 200; else emit "$(json_value 0)" 200; fi
                ;;
            *)
                if [ "$firing" = "1" ]; then
                    if [ -n "$alert_job" ]; then
                        emit "$(printf '{"status":"success","data":{"resultType":"vector","result":[{"metric":{"alertname":"%s","job_name":"%s"},"value":[0,"1"]}]}}' "$alert_name" "$alert_job")" 200
                    else
                        emit "$(printf '{"status":"success","data":{"resultType":"vector","result":[{"metric":{"alertname":"%s"},"value":[0,"1"]}]}}' "$alert_name")" 200
                    fi
                else
                    emit "$(json_empty)" 200
                fi
                ;;
        esac
        ;;
    *'count by (__name__)'*)
        emit "$(json_empty)" 200
        ;;
    *'result="failure"'*|*"${FAKE_METRIC_NAME:-probe_journey_total}"*)
        # The store is POLLUTED before the ready point: the roll's own 3
        # failed journeys out of 31 sit in any window that reaches back
        # past it. A window longer than (now - ready) + 1s, or any probe
        # read before the ready point exists, sees them.
        state="$(dirname "$0")"
        win="$(printf '%s' "$query" | sed -n 's/.*\[\([0-9]*\)s\].*/\1/p')"
        polluted=1
        if [ -f "$state/ready-at" ] && [ -n "$win" ]; then
            [ "$win" -le $(( $(date +%s) - $(cat "$state/ready-at") + 1 )) ] && polluted=0
        fi
        case "$query" in
            *'result="failure"'*)
                if [ "$polluted" = "1" ]; then emit "$(json_value 3)" 200
                elif [ -n "${FAKE_PROBE_FAILURE:-}" ]; then emit "$(json_value "$FAKE_PROBE_FAILURE")" 200
                else emit "$(json_empty)" 200; fi
                ;;
            *)
                if [ "$polluted" = "1" ]; then emit "$(json_value 31)" 200
                elif [ -n "${FAKE_PROBE_TOTAL:-}" ]; then emit "$(json_value "$FAKE_PROBE_TOTAL")" 200
                else emit "$(json_empty)" 200; fi
                ;;
        esac
        ;;
    *)
        echo "fake curl: unrecognized query: $query" >&2
        exit 1
        ;;
esac
`

	path := filepath.Join(dir, "curl")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// runMetricsGate executes verify-metrics-gate.sh with a fake curl on PATH
// and the given scenario environment layered on top of a fast, deterministic
// baseline (a 2s window polled every second, bounded by MAX_ITERATIONS so a
// scenario that never reaches its deadline -- a bug, not a designed case --
// fails the test rather than hanging it). curlLog is fakeCurl's own record
// of every invocation's argv (see fakeCurl's CURL_ARGS_LOG), empty if
// nothing called curl at all.
func runMetricsGate(t *testing.T, chartVersion string, extraEnv map[string]string) (code int, stdout, stderr, curlLog string) {
	t.Helper()

	dir := t.TempDir()
	fakeCurl(t, dir)

	saTokenFile := filepath.Join(dir, "sa-token")
	if err := os.WriteFile(saTokenFile, []byte("fake-sa-jwt"), 0o600); err != nil {
		t.Fatal(err)
	}
	caBundle := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caBundle, []byte("fake-ca"), 0o600); err != nil {
		t.Fatal(err)
	}
	curlArgsLog := filepath.Join(dir, "curl-calls.log")

	script := filepath.Join(repoRoot(t), "charts", "cd-pipeline", "files", "verify-metrics-gate.sh")

	// Built as a map, baseline first and extraEnv layered on top, so a
	// scenario can OVERRIDE a baseline var (METRICS_BASE_URL, CA_BUNDLE)
	// rather than only ever add a new one -- a duplicate name later in a
	// flat argv/envp is not reliably "last wins" across shells, so this
	// keeps the process's actual environment to one entry per name.
	scenario := map[string]string{
		"CHART_VERSION":         chartVersion,
		"PROJECT_NAME":          "shop",
		"WORKLOAD_NAMESPACE":    "shop",
		"WORKLOAD_RELEASE":      "shop",
		"ISSUER_HOST":           "issuer.example.com",
		"METRICS_BASE_URL":      "https://metrics.internal.example/prometheus",
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
		"CURL_ARGS_LOG":         curlArgsLog,
	}
	for k, v := range extraEnv {
		scenario[k] = v
	}

	env := append(os.Environ(), "PATH="+dir+":"+os.Getenv("PATH"))
	for k, v := range scenario {
		env = append(env, k+"="+v)
	}

	cmd := exec.CommandContext(t.Context(), "sh", script)
	cmd.Env = env

	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf

	err := cmd.Run()

	logBytes, _ := os.ReadFile(curlArgsLog)
	curlLog = string(logBytes)

	if err == nil {
		return 0, outBuf.String(), errBuf.String(), curlLog
	}

	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), outBuf.String(), errBuf.String(), curlLog
	}

	t.Fatalf("running verify-metrics-gate.sh: %v\nstdout: %s\nstderr: %s", err, outBuf.String(), errBuf.String())

	return -1, "", "", ""
}

// jobName mirrors the script's own {name}-e2e-<version, dots to dashes>
// formula, so a test can name the promoted version's job without repeating
// a hardcoded string that would silently drift from the script.
func jobName(version string) string {
	return "shop-e2e-" + strings.ReplaceAll(version, ".", "-")
}

func TestMetricsGatePassesWhenEverythingIsHealthy(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

// NO DATA for the prober counter is a FAIL, not a skip -- and it must log a
// discovery query so a wrong metric-name guess is correctable from the
// first live run rather than a silent timeout.
func TestMetricsGateFailsOnNoProberData(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "NO DATA") {
		t.Errorf("stderr does not mention NO DATA:\n%s", stderr)
	}
	if !strings.Contains(stderr, "discovery") {
		t.Errorf("stderr does not log a discovery query for the missing metric:\n%s", stderr)
	}
}

func TestMetricsGateFailsOnHighFailureRatio(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "100",
		"FAKE_PROBE_FAILURE":      "10", // 10% > the 5% ceiling
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "phase 2") || !strings.Contains(stderr, "failure ratio 0.1000") {
		t.Errorf("stderr does not mention the failure ratio:\n%s", stderr)
	}
}

func TestMetricsGateFailsOnContainerRestart(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
		"FAKE_RESTARTS":           "1",
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "restart") {
		t.Errorf("stderr does not mention the restart:\n%s", stderr)
	}
}

// A namespace-wide alert (no job_name label) still firing at the END of
// the bake window fails the gate, and the failure logs the alertname so
// the run's own log is self-explaining.
func TestMetricsGateFailsOnFiringAlert(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
		"FAKE_ALERTS":             "1",
		"FAKE_ALERT_NAME":         "SomeNamespaceWideAlert",
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "firing") {
		t.Errorf("stderr does not mention the firing alert:\n%s", stderr)
	}
	if !strings.Contains(stderr, "alertname=SomeNamespaceWideAlert") {
		t.Errorf("stderr does not log the firing alert's name:\n%s", stderr)
	}
}

// A live incident: a promotion failed 6s
// in on a `KubeJobFailed` for the PREVIOUS version's still-unpruned e2e
// Job, before the new version had even rolled out. Alerts are now read
// only in phase 2, after the ready point: an alert that fires during the
// roll and is gone by the time the version is serving must not fail it.
func TestMetricsGatePassesWhenAnAlertFiresDuringTheRollButClearsBeforeReady(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_ROLL_POLLS":         "3",
		"FAKE_ALERTS_UNTIL_READY": "1",
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

// The roll's own failed journeys (3 of 31 sit in the store before the
// ready point) are ignored: the bake counts only increments after the
// ready point, and the fake serves the polluted numbers to any window
// that reaches back past it. Passing proves no query reached back.
func TestMetricsGateIgnoresFailuresThatHappenedDuringTheRoll(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_ROLL_POLLS":         "3",
		"FAKE_PROBE_TOTAL":        "12",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "phase 1 done") {
		t.Errorf("stderr does not report the end of phase 1:\n%s", stderr)
	}
}

// Phase 1 never ends (old-version pods stay): fail, naming the phase and
// the pod counts, without ever judging the probes.
func TestMetricsGateFailsInPhase1WhenTheRollNeverFinishes(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"PHASE1_SECONDS":          "3",
		"FAKE_NEVER_READY":        "1",
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "phase 1") || !strings.Contains(stderr, "0/0 pods at "+version) {
		t.Errorf("stderr does not name phase 1 and the pod counts:\n%s", stderr)
	}
}

// Ready, but the prober delivers too few journeys within the bake budget.
func TestMetricsGateFailsInPhase2OnTooFewSamples(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "4",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "phase 2") || !strings.Contains(stderr, "only 4 journeys") || !strings.Contains(stderr, "want >= 10") {
		t.Errorf("stderr does not explain the sample shortfall:\n%s", stderr)
	}
}

// Enough samples are clean but the e2e Job never succeeded: phase 2 fails
// on that alone, saying so.
func TestMetricsGateFailsInPhase2WhenTheE2EJobNeverSucceeds(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":   "20",
		"FAKE_PROBE_FAILURE": "0",
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "phase 2") || !strings.Contains(stderr, "kube_job_status_succeeded") {
		t.Errorf("stderr does not explain the missing e2e success:\n%s", stderr)
	}
}

// A firing KubeJobFailed for an OLDER version's e2e Job (already pruned
// or not, ArgoCD's timing is not this release's problem) must not fail
// THIS promotion's gate.
func TestMetricsGatePassesOnFiringAlertForAnOlderVersionsE2EJob(t *testing.T) {
	t.Parallel()

	promoted := "1.28.2"
	code, stdout, stderr, _ := runMetricsGate(t, promoted, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(promoted),
		"FAKE_ALERTS":             "1",
		"FAKE_ALERT_NAME":         "KubeJobFailed",
		"FAKE_ALERT_JOB_NAME":     jobName("1.28.1"), // the PREVIOUS promotion's leftover e2e Job
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

// A firing KubeJobFailed for THIS promotion's OWN e2e Job must fail the
// gate via the alerts check itself (not only via the separate
// kube_job_status_failed fast-fail check) -- FAKE_SUCCEEDED_JOB_NAME /
// FAKE_FAILED_JOB_NAME are deliberately left unset so this failure can
// only come from the alerts condition.
func TestMetricsGateFailsOnFiringAlertForThePromotedE2EJob(t *testing.T) {
	t.Parallel()

	promoted := "1.28.2"
	code, stdout, stderr, _ := runMetricsGate(t, promoted, map[string]string{
		"FAKE_PROBE_TOTAL":    "20",
		"FAKE_PROBE_FAILURE":  "0",
		"FAKE_ALERTS":         "1",
		"FAKE_ALERT_NAME":     "KubeJobFailed",
		"FAKE_ALERT_JOB_NAME": jobName(promoted),
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "alertname=KubeJobFailed") || !strings.Contains(stderr, jobName(promoted)) {
		t.Errorf("stderr does not log the promoted job's own firing alert:\n%s", stderr)
	}
}

// Any e2e Job failure fails the gate immediately, whatever the traffic
// metrics say -- a promotion whose own suite already failed must never
// pass on prober noise alone.
func TestMetricsGateFailsWhenE2EJobFailed(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":     "20",
		"FAKE_PROBE_FAILURE":   "0",
		"FAKE_FAILED_JOB_NAME": jobName(version),
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "kube_job_status_failed") {
		t.Errorf("stderr does not mention kube_job_status_failed:\n%s", stderr)
	}
}

// E2E_JOB_REQUIRED=false (a prober-only project): the project runs no e2e Job as
// part of delivery, so a Job that failed or never ran neither fails the gate nor
// holds it back; the prober's journeys alone decide.
func TestMetricsGateIgnoresTheE2EJobWhenNotRequired(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	for name, env := range map[string]map[string]string{
		"no job at all": {},
		"a failed job":  {"FAKE_FAILED_JOB_NAME": jobName(version)},
	} {
		env["FAKE_PROBE_TOTAL"] = "20"
		env["FAKE_PROBE_FAILURE"] = "0"
		env["E2E_JOB_REQUIRED"] = "false"

		code, stdout, stderr, _ := runMetricsGate(t, version, env)
		if code != 0 {
			t.Errorf("%s: exit code = %d, want 0\nstdout: %s\nstderr: %s", name, code, stdout, stderr)
		}
	}
}

// A DIFFERENT version's e2e Job succeeding must not satisfy this gate: the
// script names the job it reads from CHART_VERSION, so a store that answers
// "some job succeeded" for the wrong name must still read as NOT the
// promoted version having succeeded.
func TestMetricsGateFailsWhenADifferentVersionsJobSucceeded(t *testing.T) {
	t.Parallel()

	promoted := "1.26.1"
	code, stdout, stderr, _ := runMetricsGate(t, promoted, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName("1.26.0"), // the PREVIOUS promotion, not this one
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, jobName(promoted)) {
		t.Errorf("stderr does not name the promoted job %s:\n%s", jobName(promoted), stderr)
	}
}

func TestMetricsGateFailsWhenTokenExchangeFails(t *testing.T) {
	t.Parallel()

	code, stdout, stderr, _ := runMetricsGate(t, "1.26.1", map[string]string{
		"FAKE_TOKEN_FAIL": "1",
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "token") {
		t.Errorf("stderr does not mention the token failure:\n%s", stderr)
	}
}

// A TRANSPORT failure (the shape a TLS certificate error takes -- exactly
// what shipped live: pinning the private CA bundle to the PUBLIC issuer
// call made curl refuse its own real, publicly-trusted certificate) must
// say so: "curl exit 60", not just an empty body with no explanation. This
// is the failure http_request()'s CURL_EXIT check exists for -- before it,
// the script printed "returned no access_token: " with nothing after the
// colon, which is what made the live failure hard to read from the Job's
// own log.
func TestMetricsGateReportsCurlExitCodeOnTransportFailure(t *testing.T) {
	t.Parallel()

	code, stdout, stderr, _ := runMetricsGate(t, "1.26.1", map[string]string{
		"FAKE_TOKEN_TLS_FAIL": "1",
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "curl exit 60") {
		t.Errorf("stderr does not report curl's own exit code:\n%s", stderr)
	}
}

// The token exchange (a PUBLIC host,
// issuer.example.com, with a publicly-trusted certificate) must verify
// against the image's default system trust store -- no --cacert at all --
// while the metrics query (metrics.internal.example, signed
// only by the private root) must pass --cacert. Mixing these up either
// way is a live outage: the private bundle alone refuses the public host
// (curl exit 60, proven live), and the system store alone would refuse the
// private host with the same error, just later.
func TestMetricsGateExchangeUsesSystemTrustStoreButMetricsQueryUsesThePrivateCA(t *testing.T) {
	t.Parallel()

	version := "1.26.1"
	code, stdout, stderr, curlLog := runMetricsGate(t, version, map[string]string{
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	var tokenCalls, queryCalls int

	for _, line := range strings.Split(strings.TrimRight(curlLog, "\n"), "\n") {
		if line == "" {
			continue
		}

		fields := strings.Split(line, "\x1f")
		if len(fields) < 2 || fields[0] != "CALL" {
			t.Fatalf("malformed curl log line: %q", line)
		}

		url := fields[len(fields)-1]

		hasCacert := false

		for _, f := range fields[1:] {
			if f == "--cacert" {
				hasCacert = true
			}
		}

		switch {
		case strings.HasSuffix(url, "/token"):
			tokenCalls++

			if hasCacert {
				t.Errorf("token exchange call passed --cacert %v -- the public issuer must verify against the system trust store, not the private CA", fields)
			}
		case strings.Contains(url, "/api/v1/query"):
			queryCalls++

			if !hasCacert {
				t.Errorf("metrics query call did not pass --cacert %v -- *.internal.example is signed only by the private root", fields)
			}
		}
	}

	if tokenCalls == 0 {
		t.Fatal("no token exchange call observed in the curl log -- fakeCurl's CURL_ARGS_LOG wiring may be broken")
	}

	if queryCalls == 0 {
		t.Fatal("no metrics query call observed in the curl log -- fakeCurl's CURL_ARGS_LOG wiring may be broken")
	}
}

// An endpoint reached over a plain HTTP in-cluster Service address, with NO
// CA_BUNDLE at all -- the mirror image of the private-root case above, where the metrics query
// DOES pass --cacert. Passing none here must still let the gate pass: an
// empty CA_BUNDLE is the correct value for a call with nothing to verify,
// not a missing one.
func TestMetricsGateHTTPHostPassesNoCACertOnTheMetricsQuery(t *testing.T) {
	t.Parallel()

	version := "1.28.1"
	code, stdout, stderr, curlLog := runMetricsGate(t, version, map[string]string{
		"METRICS_BASE_URL":        "http://metrics-proxy.monitoring.svc:8427/prometheus",
		"CA_BUNDLE":               "",
		"FAKE_PROBE_TOTAL":        "20",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}

	var queryCalls int

	for _, line := range strings.Split(strings.TrimRight(curlLog, "\n"), "\n") {
		if line == "" {
			continue
		}

		fields := strings.Split(line, "\x1f")
		if len(fields) < 2 || fields[0] != "CALL" {
			t.Fatalf("malformed curl log line: %q", line)
		}

		url := fields[len(fields)-1]
		if !strings.Contains(url, "/api/v1/query") {
			continue
		}

		queryCalls++

		for _, f := range fields[1:] {
			if f == "--cacert" {
				t.Errorf("metrics query call passed --cacert %v -- an http:// METRICS_BASE_URL with an empty CA_BUNDLE has nothing to verify", fields)
			}
		}
	}

	if queryCalls == 0 {
		t.Fatal("no metrics query call observed in the curl log -- fakeCurl's CURL_ARGS_LOG wiring may be broken")
	}
}

// kube_pod_* series are cluster-wide. Pods that are not the release's own
// -- another namespace at other versions (cert-manager, argocd), and the
// CNPG database in the SAME namespace (version 18, another instance) --
// must not count as "old version still serving".
func TestMetricsGatePhase1IgnoresPodsOutsideTheReleaseScope(t *testing.T) {
	t.Parallel()

	version := "1.31.0"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"FAKE_EXTRA_PODS":         "cert-manager|cert-manager|v1.19.0;argocd|argocd|3.1.0;shop|shop-infra|18;other-ns|shop|0.9.0",
		"FAKE_PROBE_TOTAL":        "12",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
}

// A genuinely old pod of the SAME namespace and instance still blocks.
func TestMetricsGatePhase1BlocksOnAnOldPodOfTheSameRelease(t *testing.T) {
	t.Parallel()

	version := "1.31.0"
	code, stdout, stderr, _ := runMetricsGate(t, version, map[string]string{
		"PHASE1_SECONDS":          "3",
		"FAKE_EXTRA_PODS":         "shop|shop|1.30.0",
		"FAKE_PROBE_TOTAL":        "12",
		"FAKE_PROBE_FAILURE":      "0",
		"FAKE_SUCCEEDED_JOB_NAME": jobName(version),
	})
	if code != 1 {
		t.Fatalf("exit code = %d, want 1\nstdout: %s\nstderr: %s", code, stdout, stderr)
	}
	if !strings.Contains(stderr, "phase 1") || !strings.Contains(stderr, "1 live pods at another version") {
		t.Errorf("stderr does not name phase 1 and the old pod:\n%s", stderr)
	}
}
