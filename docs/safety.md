# Safety: what each refusal is for

Each refusal here earned its place with a failure. The negative fixtures
under `tests/invalid/<chart>/` keep every one refusing, each for the reason
it names (`hack/lint-fixtures.sh`).

## Upstream values at the root of the file

**Refused:** any top-level key other than `argo-cd` / `kargo` / `argo-rollouts` and, for the first two, `global`.

**The failure:** values written for the upstream chart (`server:`, `api:`)
pasted into a values file for this one. Helm hands a subchart its own key and
`global`, nothing else, so the keys are ignored, the chart renders cleanly
and the installation runs on the defaults nobody chose. For Argo CD that can
mean an OIDC sign-in that is not configured and RBAC left at its defaults,
with every check green.

**Fixtures:** `tests/invalid/cd-argocd/unnested-upstream-values.yaml`,
`tests/invalid/cd-kargo/unnested-upstream-values.yaml`,
`tests/invalid/cd-rollouts/unnested-upstream-values.yaml`, and `unknown-key.yaml`
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

## An interface the chart does not know, and keys a pin cannot take

**Refused (`cd-delivery`):** a product whose `interface` is above the highest
this chart knows, below 1 or not a whole number; `postgres.platformOwned` and
`mtls.strict` for a product whose interface is below the step that adds them;
a cloud role name over 64 characters; any input a ring needs and the platform
or product did not give (a bucket slug with no `platform.cloud`, a workload
identity with no `platform.identity`, and so on).

**The failure:** a product's charts have strict schemas, so a key a pinned
chart does not know is a render Argo CD cannot make, cached as a comparison
error, and whatever waits for that Application to become healthy (a
promotion's verification, say) waits for something that cannot happen. The
usual defence is one version comparison per key in the platform's own
templates, each a copy of a fact the chart knows. The interface number is the
one copy, and the refusals above are the cases where leaving a key out would
deploy something other than what was asked for, which is worse than failing:
a database the platform believes it owns, or a component the platform believes
is strict.

**Fixtures:** `tests/invalid/cd-delivery/interface-too-high.yaml`,
`interface-zero.yaml`, `interface-not-a-number.yaml`,
`platform-owned-below-9.yaml`, `strict-below-5.yaml`, `iam-name-too-long.yaml`,
`bucket-without-cloud.yaml`, `workload-identity-without-identity.yaml`,
`database-tls-without-root.yaml`, `faro-without-key.yaml`,
`e2e-without-bucket.yaml`, `products-without-platform.yaml`.

**Left out, not refused:** a key whose step the product has not reached
(`events.tls` below 7, `database.tls` below 8, the browser telemetry below 10,
and the whole end-to-end Application below 3) is not passed. The platform's
fact says it has the capability; the interface says this pin cannot take it
yet; the Application renders without it and the pin keeps working.

## What the chart cannot check

An input that is a free-form payload (`values`, `product`, `identityProviders`,
`access`, `ignoreDifferences`, `e2e.events`, `postgres.scheduling`) is passed
as written: what is under it is the product chart's own schema to judge, at
sync time. And an empty value in `products.<name>.values` (an empty string,
zero, false) does not override a value the chart composed, which is how Helm
merges maps; a payload that needs to switch a composed key off says so with a
non-empty value, or the input that composed it is changed.
