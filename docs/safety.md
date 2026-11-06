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

## A pin file that leaves the repository

**Refused by CI:** `tests/invalid/cd-pipeline/pinfile-dotdot-release.yaml`,
`pinfile-dotdot-project.yaml`, `pinfile-absolute-release.yaml` and
`pinfile-absolute-project.yaml`.

**The failure:** `promotion.pinFile` is joined to the path of the clone and
handed to `yaml-update`, which writes it. A value such as `../outside/pins.yaml`
or `/etc/pins.yaml` pointed the write outside the repository the promotion
had cloned. Once a project can set
its own `pinFile`, the value is no longer the platform's alone, so both levels
refuse it.

## A project with nowhere to write

**Refused by CI:** `tests/invalid/cd-pipeline/project-git-without-repo.yaml`,
`project-git-unknown-key.yaml`, `project-promotion-unknown-key.yaml` and
`project-credentials-without-properties.yaml`.

**The failure:** a project that overrides `git` but has neither its own nor
the release's `repoURL` would promote to nothing; one that spells an override
key wrongly (`pinfile`) would silently use the release's, and write its pin to
another project's repository. The project-level `git` and `promotion` are
strict like the release's, and a repository is required wherever it is used.

## A digest pin the chart cannot check

**Refused by CI:** `tests/invalid/cd-pipeline/digest-without-image-subscription.yaml`,
`digest-with-version-prefix.yaml`, `digest-with-promoted-version.yaml`,
`strict-semvers-without-image-subscription.yaml` and
`digest-pin-unknown-value.yaml`.

**The failure:** a digest is written to the pin, while the promoted-version
check compares the chart version an Application pins with the version being
promoted. With a digest on one side the check can never pass, and a Stage that
waits on it never promotes. A version prefix would be written in front of a
digest, and a chart subscription has no digest to write. The chart refuses the
combinations instead of rendering a pipeline that stalls at its first hop.

## A payload the pin cannot take

**Refused by CI:** `tests/invalid/cd-delivery/layer-required-below-since.yaml`,
`layer-without-since.yaml`, `layer-since-not-semver.yaml`,
`layer-since-two-parts.yaml`, `layer-unknown-ring.yaml`, `layer-unknown-key.yaml`
and `platform-values-unknown-ring.yaml`.

**The failure:** a product chart's schema is closed. A platform that sends a
key to a release that does not read yet makes Argo CD fail the sync with the
chart's schema error, and Argo CD caches that failure. The pin is written by
the delivery tool, so the platform cannot know at its own render which release
the Application will install. A layer is compared with the pin at render, and a
`required` one refuses a pin below it instead of rendering a payload the chart
cannot take or silently leaving out one it cannot run without. `since` must be
a three-part version: `1.12` would compare as something else than it reads.

## A site that runs a digest nobody pinned

**Refused by CI:** `tests/invalid/cd-delivery/static-site-pin-is-a-tag.yaml`,
`static-site-pin-is-a-short-digest.yaml`, `static-site-without-artifact.yaml`,
`static-site-without-chart-version.yaml` and
`static-site-artifact-ref-has-a-tag.yaml`.

**The failure:** the server pulls the artifact when it starts. With a tag in
the pin, or a tag left on the repository, a later push to the same tag changes
what the next pod serves without any change in the delivery repository. The
pin must be the digest the delivery tool promoted, and the repository carries no
tag that could outvote it.

## A payload that moves the site's identity

**Refused by CI:** `tests/invalid/cd-delivery/static-site-platform-values-set-artifact.yaml`,
`static-site-values-set-service-account.yaml`,
`static-site-platform-values-set-install-name.yaml` and
`static-site-platform-values-replace-site.yaml`; the same checks guard
`values`, not only `platformValues`.

**The failure:** the two Applications of a site agree on the ServiceAccount, the
install name and the artifact only because the chart writes the same value into
both. A payload that set one of those paths, or replaced `site` with a scalar,
would split the server from the identity that was created for it (the pod is
admitted as a ServiceAccount the identity does not bind), or run an artifact
other than the pinned one, and each Application would still render. The paths
are refused where they are set instead.

## A ring a site does not have

**Refused by CI:** `tests/invalid/cd-delivery/static-site-with-e2e-ring.yaml`,
`static-site-platform-values-e2e-ring.yaml`, `static-site-with-interface.yaml`
and `static-site-with-layers.yaml`.

**The failure:** a site has two rings and no interface, and its pin is not a
version, so a layer's `since` could never be compared. A key from another kind
would render without error and never be read.

In a company flow the same shape is enforced before a pin exists:
`tests/invalid/cd-company/static-site-dormant-e2e-payload.yaml` refuses an
end-to-end payload, and `static-site-company-sets-identity.yaml` and
`static-site-company-sets-artifact.yaml` refuse a company value on the registry
and identity block (the scope of the site's IAM role) and on the pinned
artifact.

## A kind whose ownership is not written down

**Refused by CI:** the fixtures `tests/invalid/cd-company/static-site-company-sets-*.yaml`
(`identity`, `identity-block`, `access`, `tier`, `route`, `tls`, `service-account`,
`fullname-override`, `install-name`, `registry-app` and `artifact`); the twin case
`tests/cases/cd-company/kind-static-site` proves that what a site legitimately
sets (`replicaCount`, `site.notFoundPage`) is still accepted.

**The failure:** the infrastructure ring of a static site carries the scope of
an IAM role. Company values reach every ring in `cd-company`, so a company that
could set `registry.ack` or `identity` there could widen the role, and one that
could set `route`, `tls`, `tier` or `access` could change where and how the site
is served. A kind has its own ownership list, which replaces the product list
rather than extending it, so a key left off it is open to a company. The list
for `static-site` is `fullnameOverride`, `serviceAccount`, `registry`,
`identity`, `access`, `tier`, `route` and `tls` on both rings, plus
`installName` on `infra` and `site.artifact` on `app`. It fails closed: a
company payload on any of them is refused, dormant or not, and a scalar cannot
replace the map above a reserved path.

## A platform payload a company overrides

**Refused by CI:** `tests/invalid/cd-company/company-overrides-platform-layer.yaml`,
`company-overrides-platform-values.yaml` and
`company-replaces-platform-subtree-with-scalar.yaml`; the twin case
`tests/cases/cd-company/layers` proves that a layer whose gate is closed
reserves nothing.

**The failure:** the company owns the pin and `values`, and the platform owns
the payload that makes a release safe to run (a replica spread, say). If a
company value could replace a path a platform layer sets, the guarantee would
hold until someone overrode it. The reserved paths follow the open layers, so
they move with the pin: a pin that moves up closes a path to the company
without any edit to the company's files, and the next render says which.

## A single chart that is given a product's facts

**Refused by CI:** `tests/invalid/cd-delivery/unknown-kind.yaml`,
`chart-with-rings.yaml`, `chart-with-interface.yaml`, `chart-with-e2e.yaml`,
`chart-without-pin.yaml` and `chart-without-repository.yaml`.

**The failure:** the product keys (`interface`, `postgres`, `e2e`, `faro`,
`bucketSlug`, the infrastructure and end-to-end rings) each make the chart
compose something for a product's charts. A single chart that was handed one by
mistake would render without error and never read it, so the operator would
believe the chart had a database role, a route or an end-to-end run. A
`chart` product takes a short list of keys and refuses the rest by name.

## A ring a single chart does not have

**Refused by CI:** `tests/invalid/cd-company/chart-kind-dormant-infra-payload.yaml`
and `chart-kind-dormant-e2e-payload.yaml`; `chart-kind-company-sets-reserved-root.yaml`
and the case `tests/cases/cd-company/kind-chart` pin what a single chart keeps.

**The failure:** `cd-company` checked a dormant service's payload against the
product rings only, and the single-chart refusal lived in `cd-delivery`, which
a dormant service never reaches. A company could commit `values.<service>.infra`
for a chart that has no infrastructure ring and see no error until the first
promotion wrote a pin, at which point the sync of every service in the flow
broke on a file nobody had touched. The refusal now follows the kind and does
not wait for a pin.

The reserved list for a chart's `values.app` is deliberately conservative: it
is the product's list, which also reserves common chart keys such as
`serviceAccount` and `tls`, so a chart that uses one of them for its own
purpose is refused until a narrower list for charts is decided on its own.
