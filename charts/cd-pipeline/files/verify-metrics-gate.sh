#!/bin/sh
#
# The metrics gate of a Kargo Stage: ONE monitoring check that reads a
# Prometheus-compatible query endpoint rather than running any traffic
# itself, in TWO phases:
#
#   1. wait (bounded) until the promoted version is FULLY SERVING -- every
#      live pod at CHART_VERSION and Ready, no live pod at any other
#      version. Failures while old pods terminate and new ones start are
#      the roll's, not the new version's, and never count.
#   2. bake on a SAMPLE COUNT, not the wall clock: pass once MIN_JOURNEYS
#      prober journeys have been observed strictly AFTER the ready point
#      (plus a scrape-lag margin) with a failure ratio <= MAX_FAILURE_RATIO,
#      no restarts, no firing alerts and the version's own e2e Job
#      succeeded; fail early on a breach.
#
# The exit code is the verdict (the AnalysisTemplate keeps failureLimit 0).
# Every failure message names its phase and the numbers behind it.
#
# WHY ONE JOB AND NOT ARGO ROLLOUTS' OWN MULTI-MEASUREMENT METRIC: the
# bake's pass/fail needs the increase() of the SAME counters from the
# ready point to now, not a series of independent point-in-time reads. A
# poll loop inside one Job answers it once, the way a human reading a
# dashboard would.
#
# THE TOKEN: the Job's ServiceAccount has no access to anything by itself.
# It projects its own token for the issuer's audience and trades it at the
# issuer's /token endpoint (RFC 8693 token exchange) for a token scoped to
# client EXCHANGE_CLIENT_ID (default `metrics-gate`), the one door the
# ServiceAccount is granted. The minted token is short-lived, so a run whose
# bake window outlives it re-exchanges before it expires -- reading the SA
# token file fresh every time, since the kubelet rotates it under the pod.
#
# THE METRICS: the query endpoint is expected to confine every query of
# this principal to the cluster and namespace it is granted (a proxy that
# injects the label filters), so the PromQL below adds no cluster filter of
# its own; the version queries name the namespace and release explicitly
# (see SCOPE below).
#
# METRIC NAMES ARE VALUES (env): the stored name of an OpenTelemetry
# prober's `probe.journey` counter depends on how the store ingests OTLP
# (the dotted name, or Prometheus naming: `probe_journey_total`). The
# kube-state-metrics names (kube_job_status_succeeded/failed,
# kube_pod_container_status_restarts_total) are Prometheus-native. On the
# first NO DATA for the prober counter, this script logs a
# `count by (__name__)` discovery query using the one query route it is
# allowed, so a misnamed metric shows in the log rather than timing out
# silently.
set -eu

: "${CHART_VERSION:?CHART_VERSION is required}"
: "${PROJECT_NAME:?PROJECT_NAME is required}"
: "${ISSUER_HOST:?ISSUER_HOST is required}"
: "${METRICS_BASE_URL:?METRICS_BASE_URL is required}"
: "${SA_TOKEN_FILE:?SA_TOKEN_FILE is required}"
# Optional: the CA bundle that verifies METRICS_BASE_URL, for an endpoint
# whose certificate only a private root signs. An endpoint reached over
# plain HTTP (an in-cluster Service address) needs none, and is handed
# none: http_request treats "" as "use the image's default trust store",
# which a plain http:// URL never consults.
: "${CA_BUNDLE:=}"
# Phase 1 budget: how long to wait for the new version to be fully
# serving. Phase 2 budget: how long, counted from the ready point, the
# bake may take to collect MIN_JOURNEYS clean samples. SCRAPE_LAG_SECONDS
# is the margin after the ready point before samples count.
: "${WORKLOAD_NAMESPACE:?WORKLOAD_NAMESPACE is required}"
: "${WORKLOAD_RELEASE:?WORKLOAD_RELEASE is required}"
: "${PHASE1_SECONDS:?PHASE1_SECONDS is required}"
: "${BAKE_SECONDS:?BAKE_SECONDS is required}"
: "${SCRAPE_LAG_SECONDS:?SCRAPE_LAG_SECONDS is required}"
: "${POLL_SECONDS:?POLL_SECONDS is required}"
: "${MIN_JOURNEYS:?MIN_JOURNEYS is required}"
: "${MAX_FAILURE_RATIO:?MAX_FAILURE_RATIO is required}"
: "${CHECK_FIRING_ALERTS:?CHECK_FIRING_ALERTS is required}"
: "${PROBER_JOURNEY_METRIC:?PROBER_JOURNEY_METRIC is required}"
# true (default) = the version's own e2e Job must not fail and must have
# succeeded by the end of the bake. false = the project does not run an
# in-cluster e2e Job as part of delivery: neither is checked and the
# prober's journeys alone carry the verdict.
: "${E2E_JOB_REQUIRED:=true}"
# 0 = unlimited iterations of the poll loop within the window (production
# default -- the window itself, not the iteration count, bounds the run).
# A test sets this to keep a run that would otherwise poll for the full
# window from taking the full window in CI.
: "${MAX_ITERATIONS:=0}"
# Overridable so a test can point at a fake token endpoint / metrics
# endpoint without touching the exchange or query logic.
: "${TOKEN_ENDPOINT:=https://${ISSUER_HOST}/token}"
: "${METRICS_QUERY_URL:=${METRICS_BASE_URL}/api/v1/query}"
: "${EXCHANGE_CLIENT_ID:=metrics-gate}"
: "${EXCHANGE_AUDIENCE:=metrics-gate}"
# The jq BINARY, not a bare PATH lookup: the chart mounts jq's official
# image as a read-only directory volume (containerd only supports a
# DIRECTORY subPath on an image volume, so the pod names the directory
# and this script names the file inside it: the JQ env var and the
# jq-tool volume of the check). A test leaves this at the default and
# relies on a real `jq` on PATH.
: "${JQ:=jq}"

job_name="${PROJECT_NAME}-e2e-$(printf '%s' "$CHART_VERSION" | tr '.' '-')"

log() { echo "$*" >&2; }

fail() {
    log "metrics-gate FAILED: $1"
    exit 1
}

# http_request runs one curl call and sets three globals: BODY (the
# response body), HTTP_STATUS (the final HTTP status, or a curl-chosen
# placeholder such as "000" if the transfer never completed), and
# CURL_EXIT (curl's own exit code). `set -e` never fires on a bad
# status OR a transport failure here -- callers decide what either
# means, and need BOTH to say something more useful than an empty body
# ever could.
#
# $1 is the CA bundle to verify the peer with, or "" for the image's
# default system trust store. THIS ARGUMENT IS THE WHOLE POINT: passing
# a private CA bundle for EVERY call refuses a publicly-certified issuer
# outright -- its certificate was never signed by the private root, so
# pinning --cacert to the private bundle for that call is not "extra
# safe", it is a certificate the server never presents, and curl fails
# with exit 60 ("unable to get local issuer certificate"). Only query()
# passes CA_BUNDLE; exchange_token() passes "" and relies on the image's
# own system trust store (the publicly-trusted roots), and so does
# query() itself when CA_BUNDLE is unset.
#
# Neither bundle is ever disabled or merged: a public host is verified
# against public roots, a private host against the private root, and
# never the other way around.
http_request() {
    cacert="$1"
    shift

    body_file="$(mktemp)"

    set +e
    if [ -n "$cacert" ]; then
        HTTP_STATUS="$(curl -sS --cacert "$cacert" -o "$body_file" -w '%{http_code}' "$@")"
    else
        HTTP_STATUS="$(curl -sS -o "$body_file" -w '%{http_code}' "$@")"
    fi
    CURL_EXIT=$?
    set -e

    BODY="$(cat "$body_file")"
    rm -f "$body_file"
}

# exchange_token reads the CURRENT projected SA token (the kubelet rotates
# the file under the pod, so this never caches it) and trades it at the
# issuer for a `metrics-gate`-audience token, setting ACCESS_TOKEN and
# TOKEN_EXPIRY (unix seconds).
exchange_token() {
    sa_token="$(cat "$SA_TOKEN_FILE")"

    # "" -- the PUBLIC issuer, verified against the system trust store
    # (see http_request's own comment for why this is not the private
    # CA bundle).
    http_request "" -u "${EXCHANGE_CLIENT_ID}:" \
        --data-urlencode "grant_type=urn:ietf:params:oauth:grant-type:token-exchange" \
        --data-urlencode "subject_token=${sa_token}" \
        --data-urlencode "subject_token_type=urn:ietf:params:oauth:token-type:jwt" \
        --data-urlencode "audience=${EXCHANGE_AUDIENCE}" \
        "$TOKEN_ENDPOINT"

    if [ "$CURL_EXIT" -ne 0 ]; then
        log "token exchange at $TOKEN_ENDPOINT: curl exit ${CURL_EXIT} (see curl(1) EXIT CODES), HTTP ${HTTP_STATUS}"
        return 1
    fi

    ACCESS_TOKEN="$(printf '%s' "$BODY" | "$JQ" -r '.access_token // empty')"
    if [ -z "$ACCESS_TOKEN" ]; then
        log "token exchange at $TOKEN_ENDPOINT returned no access_token (HTTP ${HTTP_STATUS}): ${BODY}"
        return 1
    fi

    expires_in="$(printf '%s' "$BODY" | "$JQ" -r '.expires_in // 300')"
    TOKEN_EXPIRY=$(($(date +%s) + expires_in))
}

# ensure_token re-exchanges when the current token would expire within one
# more poll interval, so a bake window longer than the token's lifetime
# never presents an expired bearer mid-window.
ensure_token() {
    now="$(date +%s)"
    if [ -z "${TOKEN_EXPIRY:-}" ] || [ $((TOKEN_EXPIRY - now)) -le "$POLL_SECONDS" ]; then
        exchange_token || fail "token re-exchange"
    fi
}

# query runs one instant PromQL query and prints the raw JSON response.
# CA_BUNDLE, when set: the one call that may reach an endpoint only a
# private root signs (see http_request's own comment).
query() {
    http_request "$CA_BUNDLE" -H "Authorization: Bearer ${ACCESS_TOKEN}" \
        --data-urlencode "query=$1" \
        "$METRICS_QUERY_URL"

    if [ "$CURL_EXIT" -ne 0 ]; then
        log "metrics query at $METRICS_QUERY_URL: curl exit ${CURL_EXIT} (see curl(1) EXIT CODES), HTTP ${HTTP_STATUS}: ${BODY}"
    fi

    printf '%s' "$BODY"
}

# query_value prints the first result's scalar value, or nothing if the
# query returned no series -- NO DATA and "value is 0" are different
# answers and callers must be able to tell them apart.
query_value() {
    query "$1" | "$JQ" -r '.data.result[0].value[1] // empty'
}

# num_ge / num_gt do the floating point comparisons POSIX sh has no
# operator for; VictoriaMetrics prints values as decimals ("3", "0.6667").
num_ge() { awk -v a="$1" -v b="$2" 'BEGIN{exit !(a>=b)}'; }
num_gt() { awk -v a="$1" -v b="$2" 'BEGIN{exit !(a>b)}'; }

# discover logs a name-search query so the first live run against a store
# whose exact metric names were guessed corrects the guess, using ONLY the
# one query route this principal may call.
discover() {
    log "discovery: metrics named like $1"
    query "count by (__name__) ({__name__=~\"$1\"})" >&2
}

# The two queries below read the version a pod runs from kube-state-metrics'
# `kube_pod_labels`, which carries `label_app_kubernetes_io_version` ONLY
# when kube-state-metrics allow-lists it for pods
# (`metricLabelsAllowlist`). The chart under test stamps
# app.kubernetes.io/version on every pod it renders (the promoted
# CHART_VERSION). That label is THE one signal that says which version a
# pod is, independent of what Argo CD has or has not synced yet: a
# Deployment's own generation/replica counters look "fully rolled out"
# BEFORE the sync applies the new spec, and would start the bake on the
# OLD version.
#
# SCOPE: kube_pod_* series are cluster-wide (the query endpoint
# confines the CLUSTER, and its namespace injection is not relied on
# here), and a namespace holds pods that are not the release's own -- a
# CNPG database pod carries app.kubernetes.io/version=18 with instance
# `<release>-infra`, cert-manager and argocd carry versions of their own.
# So every selector names BOTH the workload namespace AND the Helm
# release (`app.kubernetes.io/instance`, allow-listed alongside the
# version), and the joins are on (namespace, pod) so a same-named pod in
# another namespace never matches. Pods without a version label (the
# prober, the e2e Job) are excluded by `!=""`.
# Pending|Running excludes Succeeded/Failed pods (old e2e Job pods that
# have finished are not "old version still serving"); a Terminating pod
# is still Running, so it counts as old until it is really gone.
scope="namespace=\"${WORKLOAD_NAMESPACE}\", label_app_kubernetes_io_instance=\"${WORKLOAD_RELEASE}\""
pods_live="kube_pod_status_phase{namespace=\"${WORKLOAD_NAMESPACE}\", phase=~\"Pending|Running\"} == 1"
pods_ready="kube_pod_status_ready{namespace=\"${WORKLOAD_NAMESPACE}\", condition=\"true\"} == 1"
q_new_total="count(kube_pod_labels{${scope}, label_app_kubernetes_io_version=\"${CHART_VERSION}\"} and on(namespace, pod) (${pods_live})) or vector(0)"
q_new_ready="count(kube_pod_labels{${scope}, label_app_kubernetes_io_version=\"${CHART_VERSION}\"} and on(namespace, pod) (${pods_ready})) or vector(0)"
q_old="count(kube_pod_labels{${scope}, label_app_kubernetes_io_version!=\"\", label_app_kubernetes_io_version!=\"${CHART_VERSION}\"} and on(namespace, pod) (${pods_live})) or vector(0)"

# check_e2e_failed: the promoted version's own e2e Job failing is a
# verdict in either phase -- waiting does not un-fail a Job.
check_e2e_failed() {
    [ "$E2E_JOB_REQUIRED" = "true" ] || return 0
    # max(), not the first series: kube-state-metrics exposes one
    # kube_job_status_failed series PER failure reason (BackoffLimitExceeded,
    # DeadlineExceeded, Evicted, plus a reason-less one), 0 for every reason
    # but the real one, and the first result in the list is not
    # necessarily the one that is 1.
    failed_v="$(query_value "max(kube_job_status_failed{job_name=\"${job_name}\"})")"
    if [ -n "$failed_v" ] && num_ge "$failed_v" 1; then
        fail "e2e job ${job_name} reports kube_job_status_failed=${failed_v} -- the promoted version's own e2e suite failed (read its Pod logs: kubectl -n ${WORKLOAD_NAMESPACE} logs job/${job_name}); that Job is immutable and stays failed for this version, so every verification re-run of ${CHART_VERSION} ends here within seconds until a new version replaces it"
    fi
}

# to_int rounds a PromQL value to the nearest integer: increase() over a
# window extrapolates, so 9.98 journeys means 10.
to_int() { awk -v a="${1:-0}" 'BEGIN{printf "%d", a + 0.5}'; }

# next_poll sleeps one poll interval, but never past a deadline ($1), and
# enforces the test-only iteration bound.
next_poll() {
    if [ "$MAX_ITERATIONS" -gt 0 ] && [ "$iterations" -ge "$MAX_ITERATIONS" ]; then
        fail "gate did not finish within MAX_ITERATIONS=${MAX_ITERATIONS} (test bound, not the production budget)"
    fi
    now="$(date +%s)"
    sleep_for="$POLL_SECONDS"
    left=$(($1 - now))
    [ "$left" -lt "$sleep_for" ] && sleep_for="$left"
    [ "$sleep_for" -gt 0 ] && sleep "$sleep_for"
    return 0
}

log "metrics gate: ${PROJECT_NAME} ${CHART_VERSION}: phase 1 (wait for the roll, <=${PHASE1_SECONDS}s) then phase 2 (bake until ${MIN_JOURNEYS} journeys, <=${BAKE_SECONDS}s); e2e job ${job_name}"

ensure_token

# ---------------------------------------------------------------------
# PHASE 1 -- wait until the NEW version is fully serving.
#
# The promotion starts this Job, but ArgoCD syncs minutes later and the
# roll takes minutes more; probes that fail while old pods terminate and
# new ones start say nothing about the new version. Nothing is judged
# here except the e2e Job's own failure. Ready = at least one live pod
# at CHART_VERSION, every live CHART_VERSION pod Ready, and NO live pod
# at any other version.
# ---------------------------------------------------------------------
iterations=0
p1_start="$(date +%s)"
p1_deadline=$((p1_start + PHASE1_SECONDS))

while :; do
    iterations=$((iterations + 1))
    ensure_token
    check_e2e_failed

    # The ready point is the moment the QUERY was asked, not when the
    # answer was read: the state it reports is at least that old.
    t_q="$(date +%s)"
    new_total="$(query_value "$q_new_total")"
    new_ready="$(query_value "$q_new_ready")"
    old_pods="$(query_value "$q_old")"
    new_total="${new_total:-0}"
    new_ready="${new_ready:-0}"
    old_pods="${old_pods:-0}"

    if num_ge "$new_total" 1 && num_ge "$new_ready" "$new_total" && ! num_gt "$old_pods" 0; then
        ready_ts="$t_q"
        log "phase 1 done after $((ready_ts - p1_start))s: ${new_ready}/${new_total} pods at ${CHART_VERSION} ready, ${old_pods} old-version pods left"
        break
    fi

    now="$(date +%s)"
    if [ "$now" -ge "$p1_deadline" ]; then
        hint=""
        if [ "$new_total" = "0" ]; then
            discover "kube_pod_labels"
            hint=" (no live pod labelled app.kubernetes.io/version=${CHART_VERSION}: not rolled out yet, or kube-state-metrics does not export the label -- see metricLabelsAllowlist)"
        fi
        fail "phase 1 (waiting for the roll): after ${PHASE1_SECONDS}s ${new_ready}/${new_total} pods at ${CHART_VERSION} ready, ${old_pods} live pods at another version${hint}"
    fi

    next_poll "$p1_deadline"
done

# ---------------------------------------------------------------------
# PHASE 2 -- bake on SAMPLES, counted strictly after the ready point.
#
# since = ready point + SCRAPE_LAG_SECONDS: a scrape interval of margin,
# so a journey that ran before the roll finished but whose sample
# arrives late is not counted. Every increase() below is over
# [now - since], so nothing before `since` is ever in scope; VictoriaMetrics'
# increase() also handles counter resets (a prober pod restart).
# Fail early on a breach (ratio over >= MIN_JOURNEYS samples, a restart,
# a firing alert); pass once MIN_JOURNEYS samples are in and clean and the
# e2e Job has succeeded.
# ---------------------------------------------------------------------
since=$((ready_ts + SCRAPE_LAG_SECONDS))
p2_deadline=$((ready_ts + BAKE_SECONDS))
total_i=0
failure_i=0
ratio=0
succeeded_v=""
have_data=""

while :; do
    iterations=$((iterations + 1))
    ensure_token
    check_e2e_failed

    now="$(date +%s)"
    if [ "$now" -gt "$since" ]; then
        w=$((now - since))

        restarts_v="$(query_value "sum(increase(kube_pod_container_status_restarts_total[${w}s]))")"
        if [ -n "$restarts_v" ] && num_gt "$restarts_v" 0; then
            fail "phase 2 (bake): container restarts increased ${restarts_v} in the ${w}s since the roll finished"
        fi

        # Firing alerts, judged from the ready point on and never in
        # phase 1 (an old version's leftovers or the roll's own alerts
        # are not this version's fault). See the job_name filter: an
        # alert for ANOTHER version's e2e Job never counts.
        if [ "$CHECK_FIRING_ALERTS" = "true" ]; then
            alerts_query="ALERTS{alertstate=\"firing\", job_name!~\"${PROJECT_NAME}-e2e-.*\"} or ALERTS{alertstate=\"firing\", job_name=\"${job_name}\"}"
            alerts_v="$(query_value "count(${alerts_query})")"
            if [ -n "$alerts_v" ] && num_gt "$alerts_v" 0; then
                log "firing alert(s) after the roll finished:"
                query "${alerts_query}" | "$JQ" -r '.data.result[] | "  alertname=" + (.metric.alertname // "unknown") + (if .metric.job_name then " job_name=" + .metric.job_name elif .metric.pod then " pod=" + .metric.pod else "" end)' >&2
                fail "phase 2 (bake): ${alerts_v} alert(s) firing ${w}s after the roll finished"
            fi
        fi

        total_v="$(query_value "sum(increase(${PROBER_JOURNEY_METRIC}[${w}s]))")"
        if [ -n "$total_v" ]; then
            have_data=1
            total_i="$(to_int "$total_v")"
            failure_v="$(query_value "sum(increase(${PROBER_JOURNEY_METRIC}{result=\"failure\"}[${w}s]))")"
            failure_i="$(to_int "${failure_v:-0}")"
            ratio="$(awk -v f="$failure_i" -v t="$total_i" 'BEGIN{ if (t == 0) { print 0 } else { printf "%.4f", f/t } }')"

            if [ "$total_i" -ge "$MIN_JOURNEYS" ]; then
                if num_gt "$ratio" "$MAX_FAILURE_RATIO"; then
                    fail "phase 2 (bake): failure ratio ${ratio} exceeds ${MAX_FAILURE_RATIO} (${failure_i}/${total_i} journeys in the ${w}s since the roll finished)"
                fi
                succeeded_v="$(query_value "kube_job_status_succeeded{job_name=\"${job_name}\"}")"
                if [ "$E2E_JOB_REQUIRED" != "true" ] || { [ -n "$succeeded_v" ] && num_ge "$succeeded_v" 1; }; then
                    log "metrics gate PASSED for ${job_name}: ${total_i} journeys after the roll (${failure_i} failed, ratio ${ratio}), ${w}s baked"
                    exit 0
                fi
            fi
        fi
    fi

    now="$(date +%s)"
    if [ "$now" -ge "$p2_deadline" ]; then
        if [ -z "$have_data" ]; then
            discover "probe.*|probe_.*"
            fail "phase 2 (bake): no data for ${PROBER_JOURNEY_METRIC} in ${BAKE_SECONDS}s after the roll finished (NO DATA = FAIL)"
        fi
        if [ "$total_i" -lt "$MIN_JOURNEYS" ]; then
            fail "phase 2 (bake): only ${total_i} journeys (${failure_i} failed) in ${BAKE_SECONDS}s after the roll finished, want >= ${MIN_JOURNEYS}"
        fi
        discover "kube_job_status.*"
        fail "phase 2 (bake): ${total_i} journeys clean (ratio ${ratio}) but e2e job ${job_name} did not report kube_job_status_succeeded=1 within ${BAKE_SECONDS}s (got '${succeeded_v}')"
    fi

    next_poll "$p2_deadline"
done
