{{/*
One company project: per cluster of the project, the Applications
cd-company renders for it (the shared `cd-company.render`, so the children are
byte-identical to cd-company's for the same single-cluster inputs), then the
project's Kargo Warehouse, Stages and ProjectConfig (cd-pipeline's named
templates, with no Project, credentials, verification or access).
Call with the chart's root context.
*/}}
{{- define "cd-project.render" -}}
{{- $v := .Values -}}
{{- include "cd-project.checkDigest" $v -}}
{{- $p := $v.project -}}
{{- $name := $p.name -}}
{{- range $c := $p.clusters -}}
  {{- if not (has $c $v.clusters) -}}
    {{- fail (printf "cd-project: project.clusters: %q is not a cluster the platform registered for this project (clusters: %s)" $c (join ", " $v.clusters)) -}}
  {{- end -}}
  {{- if not (hasKey $v.platform $c) -}}
    {{- fail (printf "cd-project: platform.%s is missing: the platform supplies the facts of every cluster it registered" $c) -}}
  {{- end -}}
{{- end -}}
{{- range $kind := list "pins" "values" -}}
  {{- range $c := keys (index $v $kind) | sortAlpha -}}
    {{- if not (has $c $p.clusters) -}}
      {{- fail (printf "cd-project: %s.%s: %q is not a cluster of project.clusters" $kind $c $c) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- if and $p.source.infraChart (eq ($p.source.kind | default "product") "chart") -}}
  {{- fail "cd-project: project.source.infraChart: kind chart has one ring (app) and no infra chart" -}}
{{- end -}}
{{- $order := $p.pipeline.stageOrder -}}
{{- range $s := $order -}}
  {{- if not (has $s $p.clusters) -}}
    {{- fail (printf "cd-project: project.pipeline.stageOrder: %q is not a cluster of project.clusters (a Stage promotes the pin of its cluster)" $s) -}}
  {{- end -}}
{{- end -}}
{{- range $s := ($p.pipeline.autoPromote | default list) -}}
  {{- if not (has $s $order) -}}
    {{- fail (printf "cd-project: project.pipeline.autoPromote: %q is not a Stage of project.pipeline.stageOrder" $s) -}}
  {{- end -}}
{{- end -}}
{{- range $s := keys ($p.pipeline.upstreams | default dict) | sortAlpha -}}
  {{- if not (has $s $order) -}}
    {{- fail (printf "cd-project: project.pipeline.upstreams: %q is not a Stage of project.pipeline.stageOrder" $s) -}}
  {{- end -}}
{{- end -}}
{{- $out := "" -}}
{{- range $c := $p.clusters -}}
  {{- $products := dict -}}
  {{- if hasKey $v.products $c -}}{{- $_ := set $products $name (include "cd-project.facts" (dict "cluster" $c "product" (index $v.products $c) "source" $p.source) | fromYaml) -}}{{- end -}}
  {{- $pins := dict -}}
  {{- if hasKey $v.pins $c -}}{{- $_ := set $pins $name (index $v.pins $c) -}}{{- end -}}
  {{- $values := dict -}}
  {{- if hasKey $v.values $c -}}{{- $_ := set $values $name (index $v.values $c) -}}{{- end -}}
  {{- $out = print $out (include "cd-company.render" (dict "chart" "cd-project" "at" (dict $name $c) "platform" (index $v.platform $c) "products" $products "services" (list $name) "pins" $pins "values" $values)) "\n" -}}
{{- end -}}
{{- $out = print $out (include "cd-project.pipeline" .) "\n" -}}
{{- include "cd-project.assertKinds" $out -}}
{{- $out -}}
{{- end -}}

{{/*
The platform's four keys are sealed by platformDigest: sha256 of toJson of
{clusters, pipeline, platform, products} as the platform passes them. The
platform's valuesObject wins over every company file on a key both set, so a
company file can only add to the four; an addition moves the digest, and the
render is refused. Call with .Values.
*/}}
{{- define "cd-project.checkDigest" -}}
{{- $sum := sha256sum (toJson (dict "clusters" .clusters "pipeline" .pipeline "platform" .platform "products" .products)) -}}
{{- if ne $sum .platformDigest -}}
{{- fail (printf "cd-project: clusters, platform, products and pipeline are the platform's and do not match platformDigest: a company file sets one of them, or the digest is stale (received sha256 %s)" $sum) -}}
{{- end -}}
{{- end -}}

{{/*
The project's Kargo objects: cd-pipeline's defaults with the platform's
`pipeline` laid over them (maps merge key by key, everything else replaces),
one project derived from `stageOrder`, `createProject: false`. The pin key
defaults to `pins.{stage}`, the key this chart reads a cluster's pin from.
Call with the chart's root context.
*/}}
{{- define "cd-project.pipeline" -}}
{{- $v := .Values -}}
{{- $p := $v.project -}}
{{- $values := include "cd-project.merge" (dict "base" (index $v "cd-pipeline") "over" $v.pipeline) | fromYaml -}}
{{- if not (dig "promotion" "pinKey" "" $v.pipeline) -}}
  {{- $_ := set $values.promotion "pinKey" "pins.{stage}" -}}
{{- end -}}
{{- /* No verification is rendered, so no script is read; Files is there so a
       render that reached one still ends at the kinds guard. */ -}}
{{- $root := dict "Values" $values "Files" $.Files -}}
{{- include "cd-pipeline.checkPinFile" (dict "where" "pipeline.promotion.pinFile" "file" $values.promotion.pinFile) -}}
{{- $src := $p.source -}}
{{- $chart := dict "repoURL" $src.chart "semver" $src.semver -}}
{{- if hasKey $src "versionPrefix" -}}{{- $_ := set $chart "versionPrefix" $src.versionPrefix -}}{{- end -}}
{{- with $src.subscribeAs -}}{{- $_ := set $chart "subscribeAs" . -}}{{- end -}}
{{- $project := dict "name" $p.name "createProject" false "chart" $chart "stageOrder" $p.pipeline.stageOrder -}}
{{- with $p.pipeline.autoPromote -}}{{- $_ := set $project "autoPromote" . -}}{{- end -}}
{{- with $p.pipeline.upstreams -}}{{- $_ := set $project "upstreams" . -}}{{- end -}}
{{- $project = include "cd-pipeline.derive" (dict "root" $root "p" $project) | fromYaml -}}
{{- include "cd-pipeline.project" (dict "root" $root "p" $project) -}}
{{- end -}}

{{/*
A cluster's product facts with the project's chart source in them. The
source is the company's (`project.source`); the platform keeps which
repositories may be installed in its AppProjects' sourceRepos, so a product
that names a `repository` or `chart` of its own is refused. cd-delivery
installs `<repository>/<chart>` and, beside it, `<chart>-infra` and
`<chart>-e2e` from the same repository at the same pin, so `source.chart` (an
`oci://` reference) is split into the two, and `source.infraChart`, where
given, must be the infra chart that address yields. The kind of product is
the company's too (`source.kind`: `product`, the three rings, or `chart`, the
app ring alone) and is put in as `kind`; a product that sets one is refused,
and so is `infraChart` for a `chart` (checked once, in cd-project.render). A
static site is delivered by digest from a chart release of its own and has no
chart for the Warehouse to discover: refused.
As YAML for `fromYaml`. Call with (dict "cluster" .. "product" .. "source" ..).
*/}}
{{- define "cd-project.facts" -}}
{{- $c := .cluster -}}
{{- $src := .source -}}
{{- range $key := list "repository" "chart" -}}
  {{- if hasKey $.product $key -}}
    {{- fail (printf "cd-project: products.%s.%s: the chart source is the company's (project.source); the platform restricts sources through its AppProjects' sourceRepos" $c $key) -}}
  {{- end -}}
{{- end -}}
{{- if hasKey $.product "kind" -}}
  {{- fail (printf "cd-project: products.%s.kind: the kind of product is the company's (project.source.kind), like its charts" $c) -}}
{{- end -}}
{{- $kind := $src.kind | default "product" -}}
{{- if not (has $kind (list "product" "chart")) -}}
  {{- fail (printf "cd-project: project.source.kind %s: a project is a product or a chart (a static site is delivered by digest and has no chart for the project's Warehouse to discover)" $kind) -}}
{{- end -}}
{{- if not (hasPrefix "oci://" $src.chart) -}}
  {{- fail (printf "cd-project: project.source.chart must be an oci:// reference (<repository>/<chart>), got %q" $src.chart) -}}
{{- end -}}
{{- /* Split at the last `/` by hand: `dir` would clean the `//` of `oci://`. */ -}}
{{- $parts := splitList "/" $src.chart -}}
{{- $chart := last $parts -}}
{{- $repository := join "/" (initial $parts) -}}
{{- if or (not $chart) (eq $repository "oci:/") -}}
  {{- fail (printf "cd-project: project.source.chart must be oci://<repository>/<chart>, got %q" $src.chart) -}}
{{- end -}}
{{- with $src.infraChart -}}
  {{- if ne . (printf "%s-infra" $src.chart) -}}
    {{- fail (printf "cd-project: project.source.infraChart must be %s-infra (the infra ring installs <chart>-infra from the chart's repository at the same pin), got %q" $src.chart .) -}}
  {{- end -}}
{{- end -}}
{{- $facts := deepCopy $.product -}}
{{- $_ := set $facts "repository" $repository -}}
{{- $_ := set $facts "chart" $chart -}}
{{- $_ := set $facts "kind" $kind -}}
{{- toYaml $facts -}}
{{- end -}}

{{/* A map laid over another: maps merge key by key, every other value replaces. As YAML for `fromYaml`. Call with (dict "base" .. "over" ..). */}}
{{- define "cd-project.merge" -}}
{{- $out := deepCopy (.base | default dict) -}}
{{- range $k, $x := (.over | default dict) -}}
  {{- if and (kindIs "map" $x) (kindIs "map" (get $out $k)) -}}
    {{- $_ := set $out $k (include "cd-project.merge" (dict "base" (get $out $k) "over" $x) | fromYaml) -}}
  {{- else -}}
    {{- $_ := set $out $k $x -}}
  {{- end -}}
{{- end -}}
{{- toYaml $out -}}
{{- end -}}

{{/*
The whole render holds Argo CD Applications and Kargo Warehouses, Stages and
ProjectConfigs, and nothing else: the Project, credentials, verification and
access are the platform's. A guard on the output, whatever input reached it.
Call with the rendered documents.
*/}}
{{- define "cd-project.assertKinds" -}}
{{- $allowed := list "argoproj.io/v1alpha1/Application" "kargo.akuity.io/v1alpha1/Warehouse" "kargo.akuity.io/v1alpha1/Stage" "kargo.akuity.io/v1alpha1/ProjectConfig" -}}
{{- range $doc := regexSplit "(?m)^---[ \t]*$" . -1 -}}
  {{- $obj := fromYaml $doc -}}
  {{- if $obj -}}
    {{- $kind := printf "%v/%v" $obj.apiVersion $obj.kind -}}
    {{- if not (has $kind $allowed) -}}
      {{- fail (printf "cd-project: the render holds an object of kind %s; only %s are rendered here (the platform renders the Project, credentials, verification and access)" $kind (join ", " $allowed)) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end -}}
