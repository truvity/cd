# Reference

## Charts

| Chart | Upstream | Pin | Application version |
| --- | --- | --- | --- |
| `cd-argocd` | `argo-cd` from `https://argoproj.github.io/argo-helm` | 10.10.3 | v3.5.5 |
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
| `health-opt-in.yaml` | Replaces `health.yaml`'s `Application` check (layer it after): only an Application labelled `truvity.io/sync-gating: "true"` counts for its parent; every other one reports Healthy at once, so a parent syncs as a plain fan-out. |
| `ha.yaml` | API server and repo server: 2 replicas, a PDB (`minAvailable: 1`), a soft per-node spread. |
| `on-demand.yaml` | Every component prefers on-demand nodes (`karpenter.sh/capacity-type`), never requires them. |
| `core-only.yaml` | No ApplicationSet controller (`replicas: 0`), no notifications controller. |
| `sso-only.yaml` | No local admin, a 24h session ceiling, `policy.default: role:none`, groups from the `groups` claim. Your values supply `oidc.config`, `url` and `policy.csv`. |
| `webhook-refresh.yaml` | Polling every 300s with a 60s jitter; every webhook provider verifies against the Secret `argocd-webhook`, key `secret` (yours, labelled `app.kubernetes.io/part-of: argocd`). |
| `metrics.yaml` | Metrics Services and ServiceMonitors for the application controller, the API server and the repo server (needs the ServiceMonitor CRD). |
| `behind-gateway.yaml` | The API server behind a Gateway API gateway that ends TLS: the upstream chart's HTTPRoute on, `server.insecure: "true"`, a ClusterIP Service. Your values name the route's `parentRefs` and `hostnames` (needs the Gateway API CRDs). |
| `sync-timeout.yaml` | The application controller ends a sync operation still running after an hour (`controller.sync.timeout.seconds: "3600"`; Argo CD's default never does), freeing the Application for the next sync. |
| `envoy-gateway.yaml` | A `BackendTrafficPolicy` on the server's HTTPRoute that lifts Envoy Gateway's 15s request timeout for the UI's event stream (needs Envoy Gateway's CRDs; via `extraObjects`, which your own `extraObjects` would replace). |

The `Application` check has no exemption rule (which Applications should
report Healthy at once is an estate's decision): keep your own copy of the
`resource.customizations.health.argoproj.io_Application` key. Your key replaces
the preset's. The wildcard checks share the combined `resource.customizations`
key (a ConfigMap key cannot carry `*`), so setting that key yourself replaces
all of them: re-state the ones you want.

Each check is run under Lua 5.1 against fixtures in `tests/health/` (`just
health`). A preset is covered by a case under `tests/cases/cd-argocd/` that
names it in a `presets` file, held to the same parity gate.

## Presets (`cd-kargo`)

Same mechanism as cd-argocd's (values files under `presets/`, layered before
your values; on an Argo CD source, `helm.valueFiles: [presets/<name>.yaml]`).

| Preset | What it sets |
| --- | --- |
| `ha.yaml` | API server, Kubernetes webhooks server, external webhooks server: 2 replicas, a PDB (`minAvailable: 1`), a soft per-node spread. Written for a release named `kargo` (the spread's label selector names it). |
| `restricted.yaml` | `global.securityContext` for Pod Security `restricted` (the upstream chart puts it on every container). |
| `on-demand.yaml` | Every pod prefers on-demand nodes (`karpenter.sh/capacity-type`). |
| `metrics.yaml` | The controller's metrics Service and ServiceMonitor. |
| `sso-only.yaml` | API and UI on, plaintext behind a TLS-terminating gateway, no admin account, OIDC on with `email` as the username. Your values supply the host, the issuer, the client ids, `users` and `admins`. |
| `external-webhooks.yaml` | The external webhooks server's TLS terminated upstream. Your values supply its `host` and `basePath`. |

## cd-cluster-registration

One Argo CD cluster Secret per entry of `clusters`, in `namespace` (default:
the release namespace), and nothing else. The Secret is the registration: an
Application's `destination.name` resolves through it.

| Key | Meaning |
| --- | --- |
| `name` | The registered name. Required. |
| `secretName` | The Secret's name; default `cluster-<name>`. Two entries rendering the same Secret are refused. |
| `server` | The API server URL (`https://`). Required. |
| `config` | Argo CD's cluster config verbatim (`awsAuthConfig`, `execProviderConfig`, `tlsClientConfig`, ...), written as indented JSON with sorted keys. Required. |
| `stringData` | `true` writes `stringData` (readable); default `false` writes base64 `data`, which `kubectl apply` compares cleanly against the live object. |
| `labels`, `annotations` | Yours, verbatim; the `argocd.argoproj.io/secret-type: cluster` label is always set. |

## Go packages for a platform's generator

`delivery` and `appproject` compute the rows the delivery charts are given,
from facts the caller passes in; they read no files. `delivery.PinKey`,
`KargoProject` and `KargoProjects` name a pin and its Kargo Project;
`delivery.Product` and `PlatformCharts` build cd-pipeline's project rows from
the clusters that own each pin, in the caller's promotion order. A
`ProductInput` may also carry `Git`, `Promotion` and `Kind`, which the row
repeats for the generator to write as the project's `git` and `promotion`
overrides and the product's `kind`; they are copied, not interpreted, and
unset they leave the row what it was;
`delivery.ProberGranted` answers cd-delivery's `e2e.proberGranted`;
`appproject.Deployer` builds the `<product>-<cluster>` AppProject whose
deployer role one group holds; its destination is the namespace `product`,
unless `appproject.InNamespace` names another (an install in a namespace of
its own, `products.<name>.namespace`), and `ProductInput.Namespace` makes
`delivery.Product` pick the AppProjects of that namespace. Unset, both are what
they were.

## deploy/pulumi/registryread

A Pulumi package: the cross-account registry read identity of a delivery tool
on EKS. `registryread.New` registers, through truvity/k8s's `podidentity`
component, an IAM role and its managed policy (`PolicyDocument`: the ECR
authorization token anywhere, and the pull actions, plus list and describe for
a tool that discovers versions, on every repository of one registry account)
bound to the tool's ServiceAccount by an EKS Pod Identity association. The
readers are `ArgoCDRepoCreds` (argocd-ecr-updater, which refreshes Argo CD's
repository credentials; pull) and `KargoController` (a Warehouse lists and
describes chart versions; pull and discover), each with an identity of its
own. `Args.Description` is required: IAM cannot change a policy's
description, so adopting a live policy needs its live value. An installation
that already runs such an identity keeps its URNs and adopts the live objects
with `WithNames`, `WithImports`, `WithIgnoreAssociationTags` and
`WithResourceOptions`. ARNs are built from
`Args.Partition` (default `aws`).

One registry is `RegistryAccountID` and `RegistryRegion`; several are
`Args.Registries`, each `{AccountID, Region, Repositories}` (no repositories:
every repository of the account), and the two forms together are refused.
`PolicyDocumentFor(partition, registries, discover)` builds the policy; it
gives one registry with every repository the document `PolicyDocument` has
always given, byte for byte (the policy is compared with the live one), and
otherwise one statement whose `Resource` is a sorted, de-duplicated list. The
token statement stays on `*`; no other statement is, and no action but the
read ones is ever granted. Refused: no registry, an empty region or account
ID, a registry listed twice, an account or region with a wildcard, separator
or whitespace (a wildcard account would widen the grant), a repository pattern
that is empty, rooted, has whitespace or `..`, and a policy over the 6144
non-whitespace characters IAM allows a managed policy. The registry token
Argo CD uses is refreshed by argocd-ecr-updater in one region; reading a
registry in another region needs that tool to be told so as well.

## genesis

`genesis.Run` seeds the repository credentials Secret, installs cd-argocd
with Helm (`Config.Presets`, then `Config.ValuesFile`; leave out presets
whose objects need CRDs that do not exist yet, such as `metrics` and
`envoy-gateway`), and applies the root Application. `genesis.ResolveRepoCreds`
reads the GitHub App credentials from a `Store` (`genesis/ssmstore`: SSM
SecureString parameters) and, when any is missing, reads all three from a
`Seed` (`genesis.OnePassword`: the `op` CLI) and writes them back, so a
repeated genesis needs no seed.

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
and the end-to-end ring `<product>-e2e`, with no sync-wave (each ring retries until what it
waits for exists). The rings are fixed. Every address a ring's chart reads is
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
| `platform.appProject` (required) | The AppProject each Application runs under, with `{product}` and `{cluster}` tokens (and `{vendor}`, `{customer}`, below). |
| `platform.vendor`, `.customer` | Who makes the products of this installation and whom it is for: the `{vendor}` and `{customer}` tokens of `appProject`, `applicationPrefix` and `applicationLabels`, and of nothing else. Both optional; one of the three naming a token the platform gives no value for is refused (`tests/cases/cd-delivery/installation-tokens`). |
| `platform.applicationLabels` | Labels on every Application; values take the same tokens. |
| `platform.waves` (deprecated) | Accepted and ignored since v0.10.0; Applications carry no sync-wave. Drop it. |
| `platform.chartRegistry` | The base the charts are published under; a product may name its own `repository`. |
| `platform.tier`, `.finalizer`, `.sync`, `.syncRetry` | Install kind handed to the charts (`primary`), the resources finalizer (on), and the sync policy (prune and self-heal on, server-side apply on, create-namespace off). Mechanism, not estate facts: these have defaults.  Every Application except the end-to-end one also retries a failed sync with the latest revision (`syncRetry`: no limit, refresh, 15s doubling to 5m). |
| `platform.cloud` | `accountID`, `region`, `permissionsBoundary`, and the `iamNameTemplate` and `bucketNameTemplate` of a product's role and bucket (tokens `{cluster}`, `{slug}`, `{product}`). Needed by a product with a `bucketSlug`. A role name over 64 characters is refused. |
| `platform.postgres` (required) | `instances`, `storage`, and optional `scheduling` and `labels` for the databases. |
| `platform.events` (required) | The broker: `url`, `storage`, `replicas`, `maxAge`, the `account` template, an optional `authAudience`, and `tls` where the broker accepts a workload identity. |
| `platform.database.rootCA`, `.identity`, `.telemetry`, `.e2e` | The root a database client verifies against; the workload-identity trust domain, mode and grant; the telemetry endpoint and sampling; the end-to-end mode, prober interval and trace store. Each is needed only by a product that uses it. |
| `products.<name>.pin`, `.interface`, `.hostname` (required) | The chart version of all three charts, the interface they read (below), and the route's hostname. |
| `products.<name>.repository`, `.parentRef`, `.surfaces`, `.bucketSlug` | Where the charts are published, the route's parent by name, additional routes, and the slug that says the product owns cloud objects. |
| `products.<name>.chart` | The base name of the charts the product installs (`<chart>`, `<chart>-infra`, `<chart>-e2e`); defaults to the product's name. A tenant of another product is its own product (namespace, release, Applications) that names that product here. |
| `products.<name>.workloadIdentity`, `.natsIdentity`, `.mtls`, `.postgres`, `.e2e`, `.faro` | Per-cluster facts about the product: whether its namespace carries a workload identity and the broker accepts it, its mTLS peers and strict components, its database's server certificate, platform ownership and archive, its end-to-end run, and its browser telemetry. |
| `products.<name>.postgres.runtimeRole` | Default `true`: the infra chart is handed `postgres.runtimeRole`, `runtimePasswordSecret` and `runtimePassword.generate`. Set `false` to send none of the three, for an infra chart whose `postgres` schema does not have them. Per product, below interface 15; at 15 or higher the runtime role is not optional and `false` is refused. |
| `products.<name>.postgres.migrations` | The product runs schema migrations. From interface 15 the application ring is then handed `database.migration.passwordSecret` (the owner's credential), which the chart may mount only in a migration Job or hook Job; unset, nothing of the owner is passed. The long-running workloads get `database.app.*` alone, always. Refused below 15, where `database.owner.passwordSecret` is always passed. |
| `platform.alerts.ignoreDifferences` | `ignoreDifferences` entries the application ring's Application of every product whose alerts reach its chart (`alerts.enabled`, interface 12 or higher) gets first, before the product's own: for a rule kind whose live object carries server-side defaults the render does not (a VictoriaMetrics `VMRule` defaults `record: ""` on an alerting rule). Unset: none. |
| `platform.dashboards` | The folder and data sources of this cluster's dashboards, handed verbatim to the application ring as `observability.dashboards` of every product with `dashboards: true`. Unset (a cluster that runs no dashboards): nothing is passed. |
| `platform.availability` | `single` or `high`: the cluster's redundancy, passed to the application ring as `availability` from interface 14 (the product sizes replicas and disruption budget from it). Unset: nothing passed. |
| `products.<name>.identityProviders`, `.access`, `.product` | Passed to the application chart verbatim. |
| `products.<name>.dashboards` | The product's chart renders dashboards: it gets `platform.dashboards` as `observability.dashboards` where the platform states one. |
| `products.<name>.alerts` | The product's alert values (`enabled`, `remote`, `ruleLabels`, `alertLabels`, thresholds), passed to the application chart verbatim from interface 12; below it, left out. |
| `products.<name>.e2e.{kargoStage,sync,hook,ttlSecondsAfterFinished}` | Opt-in, for an end-to-end run that a Kargo Stage triggers. `kargoStage` (`<project>:<stage>`) renders the `kargo.akuity.io/authorized-stage` annotation on the `-e2e` Application only. `sync: manual` (default `auto`) drops `syncPolicy.automated` from it; it never carries `retry`. `hook: true` renders the suite's Job as an Argo CD Sync hook (BeforeHookCreation, sync-wave 1) with `ttlSecondsAfterFinished` (default 86400, at least 120) instead of Force and Replace. Switch a product over with the Stage's `e2eApplication` and `sync: manual` plus `hook: true` in ONE change per cluster: a tracked (non-hook) Job never reports health in the Application status, so the check cannot pass in between. The Job it replaces is deleted once afterwards, only after the Application shows manual sync. All unset: no change to the render. |
| `products.<name>.values.{infra,app,e2e}` | A payload per ring, merged over what the chart composed, last. An empty value (an empty string, zero, false) cannot override one the chart composed. |
| `products.<name>.ignoreDifferences.{infra,app,e2e}` | `ignoreDifferences` entries for the ring's Application, verbatim. |
| `products.<name>.platformValues.{infra,app,e2e}` | The platform's own payload per ring, merged over what the chart composed and under `layers` and `values`. In `cd-company` the paths it sets are reserved against the company's `values`. See [Platform values and layers](#platform-values-and-layers). |
| `products.<name>.layers[]` | Ordered `{name, since, required, values.{infra,app,e2e}}`: a payload the platform sets only for a pin at or above `since`. |
| `products.<name>.kind` | `product` (default), `chart` or `static-site`: see [Kinds](#kinds). |
| `products.<name>.namespace` | The destination namespace (default the product's name). A product of kind `product` that names one keeps its key for its release, its Helm objects and its pins. |
| `products.<name>.applicationName` | Kinds `product` and `static-site`: the middle of the product's Application names (default the key); installs of one product for several customers name it alike. |
| `products.<name>.vendor`, `.customer` | Kinds `product` and `static-site`: this product's `{vendor}` and `{customer}`, over `platform.vendor` and `platform.customer`. |
| `products.<name>.chartVersion`, `.artifact.ref` | Kind `static-site` only: the release of the two charts, and the repository of the site artifact without a tag. Both required. |
| `platform.staticSite.{infraLabels,siteAnnotations}` | Labels for the infrastructure Application and annotations for the site Application of every `static-site` product, verbatim. Unset: none. |

### Platform values and layers

`products.<name>.values` is the product's own payload and wins over everything
else. Two further channels belong to the platform:

- `platformValues.{infra,app,e2e}`: always applied.
- `layers[]`: `{name, since, required, values.{infra,app,e2e}}`, applied when the
  product's `pin` is at or above `since` (`x.y.z`).

The merge order is: what the chart composed, `platformValues`, the open layers
in list order, then `values`. As everywhere in the chart, an empty value (an
empty string, zero, false) does not override an earlier one.

A layer exists because a chart's schema is closed: a release that does not read
a key rejects it, and Argo CD caches the failure. The pin is moved by the
delivery tool without the platform rendering anything, so the platform cannot
decide at its own render whether a key is safe to send; the chart decides, with
the pin it renders. The rule is `>=since-0`: a leading `v` on the pin is
ignored, a prerelease of `since` counts as reaching it, and a pin that is not a
version (a golden sentinel, a dormant `""`) never does, so every layer is off.
`required: true` turns a pin below `since` into a refusal
(`products.<name> needs a pin >= X, got Y`) for a product that cannot run
without the payload. Choosing between clusters stays with the platform: it
passes each cluster's `layers` and `platformValues`, and the chart never sees a
cluster list. `tests/cases/cd-delivery/layers` shows four pins on the same layers.

### Kinds

A product is the three-ring shape above unless it says `kind: chart`. A chart
is one chart delivered as one Application, for a component that has no
infrastructure or end-to-end ring and reads none of the product facts:
`products.<name>` takes `kind`, `pin` (the chart version, required),
`repository` (default `platform.chartRegistry`), `chart` (default the name),
`namespace` (default the name) and `values.app`. Everything else, including
`interface`, `hostname`, `postgres`, `e2e`, `faro`, `bucketSlug`, and
`values.infra` or `values.e2e`, is refused, with a message that names the kind.
Nothing is composed for it from `platform`: what the chart needs is in
`values.app`. The Application is named `<applicationPrefix><name>` (with
`applicationSuffixes.app`, if set), and its sync policy, finalizer, AppProject,
labels and retry come from `platform` exactly as for a product ring.

A `repository` that starts with `oci://` is the base the chart is published
under (`repoURL: <repository>/<chart>`, `path: .`); any other is a chart
index (`repoURL: <repository>`, `chart: <chart>`). See
`tests/cases/cd-delivery/kind-chart-oci` and `kind-chart-index`. `platform`
still requires `postgres` and `events` whenever there is any product.

### Static sites

`kind: static-site` is a site served from an OCI artifact: two Applications of
one release of two charts, `static-site-infra` (the site's cloud identity) and
`static-site` (the server). They are named `<applicationPrefix><name>-infra` and
`<applicationPrefix><name>` (with `applicationSuffixes`, if set), both are
installed as the Helm release `<name>`, and both take `chartVersion` as their
`targetRevision` from `repository` (default `platform.chartRegistry`; an
`oci://` repository carries the chart in its address, any other is a chart
index). `products.<name>` takes `kind`, `pin`, `chartVersion`, `artifact.ref`,
`repository`, `namespace`, `values.{infra,app}` and `platformValues.{infra,app}`;
anything else, `layers` and the end-to-end ring included, is refused by name.

`pin` is the digest of the artifact (`sha256:` and 64 lower-case hex digits).
A tag is refused: the digest is what runs, and a tag can move. `artifact.ref` is
the repository without a tag or digest. The chart writes these keys, and no
`platformValues` or `values` may set them or replace a map on the way to them:

| Ring | Keys the chart writes |
| --- | --- |
| `infra` | `fullnameOverride`, `installName`, `serviceAccount.name` (each the product's name) |
| `app` | `fullnameOverride`, `serviceAccount.name` (the product's name), `site.artifact.ref`, `site.artifact.digest` |

Everything else the two charts read (the registry and identity block,
placeholders, network policy, replicas) is the platform's `platformValues` or the
product's `values`, merged in that order over the keys above. Ordering between
the two Applications is the platform's: `platform.staticSite.infraLabels` and
`.siteAnnotations` are copied verbatim onto the infrastructure and the site
Application (a gate label and a sync wave, for an installation that gates the
second on the first); the chart names none. A site that is not yet promoted is
dormant the way every other kind is: the caller leaves the product out while it
has no digest. `cd-company` delivers the kind: a company's `values.<service>.{infra,app}`
may not set the keys the chart writes, the `registry` and `identity` blocks (the
scope of the site's IAM role), `access`, `tier`, `route` or `tls`, and `values.<service>.e2e` is refused by name.
See `tests/cases/cd-delivery/kind-static-site` and `kind-static-site-minimal`.

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

Interface 13 moves the workload's identity ServiceAccount into the `-infra`
chart, rendered after its PodIdentityAssociation, on the primary tier, and
passes `tier` to the application ring as well: the same value (`test` or
`primary`) the `-infra` ring gets, where the application chart's own default
is `test`. `cloud.serviceAccountAnnotations` is not passed, since EKS Pod
Identity needs no annotation on the ServiceAccount. A product below 13 gets
no `tier` on the application ring.

Interface 14 passes `availability` (`platform.availability`, when set) to the
application ring. Interface 15 makes the database runtime role the default:
the infra ring always renders it (`runtimeRole: false` is refused), and the
application ring gets `database.app.*` and no `database.owner`; a product
that declares `postgres.migrations: true` is also handed
`database.migration.passwordSecret`, for its migration Job or hook Job alone.
A product below 14 gets no `availability`, and below 15 keeps
`database.owner.passwordSecret`.

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

## cd-company

The company delivery adapter calls `cd-delivery.applications` with composed
products. `platform`, `products` (product facts without `pin`/`values`) and the
`services` allowlist are platform-owned. Company-owned `pins` and `values` are
separate maps:

```yaml
# pins/example-service/devel.yaml
pins: {example-service: "1.2.3"}
```

```yaml
# values/example-service/devel.yaml
values:
  example-service:
    infra: {postgres: {parameters: {max_connections: "100"}}}
    app: {config: {greeting: hello}}
    e2e: {config: {timeout: 30s}}
```

The layout repeats for `stage` and `prod` and for each allowed service. Each
service requires a string pin; `""` means dormant and emits no Applications.
Non-dormant services require product facts. Unknown services, envelope/fact
keys and rings fail. `values.<service>` accepts only `infra`, `app`, `e2e`.
Company files contain only their indicated envelope; the caller enforces those
file boundaries and supplies authoritative platform facts.

Company ring values cannot override platform-owned delivery paths (and, per service, any path the platform sets through `platformValues` or an open layer of its `products` row; a layer whose pin gate is closed reserves nothing): `cloud`,
`events`, `identity`, `serviceAccount`, `tier`, `pin`, `chart`,
`repository`, `interface`, `appProject`, the route's attachment (`route.parentRef`,
`route.parentRefs`, `route.sectionName`, `route.enabled`, `route.surfaces`,
`route.faro`; the hostname and paths under `route` are the company's), or the platform's postgres fields
(sizing, scheduling, labels, roles/passwords, TLS, backup and ownership).
Generated connection, identity and install paths are also reserved; see the
[chart README](../charts/cd-company/README.md#refusals) for the full path list.
A product of `kind: chart` (see [Kinds](#kinds)) has one ring: a company
payload under `values.<service>.infra` or `.e2e` is refused by name whether or
not the service has a pin, because the shape of the kind does not depend on the
pin. Its `values.app` keeps the same reserved roots as a product's: the
platform writes nothing into a single chart's values, so the list is what keeps
a company out of the Application's own fields.
Business configuration merges using cd-delivery's existing semantics. The
adapter uses the existing cd-delivery application chart as a locked, packaged
dependency (`file://../cd-delivery`). Its own `cd-delivery.products` map must
stay empty, so only the parent's named-template call renders Applications.
`just vendor cd-company` refreshes the archive; the chart proofs check it against
the source. `python3 hack/company-schema.py --check` checks composed-schema drift.

Kargo writes normal and rollback pins: direct promotion in `devel`, PRs in
`stage`/`prod`; human pin edits are emergency-only. Humans change values through
check-gated PRs. Merged values apply at the current pin without a separate Kargo
promotion. The chart and Go tool ship from the same cd release/tag.

## chartgate

`chartgate.Gate.RunApplications` and CLI `-application <yaml>` (repeatable) plus
`-sources <yaml>` accept root Applications and explicit Git checkout/revision
mappings, without a bootstrap layout. Mappings bind exact
`repoURL`/`targetRevision` pairs to caller-supplied roots and full immutable
commit IDs. A branch source may explicitly select the company candidate SHA;
the gate verifies checkout HEAD and reports the selected revisions.

`$<ref>/...` resolves against that Application's matching named source and its
mapping. Pure refs are file-only. Files are layered in listed order (including
chart-shipped files), then `valuesObject` when present (including `{}`), otherwise
inline `values`, preserving
release name and destination namespace. Missing required files, unknown refs
and missing mappings fail; `ignoreMissingValueFiles` only skips missing files.

Both local and pinned remote charts are followed through child Applications.
The complete `cd-company` -> product-chart tree is checked, with bounded
recursion, cycle detection and context-aware render deduplication. A wrapper's
success does not hide child pull/authentication/schema/template failures; the
report identifies each failing Application and chart. Existing `Run`/CLI
`-root`/`-self-repo`/`-cluster` bootstrap mode remains available.

See [chartgate usage](../chartgate/README.md) for the complete candidate invocation,
source-mapping YAML, precedence, authentication and reporting contract. The
caller owns checkouts and status reporting; the shared gate renders charts.

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

- a `Project` (unless `createProject: false`), and a `Warehouse` subscribed to the project's chart (or to
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
and per project `name`, `stages` (or `stageOrder`, see [Derived Stages](#derived-stages)) and, unless `warehouse.subscriptions` is given,
`chart.repoURL` and `chart.semver`.

| Key | Meaning |
| --- | --- |
| `git.repoURL` | The repository a promotion writes the new pin to. |
| `git.credentials` | `secretStoreRef`, `remoteKey` and `properties` (`appID`, `installationID`, `privateKey`) of a GitHub App; renders an `ExternalSecret` per project namespace, `secretName` (default `kargo-git-writeback`), `refreshInterval` (default `1h`). |
| `promotion.*` | What a promotion writes: `branch`, `pinFile` and `pinKey` (tokens `{stage}`, `{project}`, `{slug}`, `{product}`, `{vendor}`, `{customer}`, `{pinKey}`; see [Project tokens](#project-tokens)), `commitMessage` (also `{version}`), `prTitle`, `prLabels`, `provider`, `seedPinFile` (write a missing pin file on a Stage's first promotion; see [A missing pin file](#a-missing-pin-file)), and the `retry` and `gate` budgets. All have neutral defaults. |
| `projects[].git` | A per-project override of `git`: `repoURL` replaces the release's, and `credentials` replaces the release's `git.credentials` for this project's namespace (`false` renders no `ExternalSecret` for it). A project without it renders exactly as before. See [Per-project git and promotion](#per-project-git-and-promotion). |
| `projects[].promotion` | A per-project override of `promotion`, with the same keys: what the project sets replaces the release's value, except that `retry` and `gate` merge key by key. |
| `waves` | The Argo CD sync-wave of each kind of object: `project`, `warehouse`, `credentials`, `config`, `stage` (0 to 4). |
| `argocd.namespace` | Where the Stages' Applications live (`argocd`). |
| `jobs`, `promotedVersion` | What the verification Jobs run as (`runAsUser`, `runAsGroup`, TTL, headroom) and the built-in check's `image`, `jqImage`, `waitSeconds`, `pollSeconds`, optional replacement `script`, and `gateImage` (empty by default): the `promotion-gate` image, which runs the check as Go instead of the script (see [Verification](#verification)). |
| `metricsGate` | The built-in metrics gate (see [Verification](#verification)): `gateImage`, `image` and `jqImage` (default `promotedVersion`'s), `serviceAccount` (`metrics-gate-{stage}`), `issuerHost`, `endpoints.<stage>` (`url`, `caBundle`), `token` (`audience`, `expirationSeconds`, `volumeName`, `mountPath`), `caBundle` (`configMap`, `key`, `volumeName`, `mountPath`) and the bake numbers (`phase1Seconds`, `bakeSeconds`, `scrapeLagSeconds`, `pollSeconds`, `minJourneys`, `maxFailureRatio`, `checkFiringAlerts`, `proberJourneyMetric`). |
| `graph` | The promotion graph a project with `stageOrder` derives its Stages from: `upstreams`, `applicationName` (`{stage}-{app}`), `promotedVersion`, `approvers` (`subject`, `claims`) and `admins` (`subject`, `claims`). See [Derived Stages](#derived-stages). |
| `viewer` | `namespace`, `serviceAccount`, `role` and `bindingName` of the shared read-only ServiceAccount each project binds; `global.claims` also renders its namespace and ServiceAccount. |
| `labels`, `annotations` | Labels and annotations on every object, beside the sync-wave. None by default; a consumer adopting objects that already exist sets Argo CD's `argocd.argoproj.io/sync-options: Prune=false,Delete=false` here to guard them. |
| `appProjects` | Argo CD `AppProject`s by name, each `{labels, annotations, spec}` with `spec` verbatim, rendered in `argocd.namespace` beside the pipeline. The chart-wide `labels` and `annotations` (the Kargo objects' guard) are not put on them. None by default. |
| `projects[].createProject` | `true` (default) renders the project's `Project`. `false` renders none and everything else of the project as before, in its namespace: for an installation where one party owns the cluster-scoped Project (and so the namespace) and another owns the Warehouse, Stages, access and credentials in it. With `argocd.namespace` set, the verification's Roles read Applications from that namespace. `annotations` are refused with it (`tests/cases/cd-pipeline/no-project`). |
| `projects[].annotations` | Annotations on the Project object alone, for instance Kargo's `kargo.akuity.io/keep-namespace: "true"`: deleting a Project then leaves its namespace, and the Freight in it. |
| `projects[].name`, `.slug`, `.pinKey` | The Project and namespace; the short name used in check names and tokens (default the name); the key the version is written under. |
| `projects[].vendor`, `.customer` | Who makes the product and whom this installation is for: the `{vendor}` and `{customer}` tokens of the project's name templates. Optional; see [Project tokens](#project-tokens). |
| `projects[].chart` | `repoURL`, `name` (index repositories), `semver`, `versionPrefix` (repositories whose tags carry one), `discoveryLimit`, `subscribeAs` (`chart`, the default, or `image`: an image subscription with SemVer selection and `strictSemvers: false`, for an OCI chart whose `v`-prefixed tags Helm's tag listing drops; `oci://` is dropped from the repository, the pin takes the discovered tag as it is, `name` and `versionPrefix` are refused). `pin` (`tag`, the default, or `digest`: the promotion writes the Freight image's digest; see [Promoting a digest](#promoting-a-digest)), `strictSemvers` (an image subscription's own setting; unset keeps `false`). |
| `projects[].sources` | Further sources of the project's chart, by name (`[a-z0-9-]`, at most 30 characters), each with the keys of `chart`. Each one gets a Warehouse of its own, `<warehouse>-<source>`, with `warehouse`'s `interval` and `freightCreationPolicy` (`warehouse.subscriptions` is `chart`'s alone). A Stage that takes a source (`stages[].source`, or `stageSources` for derived Stages) requests that Warehouse's Freight, and its promotion and checks read the source in place of `chart`; see [Further sources](#further-sources). |
| `projects[].customers` | Derived Stages only: the customers one Kargo Project delivers the product to, each a name or `{name, chart}`. Each gets a Warehouse `<warehouse>-<customer>` (on `chart`, or its own) and a chain of Stages, one for each Stage of `stageOrder`, named by `graph.stageName` (default `{stage}-{customer}`); a customer's Stages request only its Warehouse and promote only from its own, and its first Stage promotes on its own. The project has no Warehouse of its own. `{stage}` in Application names and pins is the Stage of `stageOrder`, `{customer}` the customer; approvers are one subject for each customer's Stage. With `sources`, each customer also has a Warehouse `<warehouse>-<customer>-<source>` for each source (on `customers[].sources.<source>`, else `sources.<source>`), which its Stage of a Stage `stageSources` names takes. |
| `projects[].companions` | Further artifacts that move with the chart in one Freight, by name, each with the keys of `chart` and a `pinKey` (default the chart's filled key with `-<companion>`). Every Warehouse of the project subscribes to the chart and to each companion; each Stage writes every pin in one `yaml-update`. A customer may hold a companion to its own `semver` (`customers[].companions.<name>.semver`); a companion may name its artifact from a further source (`companions.<name>.sources.<source>`, the keys of `chart` laid over its own), which that source's Warehouses subscribe to. See [Companions](#companions). |
| `projects[].warehouse` | `name` (default the project's), `interval` (`5m0s`), `freightCreationPolicy` (`Automatic`), `subscriptions`, `freightCreationCriteria`. `freightCreationCriteria.expression` (required, non-empty) is rendered verbatim as the Warehouse's `spec.freightCreationCriteria`: Kargo creates Freight from a discovery only when the expression is true, for instance `imageFrom('repo-a')?.Tag == imageFrom('repo-b')?.Tag` over two `subscriptions`, so the two images move together. It applies to the project's own Warehouse alone, not to the Warehouses of `sources`; needs a Kargo that supports it (1.11 does). Unset, nothing is rendered (`tests/cases/cd-pipeline/freight-creation-criteria`). |
| `projects[].access.subjects[]` | `name`, `claims`, `stages`, `approve`. |
| `projects[].access.approvers`, `.admins` | With `stageOrder`: derive one approver subject per Stage after the first, or one admin subject (see [Derived Stages](#derived-stages)). |
| `projects[].stageOrder` and the keys it reads | `upstreams`, `autoPromote`, `noAutoPromote`, `noPrereleases`, `stageSources`, `applications`, `applicationsByStage`, `e2eStages`, `e2eApplication`, `metricsGateStages`: see [Derived Stages](#derived-stages). |
| `projects[].metricsGate` | The project's metrics-gate inputs: `namespace` and `release` of the workload (default the slug), `e2eJobRequired` (default true). |
| `projects[].stages[]` | `name`, `from` (the upstream Stage; none means Freight straight from the Warehouse), `source` (a key of `sources`; none means `chart`; a Stage and its upstream take the same one), `autoPromote`, `noAutoPromote` (the ProjectConfig names the Stage with `autoPromotionEnabled: false`, over `autoPromote`), `noPrereleases` (the Stage's promotion refuses a version with a prerelease part; not with `promotionTemplate`), `applications`, `mode` (`pr` after the first Stage, `direct` for it), `verification`, `promotionTemplate`, `e2eApplication` (an Application that runs the end-to-end suite as a sync hook: the promotion ends with an `argocd-update` step on it, `retry.timeout` `25m0s`, and a check requires the Application to pin the promoted version, its last operation to have Succeeded, with the synced revisions equal to the revisions the Application resolves to (for an OCI chart both are digests, never the version string), and the suite Job of that version to have succeeded (a Sync-hook Job of the operation with `hookPhase` `Succeeded`, or, before `e2e.hook` is on, a Healthy tracked Job); the step is refused by Kargo unless the Application carries `kargo.akuity.io/authorized-stage` for this Stage, which is `products.<name>.e2e.kargoStage` in `cd-delivery`; it is not listed in `applications`; needs `promotedVersion.image` and `.jqImage`). |

Durations are written the way the API server stores them (`10m0s`, not `10m`):
Argo CD compares the manifest with the stored object and a Stage whose
duration differs only in spelling is OutOfSync forever.

### The graph is values

`from` is explicit, or derived from rules you give as values (`graph`, see
[Derived Stages](#derived-stages)). The chart has no built-in rule that makes
a Stage depend on another by its name: a project whose Stages fan out, join or
run in a line says so, per Stage or per graph. Promotion by pull request is the default for any Stage that
has an upstream, and for the first it is a push to the branch.

### Derived Stages

A project may instead give `stageOrder`, its Stage names in promotion order,
and let the chart derive the Stages from the `graph` rules. `graph.upstreams`
maps a Stage to the Stages it may promote from, in preference, and a project's
own `upstreams` replaces the entries it names. Then, for each Stage of
`stageOrder`:

- its `from` is the first of its candidates that is also a Stage of the
  project; with none it takes Freight straight from the Warehouse;
- it promotes on its own when it has no upstream, or when the project's
  `autoPromote` lists it, unless the project's `noAutoPromote` lists it (below);
- it watches `applicationsByStage.<stage>`, else `applications`, else the
  slug, each named through `graph.applicationName` (tokens `{stage}`, `{app}`
  and the [project tokens](#project-tokens));
- on the Stages `e2eStages` lists, `e2eApplication` (default `<slug>-e2e`,
  through the same name) is its end-to-end Application;
- it runs the promoted-version check when `graph.promotedVersion` is true, and
  the metrics gate on the Stages `metricsGateStages` lists;
- it takes the source `stageSources.<stage>` names, else `chart`.

The project's first Stage is the last one, in order, with no upstream.
`access.approvers` adds one subject per Stage after the first, named
`graph.approvers.subject` with the claims `graph.approvers.claims` (tokens
`{stage}`, `{slug}`, `{project}` in every string): it may promote the first
Stage and its own, and the one of the last Stage may also approve Freight.
`access.admins` adds one subject, `graph.admins`, that promotes every Stage
and approves. Subjects in `access.subjects` come first, verbatim. A project
gives `stages` or `stageOrder`, never both, and the derived Stages render
exactly as the same Stages spelled out would (`tests/cases/cd-pipeline/graph`).

`noAutoPromote` lists the Stages that do not promote on their own. It wins
over both rules above, so it is how a pipeline is held still: while a project
moves to another Kargo Project, say, and only one of the two may write a pin.
A name that is not a Stage of `stageOrder` is refused. The Stage is then
written into the `ProjectConfig` with `autoPromotionEnabled: false` instead of
being left out, so the object stays and says so: a ProjectConfig that an
earlier render left behind (`Prune=false`) cannot keep the Stage promoting.
With every Stage held, the ProjectConfig holds only such entries. A Stage
spelled out takes `noAutoPromote: true` for the same
(`tests/cases/cd-pipeline/no-auto-promote`).

`noPrereleases` lists the Stages that take releases only. A Stage and its
upstream take Freight from the same Warehouse, so once that Warehouse
discovers release candidates (a range that ends in `-0`) for an earlier Stage
to run, every later Stage may be given them too. A Stage this key lists
refuses them: its promotion starts with a `fail` step (alias `no-prerelease`)
whose condition is `semverParse(<version>).Prerelease() != ''`, and one more
for each companion (`no-prerelease-<companion>`). The step reads the
discovered tag, also where the pin is a digest. The promotion fails before it
waits on anything or clones the repository, and its message names the Stage
and the version. A tag that is not a version fails the condition, and the
promotion with it. Kargo does not hide such Freight from the Stage: a person
can still start the promotion, and it fails. A name that is not a Stage of
`stageOrder` is refused, and so is the key on a Stage whose
`promotionTemplate` is spelled out. A Stage spelled out takes
`noPrereleases: true` for the same (`tests/cases/cd-pipeline/no-prereleases`).
An admission policy that lists the steps a Stage may name needs `fail` before
the key is set.

### Project tokens

Every name template of a project takes the project's tokens: `{project}` (its
`name`, the Kargo Project), `{slug}` and `{product}` (its `slug`; `{product}`
is the word `cd-delivery`'s templates use for the same thing, so one spelling
serves both charts), and `{vendor}` and `{customer}` where the project sets
`vendor` and `customer`. They are filled in `promotion.pinFile`, `pinKey`,
`commitMessage` and `prTitle` (the release's or the project's own) and in
`graph.applicationName`. So a project whose Kargo Project is named for its
installation keeps short names everywhere else:

```yaml
promotion: {pinFile: "pins/{customer}/{stage}.yaml"}
graph: {applicationName: "{stage}-{customer}-{app}"}
projects:
  - name: cd-acme-globex-shop    # the Project and its namespace
    slug: shop                   # {slug}, {product}: checks, Applications, pin
    vendor: acme
    customer: globex
```

A template that names `{vendor}` or `{customer}` for a project that does not
set it is refused, not rendered with the token left in a name or a pin key; a
project without them lays its own `promotion` over such a release
(`tests/cases/cd-pipeline/installation-tokens`).

### Further sources

A project may install some Stages from another repository than `chart`: preview
builds of every commit on a development cluster, say, and releases everywhere
else. `sources` names each further repository with the keys of `chart`, and
each one gets a Warehouse, `<warehouse>-<source>`. A Stage takes one with
`source` (`stageSources.<stage>` when derived): it requests that Warehouse's
Freight, and its promotion's `vars.chartRepo`, the promoted-version check's
`CHART_REPO` and its digest rule read the source in place of `chart`. Freight
does not cross Warehouses, so a Stage and its upstream take the same source,
refused at render otherwise. A Stage that takes Freight straight from a
Warehouse other than the first's has no upstream: give it an empty
`upstreams` entry. When a derived project names any source, such a Stage that
is not the first of `stageOrder` promotes by pull request, as a hop would,
rather than by a push to the branch (`tests/cases/cd-pipeline/sources`).

### Promotion

Unless a Stage sets `promotionTemplate`, its promotion is: for a Stage with
upstream Applications, `argocd-wait` on the upstream Stage's `applications`
(the version being promoted must already be Healthy and Synced there; a Stage
never waits on the Applications it is about to update); then `git-clone`,
with `promotion.seedPinFile` a `file-write` of a missing pin file (below),
`yaml-update` of `pinFile` at `pinKey`, `git-commit`; then either `git-push`
to the branch, or `git-push` to a generated branch, `git-open-pr` and
`git-wait-for-pr`, the last three only when the commit changed something.

#### A missing pin file

Kargo's `yaml-update` neither creates a file nor adds a key: it refuses a key
the file does not hold. A Stage whose pin file is absent therefore cannot be
promoted into until someone writes the file. With `promotion.seedPinFile: true`
(default `false`; a project may set its own) each Stage's promotion first runs
a `file-write` step, alias `seed-pin`, with `overwrite: false` and
`continueOnError: true`. It writes the pin file with every key the Stage is
about to write (the chart's and each companion's), each `""`, which is
dormant, and `yaml-update` then writes the pins into it. The step changes no
file that exists: on every promotion after a Stage's first it fails with
"already exists", `continueOnError` keeps that from failing the Promotion,
and the Promotion succeeds. **That failed `seed-pin` step is expected; do not
alert on it.** The step never adds a key to an existing file, so a pin file
shared by several Stages or projects still needs each key written by hand.
A pin key with an escaped dot (`\.`) is refused, because the seed is built by
splitting the key on dots. Cases `cd-pipeline/seed-pin-file` and
`cd-project/seed-pin-file`.

### Per-project git and promotion

`git` and `promotion` are the release's defaults. A project may lay its own
`git` (`repoURL`, `credentials`) and `promotion` (the same keys as the
release's) over them, so projects of one release can write their pins to
different repositories, branches, pin files and keys, with different GitHub
Apps, without a release per repository. The effective value is the release's
with the project's keys replacing it; the one exception is the `retry` and
`gate` maps of `promotion`, which merge key by key. `git.credentials: false`
renders no `ExternalSecret` for that project's namespace. The Warehouse,
Stage and access objects of a project, and every object of a project that sets
nothing, are unchanged, so a release can start giving one project its own
repository without renaming or recreating a Kargo object (which would delete
its Freight); `tests/proof/pipeline_test.go` proves it on
`tests/cases/cd-pipeline/per-project-git`.

Refused: a `pinFile`, at the release or in a project, that is absolute or
contains `..` (the file is written inside the repository the promotion
cloned); a project with neither its own nor the release's `git.repoURL`,
once the chart needs one; `git.credentials` without `properties`; any key the
chart does not read.

### Promoting a digest

An artifact that is deployed by what it is rather than by what it is called
(a static site published as an image, say) is pinned by digest.
`chart.pin: digest`, with `subscribeAs: image`, makes the promotion write
`imageFrom(...).Digest` where it would write the tag, and adds a `{tag}`
token (the tag the Freight was discovered under) to `promotion.commitMessage`
and `promotion.prTitle`. Set `chart.strictSemvers: true` when the live
Warehouse has Kargo's own default, so the spec does not differ from the stored
object forever.

A digest is not a chart version, so there is nothing for the promoted-version
check to compare with the Application's: a Stage of a digest project that asks
for `verification.promotedVersion` is refused, and a derived Stage leaves the
check out even when `graph.promotedVersion` turns it on for the release.
`versionPrefix` and `name` are refused with `subscribeAs: image` already.
Everything else (the gate on the upstream Stage's Applications, a push to the
branch for the first Stage, a pull request for the rest) is unchanged.

`tests/proof/pipeline_digest_test.go` renders `tests/cases/cd-pipeline/digest-pin`
and compares its Warehouse and Stages with a hand-spelled pipeline of the same
shape (`tests/proof/testdata/static-pipeline.yaml`): the objects are equal, so
a platform that spells such a pipeline out can switch to the chart without
renaming, and so losing the Freight of, any object. The one difference is
the name of the promotion's variable that holds the repository (`chartRepo`
here).

### Companions

A product whose release is more than one artifact, such as an api chart and a
static bundle published beside it with versions of their own, promotes them
together: `projects[].companions` names the further artifacts, each with the
keys of `chart` (`subscribeAs: image` and `pin: digest` for a bundle deployed
by its digest). Every Warehouse of the project (its own, each source's and
each customer's) subscribes to the chart and to every companion, with no
`freightCreationCriteria`, so a new version of either makes one Freight that
holds both, and the pair that reaches a later Stage is the pair that ran on
the Stage before it.

Each Stage writes the chart's pin and then one pin per companion in the same
`yaml-update` of the pin file, so in one commit and one pull request. A
companion's key is its `pinKey` template (the tokens of `promotion.pinKey`),
by default the chart's filled key with `-<companion>` after it:
`pins.dev-globex` and `pins.dev-globex-web`. Its value is read from the
Freight by the companion's repository (`imageFrom("<repository>").Digest`),
so a companion's repository may be neither the chart's nor another
companion's. The Stage watches the Applications `applications` names, so list
every part's (`applications: [shop-api, shop-web]`).

With `customers`, a shared companion makes Freight in every customer's
Warehouse, and each customer's chain promotes it on its own; a customer held
to its own line of a companion (`customers[].companions.<name>.semver`, in its
Warehouse alone) takes a fix of that line without the others, as a customer's
own `chart` does for the chart.

A companion from a further source: `companions.<name>.sources.<source>`, with
the keys of `chart` laid over the companion's own (usually `repoURL`, and
`semver` for its prerelease line), is what every Warehouse of that source
subscribes to in place of the companion, so a Warehouse of preview charts
holds preview bundles beside them and the Stages that take it promote both
from there. With `customers` and `sources` each customer has a Warehouse of
each source, `<warehouse>-<customer>-<source>`, on its chart from that source
(`customers[].sources.<source>`, the keys of `chart`, else `sources.<source>`)
and every companion from that source; a customer's Stage of a Stage that
`stageSources` names takes it, and a Stage with no upstream that is not the
first of `stageOrder` promotes by pull request, as without customers. A
customer's own companion `semver` holds in its released Warehouse only.
Refused besides: a customer's or a companion's source that is not a key of
`sources`, and a customer whose Warehouse of a source is another customer's
name (`tests/cases/cd-pipeline/customer-sources`). Refused: companions with
`warehouse.subscriptions` (verbatim, so the companions would not be in the
Freight), a Stage that spells its own `promotionTemplate`, a customer's
companion the project does not have, and a companion key that names a token
the project does not set (`tests/cases/cd-pipeline/companions`,
`tests/cases/cd-pipeline/companions-one-install`).

With the promoted-version check on, each companion has a check of its own
beside the chart's, `<slug>-<stage>-promoted-<companion>`, with the Stage's
argument `<companion>-digest` (`imageFrom("<repository>").Digest`) or
`<companion>-version`. One check per part means a part that none of the
Stage's Applications renders fails on its own; one check for all parts would
skip it as long as another part matched. A companion pinned by digest is
judged by its artifact: an Application renders it when a source's Helm values
name the repository (`ARTIFACT_REF`), and it is at the promoted version when
they name the digest too, compared (`status.sync.comparedTo`), Synced and
Healthy. A companion pinned by tag is judged as a chart of its own
repository. Both runtimes take it, the script and the promotion-gate image
(`tests/cases/cd-pipeline/companion-checks`). With
`promotedVersion.objects: false` (a platform rendering a company's pipeline,
cd-project) the Stage names the companions' checks and the platform renders
them, as it does the chart's.

### Verification

`verification.promotedVersion: true` adds a built-in check run after the
Promotion succeeds: a Job reads the Stage's `applications` and fails unless
every one has the promoted chart version in its spec and in its last
comparison and is Synced, Healthy and not mid-operation, polling for
`promotedVersion.waitSeconds`. It needs a ServiceAccount and a Role in the
Argo CD namespace that allows `get` on exactly those Applications; the chart
renders both. The script is shipped in the chart (`files/`); `promotedVersion.script`
replaces it.

`promotedVersion.gateImage` runs the same check as Go instead: the Job runs
that image with `/ko-app/promotion-gate promoted-version` and the same
environment (`JQ` aside), and the chart renders no script ConfigMap and no jq
volume for it, so neither `promotedVersion.image` nor `.jqImage` is needed by
it (the end-to-end operation check still uses both). The image is
`ghcr.io/truvity/cd/promotion-gate:<release>`, built from this repository's
`cmd/promotion-gate` at every tag; the check decides exactly as the script
does (`promotiongate.PromotedVersion`). Empty, the default: the script, and
the render is unchanged.

The same image's `metrics-gate` subcommand is the bake-window metrics gate
(`promotiongate.MetricsGate`): wait until the promoted version is the only one
serving, then pass on a sample count of prober journeys read from a
Prometheus-compatible endpoint, failing early on a failure ratio, a container
restart, a firing alert or the version's own failed end-to-end Job. It can be
a `verification.checks[]` entry of your own with `image`, `command:
[/ko-app/promotion-gate, metrics-gate]`, its environment in `env` and its
ServiceAccount token projected for the issuer's audience in `volumes` and
`volumeMounts` (`tests/cases/cd-pipeline/promotion-gate` is a worked example),
or the chart's built-in one below. Its environment: `CHART_VERSION`, `PROJECT_NAME`,
`WORKLOAD_NAMESPACE`, `WORKLOAD_RELEASE`, `ISSUER_HOST`, `METRICS_BASE_URL`,
`SA_TOKEN_FILE`, `PHASE1_SECONDS`, `BAKE_SECONDS`, `SCRAPE_LAG_SECONDS`,
`POLL_SECONDS`, `MIN_JOURNEYS`, `MAX_FAILURE_RATIO`, `CHECK_FIRING_ALERTS`,
`PROBER_JOURNEY_METRIC` (required); `CA_BUNDLE` (the metrics endpoint's
private root; the token exchange always verifies against the system trust
store), `E2E_JOB_REQUIRED` (`true`), `TOKEN_ENDPOINT`
(`https://$ISSUER_HOST/token`), `METRICS_QUERY_URL`
(`$METRICS_BASE_URL/api/v1/query`), `EXCHANGE_CLIENT_ID` and
`EXCHANGE_AUDIENCE` (`metrics-gate`), `MAX_ITERATIONS` (`0`, a test bound).
Exit 0 passes, 1 fails; 2 is a missing or malformed input and 5 a response
that is not the JSON it should be.

`verification.metricsGate: true` (or a derived Stage that `metricsGateStages`
lists) adds the chart's own metrics gate, before the Stage's own checks: the
shell script `files/verify-metrics-gate.sh` (the same decisions, `image` with
curl and jq mounted from `jqImage`) or, with `metricsGate.gateImage`, the
image's `metrics-gate`. The chart renders the ServiceAccount
`metricsGate.serviceAccount`, the script ConfigMap `<slug>-<stage>-verify-metrics-gate`
(not with `gateImage`) and the AnalysisTemplate `<slug>-<stage>-metrics-gate`,
whose Job gets the environment above from `metricsGate` and the Stage's
`endpoints` entry (`url` is `METRICS_BASE_URL`; `caBundle: true` mounts
`metricsGate.caBundle.configMap` and sets `CA_BUNDLE`), its ServiceAccount
token projected for `metricsGate.token.audience`, and a deadline of
`phase1Seconds + bakeSeconds + jobs.headroomSeconds`. The issuer exchange must
admit the ServiceAccount; the chart renders no grant
(`tests/cases/cd-pipeline/graph`, `tests/cases/cd-pipeline/metrics-gate-image`).

`verification.checks[]` are Job checks of your own: an AnalysisTemplate with one
measurement, run once, decided by the Job's exit code, under the `restricted` Pod
Security profile. `script` becomes a ConfigMap mounted at `/scripts`; `env`,
`volumes` and `volumeMounts` are verbatim. `verification.analysisTemplates`
names templates that exist already.

Names derived by the chart, from the project's `name` and `slug` and the Stage's
`name`: `kargo-verify-<stage>` (ServiceAccount), `kargo-verify-<project>-<stage>`
(Role and RoleBinding in the Argo CD namespace), `<slug>-<stage>-verify-promoted-version`
(ConfigMap, not with `gateImage`) and `<slug>-<stage>-promoted-version` (AnalysisTemplate).

## cd-project

One company project per release. A platform renders one Argo CD Application
per project that installs this chart: the company's repository supplies value
files (`project`, `values`, `pins`), and the platform's own facts arrive as the
Application's `valuesObject` (`clusters`, `platform`, `products`, `pipeline`,
`platformDigest`). The chart renders, and only renders:

- per cluster of `project.clusters`, the Applications `cd-company` renders for
  the project as its one service on that cluster (infra, app and e2e rings, as
  the product's kind decides), in `platform.<cluster>.argocdNamespace`. A
  cluster whose pin is `""` is dormant and renders none;
- the project's Kargo `Warehouse`, `Stage`s and `ProjectConfig`, derived by
  `cd-pipeline` from `project.pipeline.stageOrder` with `createProject: false`.

The cluster-scoped Kargo `Project`, the write-back credentials, verification
(AnalysisTemplates, their ConfigMaps, ServiceAccounts, Roles and RoleBindings)
and access are the platform's and are never rendered here. The render is
checked against those four kinds and Argo CD's `Application` at render time
as well (`tests/proof/project_test.go` holds every case to the same list).

### Inputs

```yaml
# Company files (Helm deep-merges them; per-cluster files are keyed by cluster).
project:
  name: shop                       # the service name and, unless pipeline.project says otherwise, the Kargo namespace and Warehouse
  source:
    chart: oci://registry.example.com/charts/shop
    kind: product                  # optional: product (default; three rings) or chart (app ring only)
    infraChart: oci://registry.example.com/charts/shop-infra   # optional; must be <chart>-infra; not with kind chart
    semver: ">=1.0.0 <2.0.0"
    versionPrefix: v               # optional, as cd-pipeline's chart.versionPrefix
    subscribeAs: chart             # optional: chart (default) or image
  clusters: [dev, stage, prod]
  pipeline:
    stageOrder: [dev, stage, prod] # each a cluster of project.clusters
    autoPromote: [dev]
    noAutoPromote: [prod]          # optional: Stages that never promote on their own
    noPrereleases: [prod]          # optional: Stages that refuse a version like 1.4.0-rc.1
    upstreams: {prod: [stage]}     # optional, over pipeline.graph.upstreams
    stageSources: {dev: preview}   # optional: the Stage takes project.sources.preview
  sources:                         # optional: further sources of the same chart
    preview:
      chart: oci://preview.example.com/charts/shop   # the same <chart>, another repository
      semver: ">=1.0.0-0 <2.0.0"
      # versionPrefix, subscribeAs: optional, as in source
values:                            # values/<cluster>.yaml
  dev:
    app: {config: {greeting: hello}}
pins:                              # pins/<cluster>.yaml, written by Kargo
  dev: "1.4.0"
  stage: "1.3.2"
  prod: ""                         # dormant; so is a cluster with no pin at all
```

A cluster of `project.clusters` with no key in `pins` (its pin file is absent)
is dormant, exactly as `""` is: it renders no Applications, and its Stage
exists. A new project needs no hand-written `pins: {<cluster>: ""}`. Kargo's
`yaml-update` does not create a file, so the pin file must exist by the time
the cluster's Stage is first promoted into, unless the platform's
`pipeline.promotion.seedPinFile` is on: then the first promotion writes it
(see [A missing pin file](#a-missing-pin-file)).

```yaml
# The platform's valuesObject.
clusters: [dev, stage, prod]       # registered for this project
platform:                          # cd-company's `platform`, per cluster
  dev: {clusterName: dev, argocdNamespace: argocd, applicationPrefix: dev-, ...}
products:                          # cd-company's `products.<service>`, per cluster, less repository/chart/kind
  dev: {interface: 15, hostname: shop.dev.example.com}
pipeline:                          # the allowlisted cd-pipeline settings
  git: {repoURL: https://git.example.com/acme/shop-delivery.git}
  promotion: {pinFile: "pins/{stage}.yaml"}
  argocd: {namespace: argocd}
  graph: {applicationName: "{stage}-{app}"}
  project:                         # optional: the project's Kargo Project, see below
    name: "cd-{vendor}-{customer}-{project}"
    vendor: acme
    customer: globex
    noAutoPromote: [dev]           # Stages the platform holds back
    # noPrereleases: [prod]        # optional: Stages the platform keeps prereleases out of
    # stages: [dev]                # optional: this release is the leading part of a split project
    # handedOver: [dev]            # optional: or the rest of it (see below)
platformDigest: <sha256 of the four keys above, see below>
```

`pipeline` takes cd-pipeline's `labels`, `annotations`, `waves`, `argocd`,
`git.repoURL`, `promotion`, `jobs` and `graph.upstreams` / `graph.applicationName`,
laid over cd-pipeline's defaults (maps merge key by key), and `project`, which
is cd-project's own ([The Kargo Project and its namespaces](#the-kargo-project-and-its-namespaces)).
Nothing that renders
credentials, verification, access, a Project or an AppProject is accepted:
`git.credentials`, `graph.promotedVersion`, `graph.approvers`, `graph.admins`,
`promotedVersion`, `metricsGate`, `viewer`, `projects` and `appProjects` are
refused by the schema, as is a `createProject` in `project`. A Stage writes the
pin of its cluster: `promotion.pinKey` defaults to `pins.{stage}`, the key the
chart reads, and `promotion.pinFile` names the company file it lives in.

The chart source is the company's. `project.source.chart` (`oci://<repository>/<chart>`)
is the Warehouse's one subscription and, split at its last `/`, the
`repository` and `chart` the chart puts into every cluster's product facts
before cd-company renders them, so every ring installs from it: `<chart>`,
`<chart>-infra` and `<chart>-e2e` from that repository at the cluster's pin.
The infra chart is released with the application chart and follows its pin;
the Warehouse does not subscribe to it. `source.infraChart` is optional and,
where given, must be `<source.chart>-infra`. The kind of product is the
company's as well: `source.kind` is `product` (the default: `<chart>`,
`<chart>-infra` and `<chart>-e2e`) or `chart` (one Application per cluster,
named after the project, the app ring alone; see [Kinds](#kinds)), put into
every cluster's product facts as `kind`. A `chart` refuses `infraChart`, and
cd-company's ring check refuses its `values.<cluster>.infra` and `.e2e`
(`values.<cluster>.infra: kind chart takes values.app only`). The platform's
`products.<cluster>` follow the kind: a `product` needs `interface` and
`hostname`; a `chart` takes `namespace` and nothing else. A
`products.<cluster>` that sets `repository`, `chart` or `kind` is refused
(schema, and again at render): the platform restricts which sources may be
installed through its AppProjects' `sourceRepos`, not through facts.
`static-site`, delivered by digest from a chart release of its own, has no
chart for the Warehouse and is not a `source.kind`.

`project.sources` are further sources of the same product: `chart` (the same
`<chart>` as `source.chart`, from another repository), `semver`,
`versionPrefix`, `subscribeAs`; `default` is not a source name. Each one is a
Warehouse of its own, `<project>-<source>`. A cluster whose Stage
`pipeline.stageSources` names a source installs every ring from that source's
repository: its product facts take the source's `repository`, and
`source.infraChart`, where given, follows it; `kind` stays `source`'s. The
platform still decides what a cluster may install through its AppProjects'
`sourceRepos`, so a source's repository must be one the cluster's AppProject
allows (`tests/cases/cd-project/sources`).

Refused at render: a `project.clusters` entry the platform did not register in
`clusters`, or one without `platform.<cluster>`; a `pins` or `values` key that
is not in `project.clusters`; a Stage that is not a cluster of the project, an
`autoPromote`, `noAutoPromote`, `noPrereleases`, `upstreams` or `stageSources` Stage that is not
in `stageOrder` (the company's `noAutoPromote` and `noPrereleases` or the platform's); a
`pipeline.project.name` that does not fill to a namespace name; a template
that names `{vendor}` or `{customer}` where the platform gives none;
a `stageSources` value that is not a key of `project.sources`; a source whose
chart is not `oci://` or not `source.chart`'s `<chart>`; a Stage whose
upstream takes another source; a non-dormant cluster (a cluster with no pin, or `""`, is dormant) with no `products.<cluster>`. Ring
payloads are held to cd-company's ownership rules by cd-company's own code: the
fixed reserved paths, and whatever the platform sets for the cluster through
`products.<cluster>.platformValues` and its open layers, dormant or not.

### A project split across two releases

A project that moves to a new Kargo Project cluster by cluster runs in two
releases of `cd-project` for a while, one per Kargo Project, both from the
company's one `project.yaml`. The platform says which part each release is, in
`pipeline.project`, sealed with the rest of `pipeline`:

- `stages`: the leading Stages of `project.pipeline.stageOrder` that this
  release renders, Applications and Stages, and no other. The company's
  `clusters`, `stageOrder`, `autoPromote`, `noAutoPromote`, `noPrereleases`, `upstreams` and
  `stageSources` are narrowed to them, and a further source that none of them
  takes is left out (`project.source`'s Warehouse is rendered as always). Only
  a leading part, so a kept Stage stays first, keeps its access and its way of
  promoting.
- `handedOver`: clusters of `project.clusters` whose Applications the other
  release renders. This release renders none for them and needs neither
  `clusters` nor `platform` facts for them; their Stages stay as they were and
  are held (`noAutoPromote`), so only one Kargo Project writes a cluster's pin.
  The rest of the render is byte-identical to the unsplit one.

The two halves meet only where Freight does not cross: a Stage that the split
cuts from its upstream is refused, so a company whose next Stage takes from the
previous one first gives it no upstream (`upstreams: {stage: []}`, Freight
straight from its Warehouse), as `tests/cases/cd-project/sources` does. Refused:
`stages` that are not the head of `stageOrder`, a kept Stage whose upstream is
left out, `stages` with `handedOver`, a `handedOver` cluster that is not in
`project.clusters` or one that leaves no cluster, and either key with
`project.customers` or `project.companions`
(`tests/cases/cd-project/split-leading`, `split-rest`).

### The Kargo Project and its namespaces

By default the Kargo Project is named after the project, and so are the
namespace of its Warehouse, Stages and ProjectConfig and the Warehouse itself.
The platform may name it otherwise, for instance
`cd-<vendor>-<customer>-<project>`:

```yaml
pipeline:
  project:
    name: "cd-{vendor}-{customer}-{project}"   # {project}, {product}: project.name
    vendor: acme
    customer: globex
```

`project.name` is then the slug (cd-pipeline's `slug`) and the Kargo Project
is `cd-acme-globex-shop`. What is named from the slug does not move: the
Applications (`<applicationPrefix><project>`), the Applications a Stage
watches (`graph.applicationName`), the pin key (`pins.<stage>`), the workload
namespace and the names of the checks. What is named from the Project does:
the namespace of the Kargo objects, the ProjectConfig, and cd-pipeline's
`{project}` token (the Warehouse keeps `project.name` unless the platform names
it, below), so a `promotion.commitMessage` that should keep saying
`shop` names `{slug}` or `{product}`. A Kargo Project cannot be renamed: a new
name is a new Project, created by the platform like the old one, with a new
Freight history; the pins in the repository stay.

The Warehouse is named by the platform too, apart from the Kargo Project:

```yaml
pipeline:
  project:
    warehouse: shop-globex   # the product, or <product>-<customer>
```

`pipeline.project.warehouse` is the name of the project's Warehouse as given;
a further source's is `<warehouse>-<source>` and a customer's
`<warehouse>-<customer>`, and every Stage requests its Freight from them
(`tests/cases/cd-project/warehouse`). Unset, the Warehouse is `project.name`.
A Warehouse cannot be renamed in place: a new name is a new Warehouse, and its
Freight starts again. A name that is not an object name is refused.

Where the Applications live, and under which AppProject, is the platform's as
it always was, per cluster:

- `platform.<cluster>.argocdNamespace` is the namespace of that cluster's
  Applications, and `pipeline.argocd.namespace` is where the Stages look for
  them. Set both to the Kargo Project's name and the project's Applications
  live in its own namespace (Argo CD must be told to read Applications there,
  and the AppProjects must list it in `sourceNamespaces`; neither is rendered
  here).
- `platform.<cluster>.appProject` is a template (`{product}`, `{cluster}`,
  `{vendor}`, `{customer}`): `"{product}-{cluster}"` is one AppProject per
  cluster, which for one installation is one per Stage, `<project>-<stage>`.
  The AppProjects themselves are the platform's and are not rendered here.
- `platform.<cluster>.vendor` and `.customer` give that cluster's Applications
  the same two tokens in `appProject`, `applicationPrefix` and
  `applicationLabels`; `pipeline.project.vendor` and `.customer` give them to
  the name above and to cd-pipeline's templates (`promotion.pinFile`, `pinKey`,
  `commitMessage`, `prTitle`, `graph.applicationName`). So a new installation
  may be named `<stage>-<customer>-<project>-<part>`
  (`tests/cases/cd-project/own-namespace-new-names`), and one that exists keeps its
  names by leaving the templates as they are (`tests/cases/cd-project/own-namespace`).

All of it is under `platform` and `pipeline`, which the digest seals, and the
company's `project` has no key for any of it: a company file cannot rename
its Kargo Project or move its Applications (fixtures
`company-sets-kargo-project`, `company-sets-argocd-namespace`,
`project-sets-kargo-project`).

A Stage is held back from promoting on its own by the company
(`project.pipeline.noAutoPromote`) or by the platform
(`pipeline.project.noAutoPromote`); the two lists are added together and
passed to cd-pipeline's `noAutoPromote` ([Derived Stages](#derived-stages)).
The platform's is the one that quiesces a pipeline during a move, since two
renders of one project read the same company files and only the platform's
values can tell them apart (`tests/cases/cd-project/no-auto-promote`).

Prereleases are kept out of a Stage the same way, by the company
(`project.pipeline.noPrereleases`) or by the platform
(`pipeline.project.noPrereleases`); the two lists are added together and
passed to cd-pipeline's `noPrereleases` ([Derived Stages](#derived-stages),
`tests/cases/cd-project/no-prereleases`). The platform's is the one a company
cannot take away.

### A product with several customers

A product installed once for each of several customers has one Kargo Project
for all of them (cd-pipeline's `projects[].customers`,
[Derived Stages](#derived-stages)). The customers are the platform's:
`pipeline.project.customers` registers them, sealed with the rest of
`pipeline`, and is not set beside `pipeline.project.customer`. The company
names the ones it installs in `project.customers`, each a name or
`{name, chart, semver}` with a chart of its own in place of
`project.source.chart` (`infraChart`, where `project.source` has one, becomes
`<chart>-infra`). A name the platform did not register, a name given twice, a
platform list with no company list (or the other way round) are refused.

Each customer gets a Warehouse, `<project>-<customer>`, and a chain of Stages
along `project.pipeline.stageOrder`, named by `pipeline.graph.stageName`
(default `{stage}-{customer}`, with `{stage}` the cluster). Each customer's
install on a cluster is keyed by its Stage's name: `pins.<stage>`,
`values.<stage>` and the platform's `products.<stage>` (`pins.dev-globex`); a
key that is a bare cluster is refused. The install is cd-company's service
`<project>-<customer>`, so its Helm release and, unless the platform's
products name another, its namespace; its Applications take the customer as
`{customer}` and the project's name as their middle
(`applicationPrefix: "dev-{customer}-"` gives `dev-globex-shop`). The
platform may name that middle itself, `pipeline.project.installName` (sealed;
tokens `{project}`, `{customer}`, and `{vendor}` where `pipeline.project`
sets it), to put the customer after the project: `{project}-{customer}` with
`applicationPrefix: "dev-"` gives `dev-shop-globex`, and a companion's
`dev-shop-globex-web`; the Stages watch the same names. It is refused without
`project.customers`, and when it does not give a name
(`tests/cases/cd-project/install-name`). The pin key
defaults to `pins.<graph.stageName>`, so a Stage writes the key its install
reads, and `promotion.pinFile` may name `{customer}` to give each customer a
file of its own (`tests/cases/cd-project/customers`).

With `project.sources` (preview builds on the first Stage, say), each customer
has a Warehouse of each source too, `<project>-<customer>-<source>`, on its
chart from that source: `project.customers[].sources.<source>`, an `oci://`
reference, else `project.sources.<source>.chart`. The source's `semver`,
`versionPrefix` and `subscribeAs` hold. A customer with a chart of its own
names its own for each source, since a source gives the same chart from
another repository; one that does not is refused. A customer's install on a
cluster whose Stage takes the source (`project.pipeline.stageSources`) takes
every ring from that chart, and its companions serve the artifact of that
source (below) (`tests/cases/cd-project/customer-sources`).

### Companions of a project

A release that is a chart and a static bundle with a version of its own (an
api and its web front end) names the bundle in `project.companions.<name>`:
`artifact`, the bundle's repository without a tag or digest, and `semver`.
cd-pipeline's `companions` ([Companions](#companions)) put it in the same
Freight as the chart: each Warehouse subscribes to both, and each Stage writes
the chart's pin and the bundle's digest, `<pin key>-<name>`, in one commit.

Beside each install (a cluster, or a customer's Stage) the companion is an
install of the platform's `static-site` chart, keyed `<key>-<name>` in `pins`,
`values` and `products`: `pins.dev-globex-web` holds the digest,
`values.dev-globex-web` the company's site payload, and the platform's
`products.dev-globex-web` the `chartVersion` and `platformValues` (a platform
that names the companion's `artifact` or `kind` is refused: they are the
company's). It is the service `<service>-<name>` in the install's namespace
unless the platform names another, and for a customer it takes the customer as
`{customer}` and `<project>-<name>` as the middle of its Application names
(`dev-globex-shop-web` and `dev-globex-shop-web-infra` beside
`dev-globex-shop`). The Stages watch `<project>` and `<project>-<name>`
through `graph.applicationName`, so the template must name both parts' site
Applications with `{app}`.

A customer may hold the shared bundle to its own line,
`project.customers[].companions.<name>.semver`, in its Warehouse alone: with a
customer's own `semver` for the chart, a fix of an older line of either
reaches that customer and no other, and it is written in the company's own
repository (`tests/cases/cd-project/companions`).

A project of companions alone: `project.source` may be absent when
`project.companions` is set, for a release that is a static bundle whose api
is not delivered here yet. Each install is then its companions alone, keyed
`<key>-<companion>` as beside a chart; the Warehouses subscribe to the
companions alone and each Stage writes and checks their pins alone. The
customers name no chart, and a source is `{}`, naming only its companions.
Adding `project.source` later adds the chart beside them and renames none of
their keys, Applications or checks (`tests/cases/cd-project/companions-alone`).

A companion from a further source of `project.sources`:
`project.companions.<name>.sources.<source>.artifact` (and `semver`, default
the companion's). Every Warehouse of that source subscribes to it, and an
install on a cluster whose Stage takes the source serves it, so the digest
the Stage promotes and the repository the site pulls it from agree. A source
not named keeps the companion's own artifact. A source that is not a key of
`project.sources`, and an artifact with a tag or digest, are refused.

### The platform's range of the chart

The company's `project.source.semver` (and each source's and customer's) is
the range its Warehouse subscribes with. The platform may seal a range of its
own, `pipeline.project.semver`: a floor below which it cannot install the
chart, or a ceiling it has not prepared for. Every Warehouse of the project
then subscribes within both, as one constraint: the two ANDed, an OR on
either side distributed (`>=0.1.0 <1.0.0 || >=2.0.0` within `>=0.4.7` is
`>=0.1.0 <1.0.0 >=0.4.7 || >=2.0.0 >=0.4.7`), so the company keeps its own
narrower range, for a fix of an older line, and never subscribes outside the
platform's. A pin the company writes by hand outside that constraint is
refused (fixture `pin-outside-platform-range`), and a platform range that is
not a semver constraint fails the render (`platform-semver-invalid`). Unset,
the company's range alone (`tests/cases/cd-project/platform-semver`).

### The platform's metrics gate

The platform may gate a company's Stages on its metrics as it does its own:
the sealed `pipeline.project.metricsGateStages` lists Stages of
`project.pipeline.stageOrder`, and each names `<slug>-<stage>-metrics-gate`
in its verification beside the promoted-version check. cd-project renders
cd-pipeline with `metricsGate.objects: false`, so the gate's ServiceAccount,
ConfigMap and AnalysisTemplate are never in a company's render: the platform
renders them where the gate runs, under those names, with its own issuer,
endpoints and bake numbers (`tests/cases/cd-project/metrics-gate`). A Stage
that is not in `stageOrder` is refused, and the gate's own settings stay out
of a company's reach (`pipeline.metricsGate` is refused, fixture
`pipeline-metrics-gate`).

### Company and platform

Helm sees merged values, not which file supplied a key. Argo CD gives the
`valuesObject` precedence over every value file, so a company file cannot
change a value the platform sets; it can only add to the platform's keys. The
platform seals them: `platformDigest` is

```text
sha256sum (toJson (dict "clusters" $clusters "pipeline" $pipeline "platform" $platform "products" $products))
```

over the four keys exactly as the platform passes them. Outside Helm it is
the lowercase hex SHA-256 of the compact JSON of one object with the keys
`clusters`, `pipeline`, `platform` and `products`, every object's keys sorted
bytewise, no whitespace, strings escaped as Go's `encoding/json` does (`<`,
`>` and `&` as `\u003c`, `\u003e`, `\u0026`), numbers as Go formats a
`float64` (`2`, `0.05`). In Go that is `json.Marshal` of a
`map[string]any{"clusters": ..., "pipeline": ..., "platform": ..., "products": ...}`
built from the same values. A key the platform leaves out is `[]` or `{}`,
never `null`: Helm drops a null value, so the chart would hash something else.
`TestProjectDigestIsPlainJSON` (tests/proof) holds every case's sealed digest
to exactly that computation. The chart computes the same over what it
received and refuses a render that differs, naming the digest it got. The `valuesObject` also wins
on `platformDigest` itself, so a company file cannot replace the seal. The
schema refuses every other top-level key, and holds the values of the two
packaged dependencies (`cd-company`, `cd-pipeline`) to their own defaults, so
neither side can reach a subchart's own render.

### Composition

`cd-company` and `cd-pipeline` are local dependencies (`file://../cd-company`,
`file://../cd-pipeline`), packaged under `charts/cd-project/charts/` with
`just vendor cd-project` (after `just vendor cd-company` when cd-delivery
moved). Their own templates render nothing: their values stay at their
defaults. The children come from `cd-company.render`, the template
`cd-company`'s own `applications.yaml` calls, given the cluster's slice of the
inputs; the Kargo objects from `cd-pipeline.derive` and `cd-pipeline.project`.
`tests/proof/project_test.go` proves the Applications of every case byte-identical
to cd-company's render of each cluster, the Kargo objects byte-identical to
cd-pipeline's render of the same project, and the packaged archives identical to
their sources. `python3 hack/company-schema.py` composes the schema from
cd-delivery's and cd-pipeline's.
