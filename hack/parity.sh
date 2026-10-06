#!/usr/bin/env bash
# The zero-diff gate. For every case under tests/cases/<chart>/<case>/ of a
# chart that wraps an upstream chart, the wrapper's render and the upstream
# chart's render (with the same values flattened to its own shape) must be the
# same objects. The proof is the shared `parity.Wrapper` of this repository's
# parity package, run as a Go test (tests/proof); this script is its entry
# point for `just test` and CI. See parity/wrapper.go for the claim and
# docs/adoption.md for how an estate proves its own adoption the same way.
set -euo pipefail

cd "$(dirname "$0")/.."
exec go test -count=1 ./tests/proof/
