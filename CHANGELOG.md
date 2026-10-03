# Changelog

Prose bullets, written for the consumer: what changes in the render, what
must be done first, and whether a default moved. Newest first, one
`## vX.Y.Z` heading per tag. A chart's version is the tag; the upstream
release it wraps is named in each entry.

## Unreleased

- **Added:** the `cd-pipeline` chart, which renders the Kargo delivery
  pipeline of one or more projects: per project a Project, a Warehouse, the
  ExternalSecret of the git credentials a promotion writes back with, a
  ProjectConfig, a ServiceAccount, Role and RoleBinding per access subject, the
  read-only RoleBinding, and per Stage a Stage with its promotion template
  (`argocd-wait` gate, clone, pin update, commit, then a push or a pull request
  that must merge) and its verification (a built-in promoted-version check and
  Job checks of your own, each an AnalysisTemplate with its ServiceAccount and
  ConfigMap). Every name is a value: a Project, Warehouse or Stage is exactly
  what you write, and the chart adds no label, annotation or field of its own.
  The graph is explicit (`from` per Stage). Nothing installs it unless you do
  and no existing chart's render moves. Needs `git.repoURL` and, per project,
  `name`, `stages`, `chart`. See `docs/reference.md#cd-pipeline`.
## v0.4.0

- **Added:** `cd-argocd` ships an opt-in health preset, `presets/health.yaml`:
  a values file (the way `nats-broker`'s presets are) with the Argo CD
  resource health customizations a GitOps install typically waits on, written
  to `argocd-cm` under `argo-cd.configs.cm`. Nothing changes until you layer it:
  `helm template ... -f presets/health.yaml -f my-values.yaml`, or on an Argo
  CD OCI source `helm.valueFiles: [presets/health.yaml, ...]` listed before
  your own files. The checks, each a Lua script that reads the status the
  kind's own controller writes: `Application` (the sync gate: Healthy only
  when Synced, with no sync operation running, and Healthy itself),
  `CustomResourceDefinition` (Degraded when its names are refused),
  CloudNativePG `Cluster` and `Database`, `ValkeyCluster`, Keycloak,
  NACK `Stream` and `Consumer`, any `*.services.k8s.aws` kind (ACK),
  `external-secrets.io/*`, `operator.cluster.x-k8s.io/*`, Gateway API
  `Gateway` and `GatewayClass`, Cluster API `Cluster` and `MachineDeployment`,
  and `TalosControlPlane`. The `Application` check has no exemption rule:
  an estate that wants one keeps its own copy of that one key, and its value
  wins over the preset's.
- **Layering:** values files merge by key and a string is replaced whole, so
  a `resource.customizations.health.<group>_<kind>` key of your own replaces
  that check; the wildcard checks share the one combined
  `resource.customizations` key, and setting it yourself replaces all of them.
- **Tests:** every check is run under Lua 5.1 (the dialect Argo CD embeds)
  against fixtures under `tests/health/` (`just health`, part of `just
  test`): at least one Healthy and one not-Healthy case per check, a check
  without fixtures and fixtures without a check both fail. New cases
  `health` and `health-layered` pin the render and prove it is the upstream
  chart's for the same layered values (`hack/parity.sh` and `hack/golden.sh`
  now take a case's `presets` file). Two refusals: `presets:` as a value, and
  the preset's keys at the root. No existing render changes.

## v0.3.0

- **Added:** the `cd-delivery` chart, which renders the Argo CD Applications
  that deliver a product's charts to a cluster: per product up to three, the
  infrastructure ring (`<product>-infra`), the application ring (`<product>`)
  and the end-to-end ring (`<product>-e2e`), each from the platform's facts
  and the product's pin. It carries the explicit product-chart interface only
  (truvity/policy `docs/contracts/platform.md` section 10); a product declares
  which interface its pinned charts read as an integer, and the keys each step
  adds render from that number, never from a version comparison. Takes
  `platform` (the facts of the cluster, no estate default) and `products` (pins,
  interface, per-cluster payloads); `values.schema.json` is strict throughout.
  Nothing installs it unless you do and no existing chart's render moves. See
  `docs/reference.md#cd-delivery`.

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
