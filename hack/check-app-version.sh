#!/usr/bin/env bash
# Each chart's appVersion must equal the appVersion of the upstream archive
# it vendors. The release workflow leaves appVersion alone for a chart that
# packages software we do not build, so this is the only thing keeping it
# honest when renovate moves the dependency.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
fail=0

for chart_dir in "$root"/charts/*/; do
  chart="$(basename "$chart_dir")"
  want="$(yq '.appVersion' "$chart_dir/Chart.yaml")"
  archive="$(ls "$chart_dir"charts/*.tgz)"
  # The archive's own top-level Chart.yaml, not a subchart's (argo-cd
  # vendors redis-ha, which has an appVersion of its own).
  top="$(tar -tzf "$archive" | sed -n 1p | cut -d/ -f1)"
  have="$(tar -xzOf "$archive" "$top/Chart.yaml" | yq '.appVersion')"
  if [ "$want" != "$have" ]; then
    echo "$chart: appVersion is $want but the vendored upstream says $have" >&2
    fail=1
  fi
done

[ "$fail" = 0 ] && echo "appVersion matches the vendored upstream for every chart"
exit $fail
