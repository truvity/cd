#!/bin/sh
#
# The end-to-end check of a Kargo Stage whose promotion ran the product's
# end-to-end suite through the `argocd-update` step on the end-to-end
# Application. The step wrote a sync operation on the Application and waited
# for it; this check asks the Application what that operation did, so a Freight
# is verified only when the suite ran for the version it promoted.
#
# It holds until (a) the Application's spec pins the promoted version (its
# targetRevision is EXPECTED, so a stale parent render is waited out), (b) the
# last operation has phase Succeeded, (c) that operation synced the revision the
# spec currently resolves to (syncResult revisions equal status.sync revisions;
# for an OCI chart both are digests, never the version string), and (d) the
# suite's Job for EXPECTED succeeded: either a Sync-hook Job of the operation
# with hookPhase Succeeded, or, while the Job is still a tracked resource (no
# hook), a Job of that version Healthy in the Application's resources. The Job
# is named `<project>-e2e-<version with dashes>`. A Failed or Errored
# operation, or a Job that did not succeed, fails at once.
#
# Not checked: that the operation finished after the Promotion started. The
# check does not know the Promotion; (a) and (c) tie the operation to the
# promoted revision instead.
#
# Reads the Application with the pod's own ServiceAccount through the
# in-cluster API (curl and jq); the Role the chart renders allows `get` on
# exactly that Application.
#
# Env: APP (the end-to-end Application name), EXPECTED (the targetRevision,
# prefix included), ARGOCD_NAMESPACE (default argocd), WAIT_SECONDS,
# POLL_SECONDS, JQ.
set -u

JQ="${JQ:-jq}"
API="https://kubernetes.default.svc"
SA=/var/run/secrets/kubernetes.io/serviceaccount
started=$(date +%s)
last=""

# One line: "<verdict> <message>". verdict: ok, wait, fail. Reads the
# Application on stdin.
judge() {
	"$JQ" -r --arg exp "$EXPECTED" '
	  def revs: ([.revision] + (.revisions // [])) | map(select(. != null and . != ""));
	  (.status.operationState // null) as $op
	  | ($exp | sub("^[^0-9]*"; "") | gsub("\\."; "-")) as $dashed
	  | ((.spec.sources // [.spec.source // {}]) | map(.targetRevision // "")) as $pins
	  | (($op.syncResult // {}) | revs) as $synced
	  | ((.status.sync // {}) | revs) as $resolved
	  | [($op.syncResult.resources // [])[]
	      | select(.hookType == "Sync" and .kind == "Job" and (.name | endswith("-e2e-" + $dashed)))] as $hooks
	  | [(.status.resources // [])[]
	      | select(.kind == "Job" and (.name | endswith("-e2e-" + $dashed)))] as $tracked
	  | if ($pins | index($exp)) == null then
	      "wait the Application pins \($pins | join(",") | if . == "" then "no revision" else . end), want \($exp)"
	    elif $op == null then "wait the Application has no operation yet"
	    elif ($op.phase == "Failed" or $op.phase == "Error") then
	      "fail the last operation \($op.phase): \($op.message // "no message")"
	    elif $op.phase != "Succeeded" then "wait the last operation is \($op.phase // "unknown")"
	    elif ($synced | length) == 0 or (($synced | sort) != ($resolved | sort)) then
	      "wait the last operation synced \($synced | join(",") | if . == "" then "no revision" else . end), the Application resolves \($resolved | join(",") | if . == "" then "no revision" else . end)"
	    elif ($hooks | length) > 0 then
	      if ($hooks | all(.hookPhase == "Succeeded")) then
	        "ok operation Succeeded at \($exp), hook Jobs: \($hooks | map(.name) | join(", "))"
	      elif ($hooks | any(.hookPhase == "Failed" or .hookPhase == "Error")) then
	        "fail a hook Job did not succeed: \($hooks | map("\(.name)=\(.hookPhase // "unknown")") | join(", "))"
	      else "wait a hook Job is still running: \($hooks | map("\(.name)=\(.hookPhase // "unknown")") | join(", "))" end
	    elif ($tracked | length) > 0 then
	      if ($tracked | all(.health.status == "Healthy")) then
	        "ok operation Succeeded at \($exp), tracked Job: \($tracked | map(.name) | join(", "))"
	      elif ($tracked | any(.health.status == "Degraded")) then
	        "fail the tracked Job did not succeed: \($tracked | map("\(.name)=\(.health.status // "unknown")") | join(", "))"
	      else "wait the tracked Job is not complete: \($tracked | map("\(.name)=\(.health.status // "unknown")") | join(", "))" end
	    else "wait the Application has no e2e Job for \($dashed) yet" end'
}

# Test hook: judge the Application JSON on stdin and exit.
if [ -n "${JUDGE_ONLY:-}" ]; then
	judge
	exit 0
fi

while :; do
	body=$(curl -sS --max-time 20 --cacert "$SA/ca.crt" \
		-H "Authorization: Bearer $(cat "$SA/token")" \
		-w '\n%{http_code}' \
		"$API/apis/argoproj.io/v1alpha1/namespaces/${ARGOCD_NAMESPACE:-argocd}/applications/$APP" 2>&1)
	code=$(printf '%s' "$body" | tail -n 1)
	body=$(printf '%s' "$body" | sed '$d')
	if [ "$code" != "200" ]; then
		line="wait cannot read Application (HTTP $code)"
	else
		line=$(printf '%s' "$body" | judge)
	fi
	verdict=${line%% *}
	msg=${line#* }

	case "$verdict" in
	ok)
		echo "$APP: $msg"
		exit 0
		;;
	fail)
		echo "FAIL: $APP: $msg"
		exit 1
		;;
	esac

	now=$(date +%s)
	if [ $((now - started)) -ge "$WAIT_SECONDS" ]; then
		echo "FAIL: after ${WAIT_SECONDS}s the end-to-end run for $EXPECTED is not done: $msg"
		exit 1
	fi
	if [ "$msg" != "$last" ]; then
		echo "waiting ($((now - started))s of ${WAIT_SECONDS}s): $APP: $msg"
		last=$msg
	fi
	sleep "$POLL_SECONDS"
done
