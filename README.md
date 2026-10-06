# cd

**Continuous delivery as Helm charts: Argo CD and Kargo, installed from their upstream charts with a strict schema, goldens and a zero-diff adoption gate around them.**

An installation that runs the upstream `argo-cd` or `kargo` chart today can
move onto these with its values nested one level and no change to a single
object in the cluster. After that, the pieces that make a delivery pipeline
out of the two (promotion stages, health gates, cluster registration) arrive
here as further charts and a small gate binary, each one adopted the same
way.

| What | Where |
| --- | --- |
| `cd-argocd` — Argo CD, from the upstream `argo-cd` chart 9.7.0 | `oci://ghcr.io/truvity/charts/cd-argocd` |
| `cd-kargo` — Kargo, from the upstream `kargo` chart 1.11.6 | `oci://ghcr.io/truvity/charts/cd-kargo` |
| `cd-rollouts` — Argo Rollouts, from the upstream `argo-rollouts` chart 2.43.2 | `oci://ghcr.io/truvity/charts/cd-rollouts` |
| `cd-delivery` — the Argo CD Applications that deliver a product's charts to a cluster; no upstream | `oci://ghcr.io/truvity/charts/cd-delivery` |
| `cd-pipeline` — the Kargo delivery pipeline of a project: Project, Warehouse, Stages, promotion, verification, access; no upstream | `oci://ghcr.io/truvity/charts/cd-pipeline` |
| `cd-cluster-registration` — Argo CD cluster Secrets from a list of clusters; no upstream | `oci://ghcr.io/truvity/charts/cd-cluster-registration` |

The repository is also a Go module (`github.com/truvity/cd`). `chartgate`
(`go run github.com/truvity/cd/cmd/chartgate -self-repo <your/repo>`) renders every Argo CD Helm
Application of a repository against its pinned chart and real values.

The `genesis` package bootstraps an Argo CD installation (the repository
credentials, from a store such as `genesis/ssmstore` seeded once from a
password manager; the cd-argocd install with its presets; the root
Application). The promotion gate binary is not here yet; it arrives in a
reviewed change of its own and is listed in the [CHANGELOG](CHANGELOG.md).

## Who it is for

A team that runs Kubernetes and wants Argo CD and Kargo installed from a
chart that is the upstream's, pinned exactly, with its values checked by a
schema and its renders held in version control. It assumes a cluster, Helm
(or Argo CD itself) to install a chart, and a Gateway or Ingress of the
installer's own for the web consoles.

It deliberately installs no identity provider, no ingress and no secret
store. Where the consoles are reached, who signs in and where the repository
credentials come from are the installer's values and Secrets. `cd-argocd`
can, when asked, also render the objects that sit beside an Argo CD install
(its Namespace, AppProjects, NetworkPolicies and ExternalSecrets) from the
installer's values; none of them exists until it is switched on.

## The model

- **A wrapper chart.** `cd-argocd`, `cd-kargo` and `cd-rollouts` each depend on exactly one
  upstream chart, vendored as an archive in the repository. The chart
  sets no default of its own.
- **Two namespaces of values.** The upstream chart's values live under its
  own key, `argo-cd` or `kargo`; `global` is shared with it. What the
  upstream documents as `server.replicas` is `argo-cd.server.replicas`.
- **Opt-in extras.** `cd-argocd` has optional templates for the Namespace,
  AppProjects, NetworkPolicies and ExternalSecrets of an install. They take
  their names, labels, peers, CIDRs and store reference from values and have
  no defaults: off, the render is the upstream's. See
  [docs/reference.md](docs/reference.md#opt-in-extras-cd-argocd).
- **The parity gate.** For every case under `tests/cases/`, the wrapper's
  render and the upstream chart's render, given the same values, are the
  same objects. [docs/adoption.md](docs/adoption.md) shows how to run the
  same proof with your own values before you move a live installation.

## Install and a worked example

```sh
helm install argocd oci://ghcr.io/truvity/charts/cd-argocd \
  --version 0.1.0 --namespace argocd --create-namespace -f values.yaml
```

```yaml
# values.yaml: neutral values (example.com, an issuer that does not exist).
argo-cd:
  server:
    replicas: 2
  configs:
    cm:
      url: https://argocd.example.com
      oidc.config: |
        name: Example
        issuer: https://issuer.example.com
        clientID: $argocd-client:client-id
        clientSecret: $argocd-client:client-secret
    rbac:
      policy.csv: |
        g, example:admin, role:admin
      policy.default: role:none
      scopes: '[groups]'
```

`cd-kargo` installs the same way, with `--namespace kargo` and its values under
`kargo:`. `cd-rollouts` is the Argo Rollouts controller and CRDs, for the one
reason Kargo needs them (see [docs/reference.md](docs/reference.md#cd-rollouts)):
install it only if you use Kargo verification; values under `argo-rollouts:`. Upstream refuses an enabled API without an admin account password
or an OIDC configuration; set one of them
(see [docs/reference.md](docs/reference.md)).

## Consumers

No consumer is recorded at `v0.1.0`; a repository that adopts these charts
adds a line here, naming the repository and the chart.

## Neighbours

- [ci-workflows](https://github.com/truvity/ci-workflows) — the shared CI and
  release workflows this repository calls.
- [policy](https://github.com/truvity/policy) — the contracts this repository
  is held to (`docs/contracts/component.md`).
- [observability](https://github.com/truvity/observability) — the charts that
  scrape and dashboard what these install; it carries Argo CD and Kargo
  dashboards.

## Documentation

- [docs/adoption.md](docs/adoption.md) — moving an installation onto these
  charts with a zero diff, and proving it first.
- [docs/safety.md](docs/safety.md) — each refusal and the failure that earned
  it.
- [docs/reference.md](docs/reference.md) — the values, the pins and what is
  compared.
- [CHANGELOG.md](CHANGELOG.md) — every release, and every default that moved.

## The rule that makes this repository public

Mechanism only. Nothing an estate owns has a default here: no hostname, no
identity provider, no organisation, no account, no cluster name, no node
selector. They are the installer's values. `hack/leak-canary.sh` fails the
build on the mechanical shapes of an estate's particulars, and review catches
the rest.

## Status

`v0.1.0` ships `cd-argocd` and `cd-kargo`, and later releases add `cd-rollouts`. Each renders the upstream chart's objects
unchanged; that is what the parity gate proves on every pull request. No
chart in this repository sets a default of its own yet.

## Development

```sh
devbox shell
just check      # lint, goldens, the zero-diff gate, the leak canary
just golden     # regenerate tests/golden/ after an intended change; read the diff
just vendor cd-argocd   # re-vendor the upstream archive after moving its pin
```

## Releasing

A tag `vX.Y.Z` publishes both charts at that version to
`oci://ghcr.io/truvity/charts`. The first release is hand-cut. Automatic
patch releases are off until they are armed on purpose; minors and majors
are always manual.

## Licence

MIT. See [LICENSE](LICENSE).
