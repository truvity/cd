# Changelog

Prose bullets, written for the consumer: what changes in the render, what
must be done first, and whether a default moved. Newest first, one
`## vX.Y.Z` heading per tag. A chart's version is the tag; the upstream
release it wraps is named in each entry.

## v0.1.1

- **Fixed:** the release workflow could not run. `devbox.json` did not name
  goreleaser, so `devbox run -- goreleaser release` failed with "command not
  found" and the `v0.1.0` tag published nothing. goreleaser 2.17.1 is now in
  the toolchain; the charts' content and renders are unchanged.

## v0.1.0

First release: the `cd-argocd` and `cd-kargo` charts.

- `cd-argocd` wraps the upstream `argo-cd` chart 9.7.0 (Argo CD v3.4.4) and
  `cd-kargo` wraps the upstream `kargo` chart 1.11.6 (Kargo v1.11.6). Neither
  sets a value or templates an object of its own: with the upstream chart's
  values nested one level under `argo-cd` or `kargo`, the render is the
  upstream chart's, object for object. `hack/parity.sh` proves it for every
  case under `tests/cases/`, and `docs/adoption.md` shows how to prove it
  with your own values before moving an installation onto it.
- `values.schema.json` on both charts refuses an unknown top-level key, and
  in particular upstream values pasted at the root of the file, which would
  otherwise be ignored and install the defaults.
