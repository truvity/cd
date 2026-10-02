# Reference

## Charts

| Chart | Upstream | Pin | Application version |
| --- | --- | --- | --- |
| `cd-argocd` | `argo-cd` from `https://argoproj.github.io/argo-helm` | 9.7.0 | v3.4.4 |
| `cd-kargo` | `kargo` from `oci://ghcr.io/akuity/kargo-charts` | 1.11.6 | v1.11.6 |

The pins are exact, the archives are vendored under `charts/<chart>/charts/`,
and the current values are in each `Chart.yaml`. This chart's own version is
the repository tag; it says nothing about the upstream's.

## Values

Both charts take two top-level keys and nothing else.

| Key | Meaning |
| --- | --- |
| `argo-cd` (`cd-argocd`) / `kargo` (`cd-kargo`) | The upstream chart's own values, verbatim. Documented by the upstream. |
| `global` | Shared with the upstream chart as its own `global`. |

There are no defaults of our own: `values.yaml` sets both keys empty. The
upstream's defaults are the defaults.

## Notes on the upstream charts

- **Kargo requires either an admin account or OIDC when its API is
  enabled.** `kargo.api.adminAccount.passwordHash` and `tokenSigningKey`, or
  `kargo.api.adminAccount.enabled: false` with `kargo.api.oidc` set. Without
  either, the upstream chart refuses to render. The controller alone
  (`kargo.api.enabled: false`) needs neither.
- **Argo CD reads `$<secret>:<key>` in `configs.cm`** only from a Secret
  labelled `app.kubernetes.io/part-of: argocd`. Unlabelled, the literal
  string is sent to the identity provider.
- **Both charts render their CRDs.** The goldens carry each as a name and a
  digest (`hack/golden-normalize.py`), so a changed CRD moves the golden and
  the reviewer sees which one.

## What the tests compare

| Check | Recipe | What it fails on |
| --- | --- | --- |
| Schema and refusals | `just lint` | an unknown key that renders; a fixture that does not fail for its declared reason |
| Goldens | `just test` | any change to a render, CRDs by digest |
| Parity | `just test` | a wrapper render that differs from the upstream chart's render for the same values |
| Leak canary | `just leak-canary` | an account id, internal hostname, registry host or token in a tracked file |
