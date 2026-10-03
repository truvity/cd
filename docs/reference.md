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

## Presets (`cd-argocd`)

A preset is a **values file** shipped in the chart under `presets/`. The chart
sets none by default; layer the ones you want before your own values:

```sh
helm template argocd oci://ghcr.io/truvity/charts/cd-argocd --version <v> \
  -f presets/health.yaml -f my-values.yaml
```

On an Argo CD source with the chart's OCI path, list it first:
`helm.valueFiles: [presets/health.yaml, ...]`. Maps merge, lists are replaced,
a string is replaced whole, and your values win. Pull it without installing:
`helm pull oci://ghcr.io/truvity/charts/cd-argocd --version <v> --untar`.

| Preset | What it sets |
| --- | --- |
| `health.yaml` | `argocd-cm` resource health customizations (Lua) for the kinds a GitOps install waits on: `Application` sync gate, `CustomResourceDefinition`, CloudNativePG `Cluster`/`Database`, `ValkeyCluster`, Keycloak, NACK `Stream`/`Consumer`, `*.services.k8s.aws`, `external-secrets.io/*`, `operator.cluster.x-k8s.io/*`, Gateway API `Gateway`/`GatewayClass`, Cluster API `Cluster`/`MachineDeployment`, `TalosControlPlane`. |

The `Application` check has no exemption rule (which Applications should
report Healthy at once is an estate's decision): keep your own copy of the
`resource.customizations.health.argoproj.io_Application` key. Your key replaces
the preset's. The wildcard checks share the combined `resource.customizations`
key (a ConfigMap key cannot carry `*`), so setting that key yourself replaces
all of them: re-state the ones you want.

Each check is run under Lua 5.1 against fixtures in `tests/health/` (`just
health`). A preset is covered by a case under `tests/cases/cd-argocd/` that
names it in a `presets` file, held to the same parity gate.

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
| Health checks | `just test` | a preset Lua check that answers differently from its fixture's declared status, a check with no fixtures, a fixture with no check |
| Leak canary | `just leak-canary` | an account id, internal hostname, registry host or token in a tracked file |

## cd-delivery

The Argo CD Applications that deliver a product's charts to a cluster, and
nothing else (no Argo CD, no Kargo objects). It has no upstream chart: its
templates, schema, goldens and refusals are the whole of it, so `hack/parity.sh`
does not apply to it.

A product is up to three charts, and the chart renders one Application for
each: the infrastructure ring `<product>-infra` (the objects the install
owns: cloud objects, database, event stream), the application ring `<product>`
and the end-to-end ring `<product>-e2e`, in that sync-wave order. The rings are
fixed; their wave numbers are yours. Every address a ring's chart reads is
passed by name, following the platform contract in truvity/policy
(`docs/contracts/platform.md`, section 10): the chart builds the names of
objects it asks for from the product's name (`<product>-infra-pg-rw`,
`<product>_app`, `<product>-pg-runtime`, and so on), and takes every fact of
the estate as an input.

### Inputs

`platform` is the facts of one cluster; `products` is a map of products. With
no products the chart renders nothing. With one, the platform keys marked
required below must be present, and a key the chart does not read is refused.

| Key | Meaning |
| --- | --- |
| `platform.clusterName` (required) | The destination cluster's name in Argo CD; the `{cluster}` token. |
| `platform.argocdNamespace`, `.applicationPrefix` (required) | Where the Applications live, and what their names start with (the prefix may be empty). |
| `platform.appProject` (required) | The AppProject each Application runs under, with `{product}` and `{cluster}` tokens. |
| `platform.applicationLabels` | Labels on every Application; values take the same tokens. |
| `platform.waves` (required) | `infra`, `app` and `e2e` sync-wave numbers. |
| `platform.chartRegistry` | The base the charts are published under; a product may name its own `repository`. |
| `platform.tier`, `.finalizer`, `.sync` | Install kind handed to the charts (`primary`), the resources finalizer (on), and the sync policy (prune and self-heal on, server-side apply on, create-namespace off). Mechanism, not estate facts: these have defaults. |
| `platform.cloud` | `accountID`, `region`, `permissionsBoundary`, and the `iamNameTemplate` and `bucketNameTemplate` of a product's role and bucket (tokens `{cluster}`, `{slug}`, `{product}`). Needed by a product with a `bucketSlug`. A role name over 64 characters is refused. |
| `platform.postgres` (required) | `instances`, `storage`, and optional `scheduling` and `labels` for the databases. |
| `platform.events` (required) | The broker: `url`, `storage`, `replicas`, `maxAge`, the `account` template, an optional `authAudience`, and `tls` where the broker accepts a workload identity. |
| `platform.database.rootCA`, `.identity`, `.telemetry`, `.e2e` | The root a database client verifies against; the workload-identity trust domain, mode and grant; the telemetry endpoint and sampling; the end-to-end mode, prober interval and trace store. Each is needed only by a product that uses it. |
| `products.<name>.pin`, `.interface`, `.hostname` (required) | The chart version of all three charts, the interface they read (below), and the route's hostname. |
| `products.<name>.repository`, `.parentRef`, `.surfaces`, `.bucketSlug` | Where the charts are published, the route's parent by name, additional routes, and the slug that says the product owns cloud objects. |
| `products.<name>.workloadIdentity`, `.natsIdentity`, `.mtls`, `.postgres`, `.e2e`, `.faro` | Per-cluster facts about the product: whether its namespace carries a workload identity and the broker accepts it, its mTLS peers and strict components, its database's server certificate, platform ownership and archive, its end-to-end run, and its browser telemetry. |
| `products.<name>.postgres.runtimeRole` | Default `true`: the infra chart is handed `postgres.runtimeRole`, `runtimePasswordSecret` and `runtimePassword.generate`. Set `false` to send none of the three, for an infra chart whose `postgres` schema does not have them. Not an interface step: it is per product, at any interface. |
| `products.<name>.identityProviders`, `.access`, `.product` | Passed to the application chart verbatim. |
| `products.<name>.alerts` | The product's alert values (`enabled`, `remote`, `ruleLabels`, `alertLabels`, thresholds), passed to the application chart verbatim from interface 12; below it, left out. |
| `products.<name>.values.{infra,app,e2e}` | A payload per ring, merged over what the chart composed, last. An empty value (an empty string, zero, false) cannot override one the chart composed. |
| `products.<name>.ignoreDifferences.{infra,app,e2e}` | `ignoreDifferences` entries for the ring's Application, verbatim. |

### The interface

A product's charts only ever added keys over time, and a key an older chart
does not know is a hard schema refusal that Argo CD caches, so a render must
not hand a pin a key it cannot take. `products.<name>.interface` is the
highest delivery interface the pinned charts read: a whole number, written
next to the pin and changed by whatever moves the pin. The chart renders the
keys of every step up to that number, and `fail`s on a number it does not
know (above its own highest, or below 1). It never compares a version. What
each number means is the registry's, in truvity/policy
(`docs/contracts/delivery-interface.md`); this chart's highest known interface
is in `templates/_helpers.tpl` and every change to it is named in the
CHANGELOG.

A key whose step the product has not reached is left out, not refused,
except where leaving it out would deploy something different from what was
asked for: `postgres.platformOwned` below its step, and `mtls.strict` below
its step, are refused. The end-to-end Application renders only from the
step that publishes the end-to-end chart.

### Using it from another chart

Helm cannot pass a value from one chart's values to another's, so an
installation whose pins live in a values file of its own composes `platform`
and `products` in its own templates and calls the named template
`cd-delivery.applications` with `(dict "platform" ... "products" ...)`. The
Applications are rendered by this one place either way. Helm validates only a
chart's own values against `values.schema.json`, so the named template refuses
an unknown top-level key of `platform` or of a product itself, and the
required inputs by name; the schema remains the full check for the chart
installed on its own.

## cd-pipeline

The Kargo delivery pipeline of one or more projects, and nothing else (no
Kargo, no Argo CD). It has no upstream chart, so `hack/parity.sh` does not
apply to it; its goldens and negative fixtures are its proof. Everything it
renders is named by its values: a project's name, its namespace, its
Warehouse and every Stage are exactly what you write, because renaming a
Kargo Project, Warehouse or Stage deletes it with the Freight history it holds.
The chart adds no label, annotation (beyond the sync-wave) or field of its
own: `labels` and `annotations` are empty unless you set them.

A project is a chart delivered through a list of Stages. Per project the chart
renders:

- a `Project`, and a `Warehouse` subscribed to the project's chart (or to
  `warehouse.subscriptions`, verbatim);
- the `ExternalSecret` that produces the git credentials a promotion writes
  back with, in the project's namespace, when `git.credentials` is set;
- the `ProjectConfig` that names the Stages that promote on their own;
- per `access.subjects` entry a `ServiceAccount` carrying the OIDC claims that
  map a token to it, a `Role` (read, promote the named Stages, and optionally
  approve Freight) and a `RoleBinding`; and, with `viewer` set, a
  `RoleBinding` to the shared read-only ServiceAccount;
- per Stage a `Stage`: where it takes Freight from, its verification and its
  promotion template; and what the verification needs.

### Inputs

With no projects nothing renders. Required, once there is a project: `git.repoURL`,
and per project `name`, `stages` and, unless `warehouse.subscriptions` is given,
`chart.repoURL` and `chart.semver`.

| Key | Meaning |
| --- | --- |
| `git.repoURL` | The repository a promotion writes the new pin to. |
| `git.credentials` | `secretStoreRef`, `remoteKey` and `properties` (`appID`, `installationID`, `privateKey`) of a GitHub App; renders an `ExternalSecret` per project namespace, `secretName` (default `kargo-git-writeback`), `refreshInterval` (default `1h`). |
| `promotion.*` | What a promotion writes: `branch`, `pinFile` and `pinKey` (tokens `{stage}`, `{project}`, `{slug}`, `{pinKey}`), `commitMessage` (also `{version}`), `prTitle`, `prLabels`, `provider`, and the `retry` and `gate` budgets. All have neutral defaults. |
| `waves` | The Argo CD sync-wave of each kind of object: `project`, `warehouse`, `credentials`, `config`, `stage` (0 to 4). |
| `argocd.namespace` | Where the Stages' Applications live (`argocd`). |
| `jobs`, `promotedVersion` | What the verification Jobs run as (`runAsUser`, `runAsGroup`, TTL, headroom) and the built-in check's `image`, `jqImage`, `waitSeconds`, `pollSeconds` and optional replacement `script`. |
| `viewer` | `namespace`, `serviceAccount`, `role` and `bindingName` of the shared read-only ServiceAccount each project binds; `global.claims` also renders its namespace and ServiceAccount. |
| `labels`, `annotations` | Labels and annotations on every object, beside the sync-wave. None by default; a consumer adopting objects that already exist sets Argo CD's `argocd.argoproj.io/sync-options: Prune=false,Delete=false` here to guard them. |
| `projects[].annotations` | Annotations on the Project object alone, for instance Kargo's `kargo.akuity.io/keep-namespace: "true"`: deleting a Project then leaves its namespace, and the Freight in it. |
| `projects[].name`, `.slug`, `.pinKey` | The Project and namespace; the short name used in check names and tokens (default the name); the key the version is written under. |
| `projects[].chart` | `repoURL`, `name` (index repositories), `semver`, `versionPrefix` (repositories whose tags carry one), `discoveryLimit`. |
| `projects[].warehouse` | `name` (default the project's), `interval` (`5m0s`), `freightCreationPolicy` (`Automatic`), `subscriptions`. |
| `projects[].access.subjects[]` | `name`, `claims`, `stages`, `approve`. |
| `projects[].stages[]` | `name`, `from` (the upstream Stage; none means Freight straight from the Warehouse), `autoPromote`, `applications`, `mode` (`pr` after the first Stage, `direct` for it), `verification`, `promotionTemplate`. |

Durations are written the way the API server stores them (`10m0s`, not `10m`):
Argo CD compares the manifest with the stored object and a Stage whose
duration differs only in spelling is OutOfSync forever.

### The graph is values

`from` is explicit. The chart has no rule that makes a Stage depend on
another by its name: a project whose Stages fan out, join or run in a line
says so, per Stage. Promotion by pull request is the default for any Stage that
has an upstream, and for the first it is a push to the branch.

### Promotion

Unless a Stage sets `promotionTemplate`, its promotion is: for a Stage with
upstream Applications, `argocd-wait` on the upstream Stage's `applications`
(the version being promoted must already be Healthy and Synced there; a Stage
never waits on the Applications it is about to update); then `git-clone`,
`yaml-update` of `pinFile` at `pinKey`, `git-commit`; then either `git-push`
to the branch, or `git-push` to a generated branch, `git-open-pr` and
`git-wait-for-pr`, the last three only when the commit changed something.

### Verification

`verification.promotedVersion: true` adds a built-in check run after the
Promotion succeeds: a Job reads the Stage's `applications` and fails unless
every one has the promoted chart version in its spec and in its last
comparison and is Synced, Healthy and not mid-operation, polling for
`promotedVersion.waitSeconds`. It needs a ServiceAccount and a Role in the
Argo CD namespace that allows `get` on exactly those Applications; the chart
renders both. The script is shipped in the chart (`files/`); `promotedVersion.script`
replaces it.

`verification.checks[]` are Job checks of your own: an AnalysisTemplate with one
measurement, run once, decided by the Job's exit code, under the `restricted` Pod
Security profile. `script` becomes a ConfigMap mounted at `/scripts`; `env`,
`volumes` and `volumeMounts` are verbatim. `verification.analysisTemplates`
names templates that exist already.

Names derived by the chart, from the project's `name` and `slug` and the Stage's
`name`: `kargo-verify-<stage>` (ServiceAccount), `kargo-verify-<project>-<stage>`
(Role and RoleBinding in the Argo CD namespace), `<slug>-<stage>-verify-promoted-version`
(ConfigMap) and `<slug>-<stage>-promoted-version` (AnalysisTemplate).
