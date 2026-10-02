# Safety: what each refusal is for

Each refusal here earned its place with a failure. The negative fixtures
under `tests/invalid/<chart>/` keep every one refusing, each for the reason
it names (`hack/lint-fixtures.sh`).

## Upstream values at the root of the file

**Refused:** any top-level key other than `argo-cd` / `kargo` and `global`.

**The failure:** values written for the upstream chart (`server:`, `api:`)
pasted into a values file for this one. Helm hands a subchart its own key and
`global`, nothing else, so the keys are ignored, the chart renders cleanly
and the installation runs on the defaults nobody chose. For Argo CD that can
mean an OIDC sign-in that is not configured and RBAC left at its defaults,
with every check green.

**Fixtures:** `tests/invalid/cd-argocd/unnested-upstream-values.yaml`,
`tests/invalid/cd-kargo/unnested-upstream-values.yaml`, and `unknown-key.yaml`
for each.

## What the schema cannot say

The values below the subchart key are the upstream's, and the schema leaves
them open: this repository does not restate what `argo-cd` or `kargo`
accept, because a second copy of it is a second version. A typo below the key
is therefore the upstream chart's to catch, and several are not caught. The
goldens are the net: a value that does nothing leaves the render unchanged,
which a reviewer sees in the diff of a case that sets it.

## A default that moves the render

**Refused by CI:** `hack/parity.sh` fails when a wrapper renders anything the
upstream chart does not, and `hack/default-change-guard.sh` fails a pull
request that changes an existing golden without a `**Behaviour change`
bullet in the CHANGELOG.

**The failure:** an adopter proves a zero diff against release N, and release
N+1 changes a default silently. The pin bump that follows is then the
unreviewed change.

## A vendored archive that disagrees with its pin

**Refused:** `just lint` reads `helm dependency list` and fails on a
dependency that is missing or at the wrong version, and
`hack/check-app-version.sh` fails an `appVersion` that is not the upstream's.

**The failure:** moving the version in `Chart.yaml` and forgetting
`just vendor`. Helm renders whatever archive sits in `charts/`, so every
other check passes and the golden does not move: the pin says one release
and the cluster runs another.
