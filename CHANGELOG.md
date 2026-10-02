# Changelog

Prose bullets, written for the consumer: what changes in the render, what
must be done first, and whether a default moved. Newest first, one
`## vX.Y.Z` heading per tag. A chart's version is the tag; the upstream
release it wraps is named in each entry.

## Unreleased

## v0.2.0

- **Added (opt-in):** `cd-argocd` can render the Namespace, AppProjects,
  NetworkPolicies and ExternalSecrets that sit beside an Argo CD install.
  New top-level values `namespace`, `appProjects`, `networkPolicies`,
  `externalSecrets` and `secretStoreRef`; names, labels, annotations, peers,
  CIDRs and the secret store reference are all yours, with no defaults. With
  none of them set the render is byte-for-byte the previous release's (every
  existing golden is unchanged), so this is not a Behaviour change. See
  `docs/reference.md`.
- The parity gate now holds the chart's own templates out of the comparison
  and compares every object the upstream chart contributes, so switching an
  extra on is proven not to move one of them.
- **Added:** the `cd-rollouts` chart, Argo Rollouts from the upstream
  `argo-rollouts` chart 2.43.2 (Argo Rollouts v1.10.0), vendored and
  exact-pinned. Values go under `argo-rollouts`; no template or default of
  its own, and the parity gate covers it like the others. It is a separate
  chart, not a `cd-kargo` dependency, so its CRDs keep their own lifecycle,
  namespace and release name (`docs/reference.md#cd-rollouts`). Nothing
  installs it unless you do; no existing chart's render moves.

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
