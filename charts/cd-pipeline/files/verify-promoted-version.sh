#!/bin/sh
#
# The promoted-version check of a Kargo Stage: the question a Promotion cannot
# ask for itself. Kargo's argocd-wait step compares an Application's STATE and
# has no revision field, and a Stage's own health never consults Argo CD, so
# after the pin is pushed nothing asks whether the target Application renders
# the promoted chart at all. A parent Application that is still mid-sync leaves
# the child on the previous version, Synced and Healthy.
#
# Run as a Kargo verification, i.e. after the Promotion succeeded and before the
# Freight counts as verified in this Stage, it holds until every target
# Application (a) has the promoted chart version in its spec, (b) has been
# COMPARED at that version (status.sync.comparedTo), and (c) is Synced, Healthy
# and not mid-operation.
#
# Reads the Applications with the pod's own ServiceAccount through the
# in-cluster API (curl and jq); the Role the chart renders allows `get` on
# exactly the Applications the Stage names.
#
# Env: APPS (space separated Application names), CHART_REPO, CHART_NAME
# (empty for OCI repos), EXPECTED (the targetRevision, prefix included),
# ARGOCD_NAMESPACE (default argocd), WAIT_SECONDS, POLL_SECONDS, JQ.
set -u

JQ="${JQ:-jq}"
API="https://kubernetes.default.svc"
SA=/var/run/secrets/kubernetes.io/serviceaccount
started=$(date +%s)
last=""

# One line per Application: "<verdict> <message>".
# verdict: skip (renders none of this chart), ok, wait.
judge() {
	"$JQ" -r --arg repo "$CHART_REPO" --arg chart "$CHART_NAME" --arg exp "$EXPECTED" '
	  # Identity of a chart source: repo without scheme or trailing slash,
	  # plus /chart where the source names one. Infra Applications say
	  # repoURL registry/charts + chart X, others the whole
	  # oci://.../X path, and Kargo the latter for both.
	  def ident($r; $c): ($r | sub("^oci://"; "") | sub("/+$"; "")) + (if ($c // "") != "" then "/" + $c else "" end);
	  def mine: map(select(. != null and ident(.repoURL; .chart) == ident($repo; $chart)));
	  def revs: map(.targetRevision // "");
	  ((.spec.sources // [.spec.source]) | mine) as $spec
	  | ((.status.sync.comparedTo.sources // [.status.sync.comparedTo.source]) | mine) as $cmp
	  | if ($spec | length) == 0 then "skip renders no source from \($repo)"
	    elif ($spec | revs | all(. == $exp) | not) then
	      "wait spec still targets \($spec | revs | join(",")), want \($exp) (parent app-of-apps not synced?)"
	    elif (($cmp | length) == 0) or ($cmp | revs | all(. == $exp) | not) then
	      "wait spec is at \($exp) but last comparison was at \($cmp | revs | join(",") | if . == "" then "nothing" else . end)"
	    elif (.status.operationState.phase // "") == "Running" then "wait a sync operation is still Running"
	    elif (.status.sync.status // "") != "Synced" then "wait sync status \(.status.sync.status // "unknown")"
	    elif (.status.health.status // "") != "Healthy" then "wait health \(.status.health.status // "unknown")"
	    else "ok at \($exp)" end'
}

while :; do
	pending=""
	matched=0
	report=""
	for app in $APPS; do
		body=$(curl -sS --max-time 20 --cacert "$SA/ca.crt" \
			-H "Authorization: Bearer $(cat "$SA/token")" \
			-w '\n%{http_code}' \
			"$API/apis/argoproj.io/v1alpha1/namespaces/${ARGOCD_NAMESPACE:-argocd}/applications/$app" 2>&1)
		code=$(printf '%s' "$body" | tail -n 1)
		body=$(printf '%s' "$body" | sed '$d')
		if [ "$code" != "200" ]; then
			line="wait cannot read Application (HTTP $code)"
		else
			line=$(printf '%s' "$body" | judge)
		fi
		verdict=${line%% *}
		msg=${line#* }
		report="$report
  $app: $msg"
		case "$verdict" in
		skip) ;;
		ok) matched=$((matched + 1)) ;;
		*) matched=$((matched + 1)); pending="$pending $app" ;;
		esac
	done

	if [ -z "$pending" ] && [ "$matched" -gt 0 ]; then
		echo "promoted version $EXPECTED is rendered and Synced/Healthy:$report"
		exit 0
	fi

	if [ "$matched" -eq 0 ]; then
		echo "FAIL: none of the Applications ($APPS) renders a source from $CHART_REPO -- check the Stage's applications:$report"
		exit 1
	fi

	now=$(date +%s)
	if [ $((now - started)) -ge "$WAIT_SECONDS" ]; then
		echo "FAIL: after ${WAIT_SECONDS}s the promoted version $EXPECTED is not live. Not ready:$report"
		echo "If an Application still targets the previous version, the parent that renders its pin has not synced: look for a sync operation still Running on it."
		exit 1
	fi

	if [ "$report" != "$last" ]; then
		echo "waiting ($((now - started))s of ${WAIT_SECONDS}s):$report"
		last=$report
	fi
	sleep "$POLL_SECONDS"
done
