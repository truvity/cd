#!/usr/bin/env bash
# Unit tests for the Lua health checks in charts/cd-argocd/presets/health.yaml.
#
# Argo CD runs a check as Lua with the live object as `obj`. Every check is
# pulled out of the preset (never copied: a copy is a second place to be
# wrong) and run under Lua 5.1, the dialect Argo CD embeds, against the
# fixtures in tests/health/<group>_<Kind>/<case>.yaml. A fixture is a
# resource as the cluster would hold it, led by
#
#   # expect: Healthy|Progressing|Degraded
#   # message: <the exact message>      (optional)
#
# `STAR` in a directory name stands for `*`, which cannot be written in one.
#
# The gate is two-way: a check with no fixture directory fails, a fixture
# directory with no check fails, and every check needs a Healthy fixture
# and one that is not, so that a check that says Healthy for everything (or
# never says it) is caught.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
preset="$root/charts/cd-argocd/presets/health.yaml"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
fail=0
checks=0
cases=0

cm='.["argo-cd"].configs.cm'

# check id (a directory name) -> its Lua, one file per check under $tmp/lua.
mkdir -p "$tmp/lua"
declare -A have=()

while IFS= read -r key; do
  id="${key#resource.customizations.health.}"
  yq eval "$cm[\"$key\"]" "$preset" > "$tmp/lua/$id.lua"
  have[$id]=1
done < <(yq eval "$cm | keys | .[] | select(test(\"^resource\\.customizations\\.health\\.\"))" "$preset")

yq eval "$cm[\"resource.customizations\"]" "$preset" > "$tmp/combined.yaml"
while IFS= read -r key; do
  id="$(sed 's#/#_#; s#\*#STAR#g' <<<"$key")"
  KEY="$key" yq eval 'explode(.) | .[strenv(KEY)]["health.lua"]' "$tmp/combined.yaml" > "$tmp/lua/$id.lua"
  have[$id]=1
done < <(yq eval 'explode(.) | keys | .[]' "$tmp/combined.yaml")

for id in "${!have[@]}"; do
  checks=$((checks + 1))
  dir="$root/tests/health/$id"
  if [ ! -d "$dir" ] || ! ls "$dir"/*.yaml >/dev/null 2>&1; then
    echo "NO FIXTURES: the preset has a check for $id and tests/health/$id/ has none" >&2
    fail=1
    continue
  fi
  seen=""
  for fixture in "$dir"/*.yaml; do
    cases=$((cases + 1))
    name="$(basename "$fixture" .yaml)"
    expect="$(sed -n 's/^# expect: //p' "$fixture" | head -1)"
    if [ -z "$expect" ]; then
      echo "NO 'expect' DECLARATION: tests/health/$id/$name.yaml" >&2
      fail=1
      continue
    fi
    seen="$seen $expect"
    yq eval -o=json '.' "$fixture" > "$tmp/obj.json"
    if ! got="$(lua "$root/hack/health.lua" "$tmp/lua/$id.lua" "$tmp/obj.json" 2>&1)"; then
      echo "CHECK FAILED TO RUN: tests/health/$id/$name.yaml: $got" >&2
      fail=1
      continue
    fi
    status="${got%%$'\t'*}"
    message="${got#*$'\t'}"
    if [ "$status" != "$expect" ]; then
      echo "WRONG STATUS: tests/health/$id/$name.yaml: expected $expect, the check said $status ($message)" >&2
      fail=1
      continue
    fi
    if grep -q '^# message: ' "$fixture"; then
      want="$(sed -n 's/^# message: //p' "$fixture" | head -1)"
      if [ "$message" != "$want" ]; then
        echo "WRONG MESSAGE: tests/health/$id/$name.yaml: expected \"$want\", the check said \"$message\"" >&2
        fail=1
      fi
    fi
  done
  case " $seen " in
    *" Healthy "*) ;;
    *) echo "TOO FEW FIXTURES: tests/health/$id/ has no Healthy case" >&2; fail=1 ;;
  esac
  case "$seen" in
    *Progressing*|*Degraded*) ;;
    *) echo "TOO FEW FIXTURES: tests/health/$id/ has no case that is not Healthy" >&2; fail=1 ;;
  esac
done

for dir in "$root"/tests/health/*/; do
  id="$(basename "$dir")"
  if [ -z "${have[$id]:-}" ]; then
    echo "ORPHAN FIXTURES: tests/health/$id/ matches no check in the preset" >&2
    fail=1
  fi
done

[ "$fail" = 0 ] && echo "health: $checks checks, $cases fixtures, each check said what its fixture declares"
exit $fail
