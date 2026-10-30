# Development commands. Everything CI runs is a recipe here: the shared
# check workflow (truvity/ci-workflows) runs each one as its own job, so a
# laptop and CI run the same thing.

chart_names := "cd-argocd cd-kargo cd-rollouts cd-delivery cd-company cd-pipeline cd-cluster-registration cd-webhook-relay"

# Lint every chart, and prove every refusal still refuses.
lint:
    #!/usr/bin/env bash
    set -euo pipefail
    python3 hack/company-schema.py --check
    for chart in {{ chart_names }}; do
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
    # The Go packages (chartgate and what follows) are linted with the charts.
    GOTOOLCHAIN=local golangci-lint run ./...

# Golden renders, then the zero-diff gate.
#
# One recipe, because they answer the same question from two sides: the
# goldens show a reviewer what a change does to the render, and the parity
# gate proves the wrapper adds nothing to the upstream chart's own.
test:
    go test ./...
    hack/golden.sh
    hack/parity.sh
    hack/health.sh
    hack/e2e-operation.sh

# Render every chart fixture, and check the adapter's composition contracts.
charts:
    python3 hack/company-schema.py --check
    go test ./tests/proof/
    hack/golden.sh

# The Lua health checks of presets/health.yaml, each run against its fixtures.
health:
    hack/health.sh

# Regenerate the golden renders. Review the diff before committing.
golden:
    hack/golden.sh update

# Re-vendor a chart's declared dependencies into its charts/ directory.
# cd-company packages the sibling cd-delivery source; upstream wrappers
# first move the dependency pin in Chart.yaml. Commit the archive so a
# render needs no network. Then `just golden` and review the diff; for an
# upstream wrapper also move appVersion to the upstream's.
vendor chart:
    helm dependency update charts/{{ chart }}

# The reason this repository can be public. Runs in CI as its own job.
leak-canary:
    hack/leak-canary.sh

# Reachable Go advisories. Not part of `check`: a standard-library advisory with
# no released fix must not wedge every pull request (the Security workflow runs it).
vuln:
    govulncheck ./...

# Everything CI runs on a pull request.
check: lint test leak-canary
