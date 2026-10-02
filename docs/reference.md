# Reference

## Charts

| Chart | Upstream | Pin | Application version |
| --- | --- | --- | --- |
| `cd-argocd` | `argo-cd` from `https://argoproj.github.io/argo-helm` | 9.7.0 | v3.4.4 |
| `cd-kargo` | `kargo` from `oci://ghcr.io/akuity/kargo-charts` | 1.11.6 | v1.11.6 |
| `cd-rollouts` | `argo-rollouts` from `oci://ghcr.io/argoproj/argo-helm` | 2.43.2 | v1.10.0 |

The pins are exact, the archives are vendored under `charts/<chart>/charts/`,
and the current values are in each `Chart.yaml`. This chart's own version is
the repository tag; it says nothing about the upstream's.

## cd-rollouts

Argo Rollouts (the controller and its five CRDs, in one upstream chart) as a
chart of its own, off unless you install it. Its only job here is Kargo's
verification: Kargo creates the `AnalysisRun` a Stage's `verification`
spawns, but it is the Argo Rollouts controller that executes it. It takes one
key, `argo-rollouts` (the upstream's values verbatim); the upstream chart has
no `global`, so this one takes none.

It is a chart of its own, not a dependency of `cd-kargo`, because the two
have different lifecycles and different namespaces:

- Its CRDs must exist, and must survive, independently of Kargo: deleting a
  CRD deletes every object of its kind. An installation prunes Kargo and
  must never prune these, so they need their own Application or release with
  their own prune policy and sync order (CRDs first).
- A subchart renders into its parent's release namespace and under its
  parent's release name; installing Argo Rollouts into the `kargo` namespace
  would move every object of an installation that already runs it elsewhere,
  which is the opposite of a zero-diff adoption.
- One that only needs the controller for verification can leave it out.

## Values

Both charts take the two keys below. `cd-argocd` also takes the opt-in extras
described under their own heading; every other top-level key is refused.
`cd-argocd` and `cd-kargo` take two top-level keys and nothing else; `cd-rollouts` takes `argo-rollouts` only.

| Key | Meaning |
| --- | --- |
| `argo-cd` (`cd-argocd`) / `kargo` (`cd-kargo`) | The upstream chart's own values, verbatim. Documented by the upstream. |
| `global` | Shared with the upstream chart as its own `global`. |

There are no defaults of our own: `values.yaml` sets both keys empty, and
every extra is off or empty. The upstream's defaults are the defaults.

## Opt-in extras (`cd-argocd`)

Optional templates for the objects that sit beside an Argo CD install. Each
is off until a value turns it on, takes everything from values (no name,
label, peer, CIDR or store is built in), and adds nothing the values do not
say: no `app.kubernetes.io/*` or `helm.sh/chart` label, no annotation. That
is what lets an installation that already runs these objects reproduce them
exactly. Namespaced objects land in the release namespace.

| Key | Renders | Notes |
| --- | --- | --- |
| `namespace.create: true` | a `Namespace` | `name` defaults to the release namespace; `labels` and `annotations` are yours (Pod Security labels, `argocd.argoproj.io/sync-options: Prune=false,Delete=false`). |
| `appProjects.<name>` | an `AppProject` | `labels`, `annotations`, and `spec` verbatim (destinations, sourceRepos, roles, ...). |
| `networkPolicies.<name>` | a `NetworkPolicy` | `labels`, `annotations`, and `spec` verbatim; `spec.podSelector` is required. Nothing is allowed unless you list it: peers, CIDRs and ports are yours. |
| `externalSecrets.<name>` | an `ExternalSecret` (`external-secrets.io/v1`) | `labels`, `annotations`, and `spec` verbatim. The store is named in `spec.secretStoreRef`, or once in the top-level `secretStoreRef` (`{kind, name}`), which fills in any spec that has none. A spec with neither is refused. The chart renders no `SecretStore`. |

The maps are keyed by object name, so layered values files merge per object
and a name cannot be declared twice. The objects are held out of the parity
comparison (the upstream chart does not render them) and pinned by the
`tests/cases/cd-argocd/extras` golden; the parity gate still proves that
switching them on moves none of the upstream chart's objects.

A sync wave, `Prune=false` or any other Argo CD annotation is yours to put in
`annotations`; the chart does not know it runs under Argo CD.

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
| Parity | `just test` | an object of the upstream chart's render that differs from the wrapper's for the same values (the wrapper's own opt-in templates are excluded and pinned by goldens) |
| Leak canary | `just leak-canary` | an account id, internal hostname, registry host or token in a tracked file |
