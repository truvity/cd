# Development commands. Everything CI runs is a recipe here: the shared
# check workflow (truvity/ci-workflows) runs each one as its own job, so a
# laptop and CI run the same thing.

charts := "cd-argocd cd-kargo cd-rollouts cd-delivery cd-pipeline"

# Lint every chart, and prove every refusal still refuses.
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    for chart in {{ charts }}; do
      # A chart renders with WHATEVER archive is in its charts/ directory:
      # move the version in Chart.yaml and leave the vendored archive
      # behind, and Helm renders the old upstream while every check passes
      # and the golden does not move. `helm dependency list` exits 0 either
      # way, so the STATUS column is what is read.
      if helm dependency list "charts/$chart" \
           | tail -n +2 | grep -v '^[[:space:]]*$' | grep -qv 'ok[[:space:]]*$'; then
        helm dependency list "charts/$chart" >&2
        echo "$chart: a declared dependency is missing or is the wrong version — run 'just vendor $chart'" >&2
        exit 1
      fi
      helm lint "charts/$chart" --values tests/cases/"$chart"/minimal/values.yaml
      # An unknown top-level key must fail the render. Not `! helm
      # template ...`: bash's `set -e` ignores a command negated with `!`.
      if helm template x "charts/$chart" \
           --values tests/cases/"$chart"/minimal/values.yaml \
           --set bogusKey=1 >/dev/null 2>&1; then
        echo "$chart: an unknown key rendered" >&2
        exit 1
      fi
      echo "$chart: schema OK"
    done
    # appVersion records the upstream release the vendored archive carries.
    hack/check-app-version.sh
    # Every negative fixture must fail, and for the refusal it declares.
    hack/lint-fixtures.sh

# Golden renders, then the zero-diff gate.
#
# One recipe, because they answer the same question from two sides: the
# goldens show a reviewer what a change does to the render, and the parity
# gate proves the wrapper adds nothing to the upstream chart's own.
test:
    hack/golden.sh
    hack/parity.sh
    hack/health.sh
    hack/e2e-operation.sh

# The Lua health checks of presets/health.yaml, each run against its fixtures.
health:
    hack/health.sh

# Regenerate the golden renders. Review the diff before committing.
golden:
    hack/golden.sh update

# Re-vendor one chart's pinned upstream into its charts/ directory, after
# moving the version in its Chart.yaml. The archive is committed on
# purpose: a render that needs the network is a render that differs
# depending on when it runs. Then `just golden` and read the diff, and
# move appVersion to the upstream's.
vendor chart:
    helm dependency update charts/{{ chart }}

# The reason this repository can be public. Runs in CI as its own job.
leak-canary:
    hack/leak-canary.sh

# Everything CI runs on a pull request.
check: lint test leak-canary
