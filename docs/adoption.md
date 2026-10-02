# Adopting these charts with a zero diff

For an installation that already runs the upstream `argo-cd` or `kargo`
chart, the whole move is: nest the values one level, change the chart
source, and prove before merging that nothing the cluster sees changes.

## What changes in your values

Everything the upstream chart takes goes under its key.

```yaml
# before: values for the upstream chart
server:
  replicas: 2
global:
  nodeSelector:
    kubernetes.io/arch: arm64

# after: values for this chart
argo-cd:
  server:
    replicas: 2
global:
  nodeSelector:
    kubernetes.io/arch: arm64
```

`global` may stay at the root or move under the subchart's key; Helm renders
both the same (`tests/cases/cd-argocd/nested-global`). If both are set the root
one wins, which is Helm's own precedence.

A key left at the root that is not `global` is refused by the schema. That is
deliberate: without it, upstream values pasted at the root are ignored and
the install runs on the defaults.

## What changes in the install

- Helm: `helm upgrade <release> oci://ghcr.io/truvity/charts/<chart> --version X.Y.Z`
  with the **same release name and namespace** as before. The release name
  is in every object's `app.kubernetes.io/instance` label.
- Argo CD managing itself, or Kargo: an `Application` whose source was the
  upstream chart (`chart:`, `repoURL:` the upstream's) becomes an OCI source
  (`repoURL: oci://ghcr.io/truvity/charts/<chart>`, `path: .`,
  `targetRevision:` this repository's version), with the same
  `helm.releaseName` and `valuesObject` / `valueFiles`, nested.

The `helm.sh/chart` label stays `argo-cd-<version>` / `kargo-<version>`: it
is the upstream subchart's name and version, which is what renders it. The
version of THIS chart appears nowhere in the rendered objects.

## Prove it first

The claim is that the two renders are the same objects. Prove it with YOUR
values, in a branch, before the change that moves the source merges:

```sh
# 1. The upstream chart, at the version you run today, with your values.
helm template argocd argo-cd --repo https://argoproj.github.io/argo-helm \
  --version 9.7.0 --namespace argocd -f values-before.yaml \
  | grep -v '^# Source:' > before.yaml

# 2. This chart, at the release that wraps the same upstream, with the
#    same values nested.
helm template argocd oci://ghcr.io/truvity/charts/cd-argocd \
  --version 0.1.0 --namespace argocd -f values-after.yaml \
  | grep -v '^# Source:' > after.yaml

diff before.yaml after.yaml && echo "zero diff"
```

Release name and namespace must be the same in both. The `# Source:` lines
are comments Helm emits naming the template's path; they differ
(`argo-cd/templates/...` against `cd-argocd/charts/argo-cd/templates/...`) and
nothing in a cluster ever sees them. Everything else is compared byte for
byte.

Run the same comparison in your repository's test suite for the length of
the adoption: the one-off proof is the one that matters, and a second one
catches the day somebody changes a value in the same pull request that moves
the source.

## Traps

- **Another release name or namespace.** Either one changes labels and
  names throughout. The diff in the proof shows it.
- **Values layered from several files.** Layer them in the same order on
  both sides, and nest each file. A file that sets a value the other layers
  override makes the proof order-sensitive.
- **Go-templated values.** An installation that fills its values from a
  template renders them first and nests the result; the proof compares what
  Helm receives, not the template.
- **A first sync that does not reconcile.** If the proof is clean the first
  sync changes nothing. If it is not, stop: the diff names the object and the
  field, and either the values were nested wrongly or the upstream versions
  differ.
- **Moving the pin at the same time.** Do not. Move the source at the
  upstream version you run, prove zero diff, and bump the pin in a later
  change with the upstream's changelog beside it.

## After adoption

The pin you track is this chart's version. Each release's CHANGELOG entry
names the upstream version it wraps and says whether any default moved. A
release that moves a default says so under a `**Behaviour change` bullet and
names the value that restores the previous render.
