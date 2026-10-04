#!/bin/sh
#
# The end-to-end check of a Kargo Stage whose promotion ran the product's
# end-to-end suite as an Argo CD sync hook. The Promotion's `argocd-update`
# step wrote a sync operation on the end-to-end Application and waited for it;
# this check asks the Application what that operation did, so a Freight is
# verified only when the suite ran for the version it promoted.
#
# It holds until the Application's last operation (a) has phase Succeeded,
# (b) synced the promoted revision, not the one a stale parent render still
# pinned, and (c) ran at least one Sync-hook Job, every one of them Succeeded.
# A Failed or Errored operation, or a hook that did not succeed, fails at once.
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

# One line: "<verdict> <message>". verdict: ok, wait, fail.
judge() {
	"$JQ" -r --arg exp "$EXPECTED" '
	  (.status.operationState // null) as $op
	  | (($op.syncResult // {}) | ([.revision] + (.revisions // [])) | map(select(. != null and . != ""))) as $revs
	  | [($op.syncResult.resources // [])[] | select(.hookType == "Sync" and .kind == "Job")] as $jobs
	  | if $op == null then "wait the Application has no operation yet"
	    elif ($op.phase == "Failed" or $op.phase == "Error") then
	      "fail the last operation \($op.phase): \($op.message // "no message")"
	    elif $op.phase != "Succeeded" then "wait the last operation is \($op.phase // "unknown")"
	    elif ($revs | length) == 0 or ($revs | all(. == $exp) | not) then
	      "wait the last operation synced \($revs | join(",") | if . == "" then "no revision" else . end), want \($exp)"
	    elif ($jobs | length) == 0 then "fail the operation ran no Sync-hook Job: is e2e.hook on?"
	    elif ($jobs | all(.hookPhase == "Succeeded") | not) then
	      "fail a hook Job did not succeed: \($jobs | map("\(.name)=\(.hookPhase // "unknown")") | join(", "))"
	    else "ok operation Succeeded at \($exp), hook Jobs: \($jobs | map(.name) | join(", "))" end'
}

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
