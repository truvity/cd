#!/usr/bin/env bash
# The zero-diff gate. For every case under tests/cases/<chart>/<case>/:
#
#   1. render the WRAPPER chart with the case's values;
#   2. flatten the same values to the UPSTREAM chart's own shape (the
#      subchart key's contents, with the root `global` merged over the
#      nested one, which is Helm's precedence), and render the vendored
#      upstream archive alone, under the same release name and namespace;
#   3. require the two renders to be the same objects.
#
# That is the claim an adopter relies on: moving an installation from the
# upstream chart to this one, with its values nested one level, changes
# nothing the cluster sees. It holds because this repository's charts
# default nothing of their own and render their own objects (the opt-in
# extras) only when asked: the day a default moves one of the upstream's
# objects, this script fails, and the change is a Behaviour change declared
# in CHANGELOG.md rather than a surprise.
#
# "The same objects" is the render with its `# Source:` comment lines
# removed. Those name the template's path (`argo-cd/templates/...` against
# `argocd/charts/argo-cd/templates/...`) and Helm emits them as comments,
# so no cluster, no ArgoCD comparison and no `kubectl diff` ever sees them.
# Everything else is compared byte for byte.
#
# An estate proves its own adoption the same way, with ITS values, before
# it merges the change that moves the source: docs/adoption.md.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
fail=0
n=0

# Split Helm's output into documents, drop the ones whose `# Source:` line
# starts with $1 (none, when $1 is empty), drop every `# Source:` comment,
# and drop blank lines at the end of a document (a template that begins with
# a comment leaves one; no cluster sees it).
normalize() {
  awk -v own="$1" '
    function flush() { if (doc != "" && !skip) { sub(/\n+$/, "\n", doc); printf "%s", doc } }
    /^---$/ { flush(); doc = $0 "\n"; skip = 0; next }
    own != "" && index($0, own) == 1 { skip = 1 }
    /^# Source:/ { next }
    { doc = doc $0 "\n" }
    END { flush() }'
}

# chart -> the key the upstream chart's values live under
declare -A key=([cd-argocd]=argo-cd [cd-kargo]=kargo [cd-rollouts]=argo-rollouts)

for values in "$root"/tests/cases/*/*/values.yaml; do
  case_dir="$(dirname "$values")"
  case_name="$(basename "$case_dir")"
  chart="$(basename "$(dirname "$case_dir")")"
  k="${key[$chart]}"
  ns="$(cat "$case_dir/namespace" 2>/dev/null || echo default)"
  upstream="$(ls "$root"/charts/"$chart"/charts/"$k"-*.tgz)"
  tmp="$(mktemp -d)"

  # shellcheck disable=SC2016
  yq eval "(.[\"$k\"] // {}) * {\"global\": ((.[\"$k\"].global // {}) * (.global // {}))}" \
    "$values" > "$tmp/flat.yaml"

  # The wrapper's own templates (charts/<chart>/templates/: the opt-in
  # extras) are objects the upstream chart does not render at all, so they
  # are held out of this comparison and pinned by the goldens instead. What
  # is compared is every object the UPSTREAM chart contributes: the extras
  # being switched on must not move one of them.
  helm template "$chart" "$root/charts/$chart" --namespace "$ns" -f "$values" \
    | normalize "# Source: $chart/templates/" > "$tmp/wrapped.yaml"
  helm template "$chart" "$upstream" --namespace "$ns" -f "$tmp/flat.yaml" \
    | normalize "" > "$tmp/upstream.yaml"

  n=$((n + 1))
  if ! diff -u "$tmp/upstream.yaml" "$tmp/wrapped.yaml" > "$tmp/diff"; then
    echo "PARITY BROKEN: $chart/$case_name — the wrapper renders something the upstream chart does not:" >&2
    head -40 "$tmp/diff" >&2
    fail=1
  fi
  rm -rf "$tmp"
done

[ "$fail" = 0 ] && echo "parity: $n cases render the same objects through the wrapper as through the upstream chart"
exit $fail
