#!/usr/bin/env bash
# Unit tests for the judge of charts/cd-pipeline/files/verify-e2e-operation.sh.
#
# The script is run with JUDGE_ONLY=1 against Application JSON as an OCI chart
# produces it: revisions are digests, the version only appears in
# spec.sources[].targetRevision and in the Job's name. Both modes of the suite's
# Job are covered: a Sync hook (syncResult.resources) and a tracked Job
# (status.resources).
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
script="$root/charts/cd-pipeline/files/verify-e2e-operation.sh"
d1=sha256:aaaa
d2=sha256:bbbb
fail=0
cases=0

# app <pin> <op-phase> <synced-digest> <resolved-digest> <hook-phase|-> <tracked-health|->
app() {
  jq -n --arg pin "$1" --arg phase "$2" --arg synced "$3" --arg resolved "$4" --arg hook "$5" --arg tracked "$6" '
    {spec: {sources: [{repoURL: "oci://x/url-shortener-e2e", targetRevision: $pin}]},
     status: {sync: {revisions: [$resolved]},
              resources: (if $tracked == "-" then [] else [{kind: "Job", name: "url-shortener-e2e-1-43-0", health: {status: $tracked}}] end),
              operationState: {phase: $phase, message: "m",
                syncResult: {revisions: [$synced],
                  resources: ([{kind: "Job", name: "other", hookType: "Sync", hookPhase: "Failed"}][0:0]
                    + (if $hook == "-" then [] else [{kind: "Job", name: "url-shortener-e2e-1-43-0", hookType: "Sync", hookPhase: $hook}] end))}}}}'
}

expect() { [ -n "${V:-}" ] && printf "%s -> " "$2" >&2;
  local want="$1" name="$2" json="$3" got
  cases=$((cases + 1))
  got="$(printf '%s' "$json" | JUDGE_ONLY=1 EXPECTED=1.43.0 JQ=jq sh "$script")"
  [ -n "${V:-}" ] && echo "$got" >&2
  if [ "${got%% *}" != "$want" ]; then
    echo "FAIL $name: want $want, got: $got" >&2
    fail=1
  fi
}

expect ok   "hook succeeded"                 "$(app 1.43.0 Succeeded $d1 $d1 Succeeded -)"
expect ok   "tracked Job healthy (pre-hook)" "$(app 1.43.0 Succeeded $d1 $d1 - Healthy)"
expect wait "hook still running"             "$(app 1.43.0 Succeeded $d1 $d1 Running -)"
expect fail "hook failed"                    "$(app 1.43.0 Succeeded $d1 $d1 Failed -)"
expect wait "tracked Job progressing"        "$(app 1.43.0 Succeeded $d1 $d1 - Progressing)"
expect fail "tracked Job degraded"           "$(app 1.43.0 Succeeded $d1 $d1 - Degraded)"
expect wait "spec still pins the old version" "$(app 1.42.1 Succeeded $d1 $d1 Succeeded -)"
expect wait "op synced an older digest"      "$(app 1.43.0 Succeeded $d1 $d2 Succeeded -)"
expect wait "op running"                     "$(app 1.43.0 Running $d1 $d1 Running -)"
expect fail "op failed"                      "$(app 1.43.0 Failed $d1 $d1 Failed -)"
expect wait "no Job at all"                  "$(app 1.43.0 Succeeded $d1 $d1 - -)"
expect wait "no operation"                   "$(app 1.43.0 Succeeded $d1 $d1 Succeeded - | jq 'del(.status.operationState)')"
expect ok   "single-source form"             "$(app 1.43.0 Succeeded $d1 $d1 Succeeded - | jq '.spec.source = .spec.sources[0] | del(.spec.sources)')"

echo "verify-e2e-operation: $cases cases"
exit "$fail"
