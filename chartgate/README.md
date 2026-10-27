# chartgate

Render the actual Argo CD Application tree with its pinned charts and complete
values. Local and remote chart output are both followed: a successful
`cd-company` wrapper is followed through its generated infra/app/e2e Applications
to their product charts. Any descendant pull, schema or template failure fails
the run and names the Application and chart. Rendering does not deploy resources
or assert runtime health.

## Explicit Application inputs

The platform caller supplies root Application manifests, its authoritative
configuration checkout, and the company candidate checkout at recorded commits.
No `clusters/` layout is needed. Git checkout/authentication, candidate selection,
dispatch and status reporting are caller responsibilities.

For example, `root-applications.yaml` can contain this Application (replace
`X.Y.Z` with the shared cd release's chart version):

```yaml
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata:
  name: devel-company
spec:
  project: company
  destination: {name: devel, namespace: argocd}
  sources:
    - repoURL: oci://ghcr.io/truvity/charts/cd-company
      path: .
      targetRevision: X.Y.Z
      helm:
        releaseName: devel-company
        valueFiles:
          - $reviewed/pins/example-service/devel.yaml
          - $reviewed/values/example-service/devel.yaml
          - $authority/platform.yaml
    - repoURL: https://git.example.com/example-org/platform.git
      targetRevision: platform-deploy
      ref: authority
    - repoURL: https://git.example.com/example-org/company.git
      targetRevision: company-deploy
      ref: reviewed
```

The candidate's pins and values have the [cd-company file
contract](../charts/cd-company/README.md); `platform.yaml` contains the complete
`platform`, `products` and `services` shown there. The caller checks the company
file envelopes and layers the authoritative platform facts last.

`source-mappings.yaml` binds the exact `(repoURL, targetRevision)` pair to the
checkout the caller selected. Replace the commit placeholders with full 40- or
64-character Git commit IDs, for example from `git -C <checkout> rev-parse HEAD`:

```yaml
- repoURL: https://git.example.com/example-org/platform.git
  targetRevision: platform-deploy
  root: ./platform-checkout
  revision: <platform-commit-sha>
- repoURL: https://git.example.com/example-org/company.git
  targetRevision: company-deploy
  root: ./company-candidate-checkout
  revision: <company-candidate-commit-sha>
```

Relative checkout roots are relative to the mappings file. Multiple revisions
of one repository may have distinct mappings and roots; duplicate bindings are
errors. The gate verifies each checkout's HEAD matches the supplied immutable
revision before rendering. The caller provides clean checkouts containing those
revisions. A deployment branch can be explicitly bound to a PR's candidate SHA;
the gate uses that checkout throughout and never fetches or substitutes the
branch tip. The report exposes requested revisions, selected SHAs, refs and roots.

```sh
# In this checkout:
go run ./cmd/chartgate \
  -application root-applications.yaml \
  -sources source-mappings.yaml \
  -require-ecr

# From a consumer, use the same cd release as the adapter chart:
go run github.com/truvity/cd/cmd/chartgate@vX.Y.Z \
  -application root-applications.yaml \
  -sources source-mappings.yaml \
  -require-ecr
```

`-application` is repeatable; each YAML stream can contain multiple root
Applications. Empty streams, unknown mapping fields, missing source bindings
and mismatched checkout HEADs are failures. Explicit mode does not use
`-self-repo` as a fallback and cannot be combined with `-cluster`.

## Values and source resolution

Values are layered exactly in this order:

1. Chart defaults.
2. `helm.valueFiles`, in the listed order, including chart-shipped files.
3. `helm.valuesObject` when present, including `{}`; otherwise inline
   `helm.values`.

`valuesObject` replaces the entire inline `values` representation. Keys found
only in the inline string do not survive when an object is supplied; listed
files and chart defaults still merge with the selected inline representation.

`$<ref>/path` must match that Application's `spec.sources[].ref`; ref names are
arbitrary. The path is relative to that source's mapped checkout root, not its
optional `path`. It cannot leave the checkout, including through a symlink.
Refs are scoped to each Application; children must declare the refs they use.
Pure `ref` sources supply files and are never rendered as charts. A mapped Git
source with a `path` is rendered from its checkout as a local chart.

Ordinary value-file paths are relative to the chart. Remote chart files are
extracted from the actual pulled archive. Local chart files may use `../` while
remaining within their Git checkout. `ignoreMissingValueFiles` drops missing
files only; an unknown ref or missing source mapping is always an error. Release
names (`helm.releaseName`, otherwise Application name) and destination namespaces
are passed to Helm.

## Package API

```go
apps, err := chartgate.ParseApplications(manifests)
if err != nil {
    return err
}
gate := &chartgate.Gate{
    Helm: "helm",
    CacheDir: cacheDir,
    RequireECR: true,
    Sources: []chartgate.SourceMapping{
        {RepoURL: platformURL, TargetRevision: platformBranch, Root: platformRoot, Revision: platformSHA},
        {RepoURL: companyURL, TargetRevision: deploymentBranch, Root: candidateRoot, Revision: candidateSHA},
    },
}
results := gate.RunApplications(ctx, apps)
if chartgate.Report(output, results, elapsed) != 0 {
    return errors.New("Application validation failed")
}
```

Each `Result.Sources` lists the selected Git revisions for that Application.
Do not run the same `Gate` concurrently. `Jobs` (CLI `-jobs`) bounds concurrent
Helm invocations; chart pulls/logins and identical renders are deduplicated.
Render identity includes the entire Application, all source refs, values,
release name and namespace. Distinct service contexts are checked independently.
Cycles fail explicitly. `MaxDepth`/`-max-depth` defaults to 32 levels;
`MaxRenders`/`-max-renders` defaults to 10000 distinct contexts. Evolving cycles
are stopped by those bounds. Repeated traversal retains failures.

## Existing bootstrap mode

```sh
go run github.com/truvity/cd/cmd/chartgate@vX.Y.Z \
  -root . -self-repo example-org/platform -cluster devel,stage,prod
```

Without `-cluster`, the CLI discovers names in `values/clusters/*.yaml` and
starts from `clusters/<cluster>.yaml`. `-self-repo` recognizes this checkout's
Git URL. Its named refs (including the usual `$values`) resolve to that root;
unrelated plain Git manifests are reported as not checked. This mode retains
the caller's existing working-tree behavior without asserting an immutable SHA.

## Authentication and failures

Private ECR charts use `aws ecr get-login-password` followed by Helm registry
login, once per host. `-aws-profile`/`CHART_GATE_AWS_PROFILE` explicitly opts into
a profile; otherwise AWS uses its ambient chain. Helm state is isolated under
the cache, selected with `-cache`/`CHART_GATE_CACHE`.

`-require-ecr` makes a failed ECR login a failure; it defaults on in GitHub
Actions or with `CHART_GATE_REQUIRE_ECR=true`. A credential-free local run may
report those charts as not checked. Every chart pull failure is a failure,
including private-registry authentication errors. No required descendant is
silently discarded. The report names each failing Application/chart and includes
Helm's error (capped at 25 lines). CLI exit status is 0 on success, 1 for gate
failures, and 2 for CLI/input-file errors.
