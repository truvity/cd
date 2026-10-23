#!/usr/bin/env bash
# Golden renders: every tests/cases/<chart>/<case>/values.yaml is rendered
# with `helm template` and compared byte-for-byte against
# tests/golden/<chart>/<case>.yaml, with CRDs reduced to a name and digest
# (hack/golden-normalize.py). A template change that alters output
# therefore shows up as a reviewable diff, with no cluster involved.
#
#   hack/golden.sh          compare (CI)
#   hack/golden.sh update   regenerate the golden files
set -euo pipefail

mode="${1:-check}"
root="$(cd "$(dirname "$0")/.." && pwd)"
fail=0

for values in "$root"/tests/cases/*/*/values.yaml; do
  case_dir="$(dirname "$values")"
  case_name="$(basename "$case_dir")"
  chart="$(basename "$(dirname "$case_dir")")"
  golden="$root/tests/golden/$chart/$case_name.yaml"

  # A case may name presets (tests/cases/<chart>/<case>/presets, one per
  # line): charts/<chart>/presets/<name>.yaml, passed BEFORE the case's own
  # values the way an adopter layers them.
  preset_args=()
  if [ -f "$case_dir/presets" ]; then
    while read -r preset; do
      [ -n "$preset" ] && preset_args+=(-f "$root/charts/$chart/presets/$preset.yaml")
    done < "$case_dir/presets"
  fi

  # A library chart renders nothing, so a case of one renders the consumer
  # chart tests/consumers/<chart>, with the library as its subchart.
  chart_dir="$root/charts/$chart"
  if [ -d "$root/tests/consumers/$chart" ]; then
    consumer="$(mktemp -d)"
    cp -r "$root/tests/consumers/$chart/." "$consumer/"
    mkdir -p "$consumer/charts"
    cp -r "$root/charts/$chart" "$consumer/charts/$chart"
    chart_dir="$consumer"
  fi

  rendered="$(helm template "$chart" "$chart_dir" \
      --namespace "$(cat "$case_dir/namespace" 2>/dev/null || echo default)" \
      "${preset_args[@]}" -f "$values" | "$root/hack/golden-normalize.py")"

  [ -d "$root/tests/consumers/$chart" ] && rm -rf "$chart_dir"

  if [ "$mode" = update ]; then
    mkdir -p "$(dirname "$golden")"
    printf '%s\n' "$rendered" > "$golden"
    echo "updated $golden"
    continue
  fi

  if ! diff -u "$golden" <(printf '%s\n' "$rendered"); then
    echo "GOLDEN MISMATCH: $chart/$case_name — run 'just golden' and review the diff"
    fail=1
  fi
done

[ "$fail" = 0 ] && echo "golden renders match"
exit $fail
