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
| `products.<name>.identityProviders`, `.access`, `.product` | Passed to the application chart verbatim. |
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
