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
{{- $installs := (include "cd-project.installs" . | fromYaml).installs -}}
{{- $keys := list -}}
{{- range $i := $installs -}}{{- $keys = append $keys $i.key -}}{{- end -}}
{{- range $kind := list "pins" "values" -}}
  {{- range $c := keys (index $v $kind) | sortAlpha -}}
    {{- if not (has $c $keys) -}}
      {{- if or $p.customers $p.companions -}}
        {{- fail (printf "cd-project: %s.%s: %q is not the Stage of a customer of project.customers on a cluster of project.clusters, or such a key with -<companion> after it (%s)" $kind $c $c (join ", " $keys)) -}}
      {{- end -}}
      {{- fail (printf "cd-project: %s.%s: %q is not a cluster of project.clusters" $kind $c $c) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- /* The platform's range (pipeline.project.semver): every Warehouse
       subscribes within it (cd-project.withPlatformRange), and a pin the
       company writes by hand outside it is refused, so a version the
       platform cannot install never reaches a cluster. */ -}}
{{- with dig "project" "semver" "" $v.pipeline -}}
  {{- $plat := . -}}
  {{- $_ := semverCompare $plat "0.0.0" -}}
  {{- range $i := $installs -}}
    {{- $pin := index $v.pins $i.key | default "" -}}
    {{- if and (not $i.companion) $pin -}}
      {{- $range := include "cd-project.withPlatformRange" (dict "own" $i.source.semver "platform" $plat) -}}
      {{- if not (semverCompare $range (trimPrefix ($i.source.versionPrefix | default "") $pin)) -}}
        {{- fail (printf "cd-project: pins.%s: %s is outside the range the Warehouse subscribes with, %q (the company's %q within the platform's %q, pipeline.project.semver)" $i.key $pin $range $i.source.semver $plat) -}}
      {{- end -}}
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
{{- range $s := ($p.pipeline.noAutoPromote | default list) -}}
  {{- if not (has $s $order) -}}
    {{- fail (printf "cd-project: project.pipeline.noAutoPromote: %q is not a Stage of project.pipeline.stageOrder" $s) -}}
  {{- end -}}
{{- end -}}
{{- range $s := (dig "project" "noAutoPromote" list $v.pipeline) -}}
  {{- if not (has $s $order) -}}
    {{- fail (printf "cd-project: pipeline.project.noAutoPromote: %q is not a Stage of project.pipeline.stageOrder" $s) -}}
  {{- end -}}
{{- end -}}
{{- range $s := (dig "project" "metricsGateStages" list $v.pipeline) -}}
  {{- if not (has $s $order) -}}
    {{- fail (printf "cd-project: pipeline.project.metricsGateStages: %q is not a Stage of project.pipeline.stageOrder" $s) -}}
  {{- end -}}
{{- end -}}
{{- range $s := keys ($p.pipeline.upstreams | default dict) | sortAlpha -}}
  {{- if not (has $s $order) -}}
    {{- fail (printf "cd-project: project.pipeline.upstreams: %q is not a Stage of project.pipeline.stageOrder" $s) -}}
  {{- end -}}
{{- end -}}
{{- $sources := $p.sources | default dict -}}
{{- $chartName := last (splitList "/" $p.source.chart) -}}
{{- range $sn := keys $sources | sortAlpha -}}
  {{- $sc := (index $sources $sn).chart -}}
  {{- if not (hasPrefix "oci://" $sc) -}}
    {{- fail (printf "cd-project: project.sources.%s.chart must be an oci:// reference (<repository>/<chart>), got %q" $sn $sc) -}}
  {{- end -}}
  {{- if ne (last (splitList "/" $sc)) $chartName -}}
    {{- fail (printf "cd-project: project.sources.%s.chart must be the same chart as project.source.chart (%s) from another repository, got %q" $sn $chartName $sc) -}}
  {{- end -}}
{{- end -}}
{{- $stageSources := $p.pipeline.stageSources | default dict -}}
{{- range $s := keys $stageSources | sortAlpha -}}
  {{- if not (has $s $order) -}}
    {{- fail (printf "cd-project: project.pipeline.stageSources: %q is not a Stage of project.pipeline.stageOrder" $s) -}}
  {{- end -}}
  {{- if not (hasKey $sources (index $stageSources $s)) -}}
    {{- fail (printf "cd-project: project.pipeline.stageSources.%s: %q is not a key of project.sources" $s (index $stageSources $s)) -}}
  {{- end -}}
{{- end -}}
{{- $out := "" -}}
{{- range $c := $p.clusters -}}
  {{- $products := dict -}}
  {{- $pins := dict -}}
  {{- $values := dict -}}
  {{- $services := list -}}
  {{- $at := dict -}}
  {{- range $i := $installs -}}
    {{- if eq $i.cluster $c -}}
      {{- $services = append $services $i.service -}}
      {{- $_ := set $at $i.service $i.key -}}
      {{- if hasKey $v.products $i.key -}}
        {{- $facts := dict -}}
        {{- if $i.companion -}}
          {{- $facts = include "cd-project.companionFacts" (dict "key" $i.key "product" (index $v.products $i.key) "companion" (index $p.companions $i.companion) "namespace" $i.namespace) | fromYaml -}}
        {{- else -}}
          {{- $facts = include "cd-project.facts" (dict "cluster" $i.key "product" (index $v.products $i.key) "source" $i.source) | fromYaml -}}
        {{- end -}}
        {{- /* A customer's install: its own {customer}, and the project's
               name (with the companion's) in the middle of its Application
               names. */ -}}
        {{- with $i.customer -}}
          {{- $_ := set $facts "customer" . -}}
          {{- $_ := set $facts "applicationName" (ternary (printf "%s-%s" $name $i.companion) $name (not (empty $i.companion))) -}}
        {{- end -}}
        {{- $_ := set $products $i.service $facts -}}
      {{- end -}}
      {{- /* A cluster with no pin is dormant: its pin file is absent until
             Kargo writes the first version, so a new project carries no
             `pins: {<cluster>: ""}` by hand. */ -}}
      {{- $_ := set $pins $i.service (get $v.pins $i.key | default "") -}}
      {{- if hasKey $v.values $i.key -}}{{- $_ := set $values $i.service (index $v.values $i.key) -}}{{- end -}}
    {{- end -}}
  {{- end -}}
  {{- $out = print $out (include "cd-company.render" (dict "chart" "cd-project" "at" $at "platform" (index $v.platform $c) "products" $products "services" $services "pins" $pins "values" $values)) "\n" -}}
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
`pipeline.project` is not a cd-pipeline setting: it is what the platform says
about this project's Kargo Project (cd-project.kargoProject), and is taken
out before the rest is laid over the defaults.
Call with the chart's root context.
*/}}
{{- define "cd-project.pipeline" -}}
{{- $v := .Values -}}
{{- $p := $v.project -}}
{{- $values := include "cd-project.merge" (dict "base" (index $v "cd-pipeline") "over" (omit $v.pipeline "project")) | fromYaml -}}
{{- if not (dig "promotion" "pinKey" "" $v.pipeline) -}}
  {{- /* With customers a pin is keyed by its Stage's name, as the install
         reads it: pins.<cluster>-<customer> by default. */ -}}
  {{- $_ := set $values.promotion "pinKey" (ternary (printf "pins.%s" (dig "graph" "stageName" "{stage}-{customer}" $v.pipeline)) "pins.{stage}" (not (empty $p.customers))) -}}
{{- end -}}
{{- /* The promoted-version check, when the platform's graph turns it on, is
       only named by the Stages: its objects are the platform's (it renders
       them where the check runs), never this render's. No script is read;
       Files is there so a render that reached one still ends at the kinds
       guard. */ -}}
{{- $_ := set $values.promotedVersion "objects" false -}}
{{- /* So is the metrics gate's, on the Stages the platform's sealed
       pipeline.project.metricsGateStages lists: each names
       `<slug>-<stage>-metrics-gate`, and the gate's ServiceAccount,
       ConfigMap and AnalysisTemplate are the platform's. */ -}}
{{- $_ := set $values.metricsGate "objects" false -}}
{{- $root := dict "Values" $values "Files" $.Files -}}
{{- include "cd-pipeline.checkPinFile" (dict "where" "pipeline.promotion.pinFile" "file" $values.promotion.pinFile) -}}
{{- $src := $p.source -}}
{{- $plat := dig "project" "semver" "" $v.pipeline -}}
{{- $chart := dict "repoURL" $src.chart "semver" (include "cd-project.withPlatformRange" (dict "own" $src.semver "platform" $plat)) -}}
{{- if hasKey $src "versionPrefix" -}}{{- $_ := set $chart "versionPrefix" $src.versionPrefix -}}{{- end -}}
{{- with $src.subscribeAs -}}{{- $_ := set $chart "subscribeAs" . -}}{{- end -}}
{{- $project := dict "name" $p.name "createProject" false "chart" $chart "stageOrder" $p.pipeline.stageOrder -}}
{{- $project = merge (include "cd-project.kargoProject" . | fromYaml) $project -}}
{{- with $p.pipeline.autoPromote -}}{{- $_ := set $project "autoPromote" . -}}{{- end -}}
{{- /* Either side may hold a Stage back: the company in its own file, the
       platform in what it seals (to quiesce a pipeline it is moving). */ -}}
{{- with concat ($p.pipeline.noAutoPromote | default list) (dig "project" "noAutoPromote" list $v.pipeline) | uniq -}}{{- $_ := set $project "noAutoPromote" . -}}{{- end -}}
{{- with $p.pipeline.upstreams -}}{{- $_ := set $project "upstreams" . -}}{{- end -}}
{{- with dig "project" "metricsGateStages" list $v.pipeline -}}{{- $_ := set $project "metricsGateStages" . -}}{{- end -}}
{{- with $p.sources -}}
  {{- $sources := dict -}}
  {{- range $sn, $sv := . -}}
    {{- $sc := dict "repoURL" $sv.chart "semver" (include "cd-project.withPlatformRange" (dict "own" $sv.semver "platform" $plat)) -}}
    {{- if hasKey $sv "versionPrefix" -}}{{- $_ := set $sc "versionPrefix" $sv.versionPrefix -}}{{- end -}}
    {{- with $sv.subscribeAs -}}{{- $_ := set $sc "subscribeAs" . -}}{{- end -}}
    {{- $_ := set $sources $sn $sc -}}
  {{- end -}}
  {{- $_ := set $project "sources" $sources -}}
{{- end -}}
{{- with $p.pipeline.stageSources -}}{{- $_ := set $project "stageSources" . -}}{{- end -}}
{{- /* The companions move with the chart in one Freight: each one an image
       subscription pinned by digest, its pin `<chart's pin key>-<name>`, and
       its Application (`<project>-<name>`) watched beside the project's. */ -}}
{{- with $p.companions -}}
  {{- $companions := dict -}}
  {{- $apps := list $p.name -}}
  {{- range $cn := keys . | sortAlpha -}}
    {{- $co := index $p.companions $cn -}}
    {{- $_ := set $companions $cn (dict "repoURL" (trimPrefix "oci://" $co.artifact) "semver" $co.semver "subscribeAs" "image" "pin" "digest") -}}
    {{- $apps = append $apps (printf "%s-%s" $p.name $cn) -}}
  {{- end -}}
  {{- $_ := set $project "companions" $companions -}}
  {{- $_ := set $project "applications" $apps -}}
{{- end -}}
{{- with (include "cd-project.installs" . | fromYaml).customers -}}
  {{- $customers := list -}}
  {{- range $c := . -}}
    {{- $cc := dict "repoURL" $c.source.chart "semver" (include "cd-project.withPlatformRange" (dict "own" $c.source.semver "platform" $plat)) -}}
    {{- if hasKey $c.source "versionPrefix" -}}{{- $_ := set $cc "versionPrefix" $c.source.versionPrefix -}}{{- end -}}
    {{- with $c.source.subscribeAs -}}{{- $_ := set $cc "subscribeAs" . -}}{{- end -}}
    {{- $entry := dict "name" $c.name "chart" $cc -}}
    {{- with $c.companions -}}{{- $_ := set $entry "companions" . -}}{{- end -}}
    {{- $customers = append $customers $entry -}}
  {{- end -}}
  {{- $_ := set $project "customers" $customers -}}
{{- end -}}
{{- $project = include "cd-pipeline.derive" (dict "root" $root "p" $project) | fromYaml -}}
{{- include "cd-pipeline.project" (dict "root" $root "p" $project) -}}
{{- end -}}

{{/*
The project's installs, as YAML for `fromYaml`: `installs`, one for each
cluster of project.clusters, or, with project.customers, one for each
customer on each cluster; and `customers`, each with its chart source.

- key: what pins, values and products are keyed by: the cluster, or the
  Stage of the customer's chain on that cluster (pipeline.graph.stageName,
  default `{stage}-{customer}`, with {stage} the cluster).
- service: cd-company's service, so the Helm release, its objects and the
  namespace unless the platform's products name one: the project, or
  `<project>-<customer>`.
- source: the chart source the install takes every ring from
  (cd-project.clusterSource, or the customer's chart laid over
  project.source).

The customers are the platform's (pipeline.project.customers, sealed by
platformDigest): the company names the ones it installs and may give each
one a chart of its own; a product with several customers has one Kargo
Project, so `pipeline.project.customer` is not set beside them, and the
sources of project.sources are not used beside them (each customer is a
source of its own). Call with the chart's root context.
*/}}
{{- define "cd-project.installs" -}}
{{- $v := .Values -}}
{{- $p := $v.project -}}
{{- $registered := dig "project" "customers" list ($v.pipeline | default dict) -}}
{{- $installs := list -}}
{{- $customers := list -}}
{{- if and $registered (dig "project" "customer" "" ($v.pipeline | default dict)) -}}
  {{- fail "cd-project: pipeline.project sets customer and customers: a product with several customers has one Kargo Project, and each Stage has its own {customer}" -}}
{{- end -}}
{{- if and $registered (not $p.customers) -}}
  {{- fail (printf "cd-project: pipeline.project.customers registers %s: project.customers names the ones the company installs" (join ", " $registered)) -}}
{{- end -}}
{{- if and $p.customers (not $registered) -}}
  {{- fail "cd-project: project.customers needs pipeline.project.customers: the platform registers a product's customers" -}}
{{- end -}}
{{- if and $p.customers (or $p.sources $p.pipeline.stageSources) -}}
  {{- fail "cd-project: project.customers is set with project.sources or project.pipeline.stageSources: each customer is a source of its own" -}}
{{- end -}}
{{- $stageName := dig "graph" "stageName" "{stage}-{customer}" ($v.pipeline | default dict) -}}
{{- $seen := dict -}}
{{- range $entry := ($p.customers | default list) -}}
  {{- $c := ternary (dict "name" $entry) $entry (kindIs "string" $entry) -}}
  {{- if not (has $c.name $registered) -}}
    {{- fail (printf "cd-project: project.customers: %q is not a customer the platform registered for this product (pipeline.project.customers: %s)" $c.name (join ", " $registered)) -}}
  {{- end -}}
  {{- if hasKey $seen $c.name -}}
    {{- fail (printf "cd-project: project.customers: %q is listed twice" $c.name) -}}
  {{- end -}}
  {{- $_ := set $seen $c.name true -}}
  {{- $src := deepCopy $p.source -}}
  {{- with $c.chart -}}
    {{- if not (hasPrefix "oci://" .) -}}
      {{- fail (printf "cd-project: project.customers.%s.chart must be an oci:// reference (<repository>/<chart>), got %q" $c.name .) -}}
    {{- end -}}
    {{- $_ := set $src "chart" . -}}
    {{- if hasKey $src "infraChart" -}}{{- $_ := set $src "infraChart" (printf "%s-infra" .) -}}{{- end -}}
  {{- end -}}
  {{- with $c.semver -}}{{- $_ := set $src "semver" . -}}{{- end -}}
  {{- range $cn := keys ($c.companions | default dict) | sortAlpha -}}
    {{- if not (hasKey ($p.companions | default dict) $cn) -}}
      {{- fail (printf "cd-project: project.customers.%s.companions: %q is not a companion of project.companions" $c.name $cn) -}}
    {{- end -}}
  {{- end -}}
  {{- $cust := dict "name" $c.name "source" $src -}}
  {{- with $c.companions -}}{{- $_ := set $cust "companions" . -}}{{- end -}}
  {{- $customers = append $customers $cust -}}
  {{- range $cl := $p.clusters -}}
    {{- $key := include "cd-pipeline.fill" (dict "s" $stageName "tokens" (dict "stage" $cl "customer" $c.name)) -}}
    {{- if not (regexMatch "^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$" $key) -}}
      {{- fail (printf "cd-project: pipeline.graph.stageName %q gives %q for customer %s on %s, which is not a Stage name (lower-case letters, digits and -, at most 63; tokens {stage} and {customer})" $stageName $key $c.name $cl) -}}
    {{- end -}}
    {{- range $i := $installs -}}
      {{- if eq $i.key $key -}}
        {{- fail (printf "cd-project: pipeline.graph.stageName %q gives %q twice: it names each customer's Stage on each cluster apart" $stageName $key) -}}
      {{- end -}}
    {{- end -}}
    {{- $installs = append $installs (dict "key" $key "cluster" $cl "customer" $c.name "service" (printf "%s-%s" $p.name $c.name) "source" $src) -}}
  {{- end -}}
{{- else -}}
  {{- range $cl := $p.clusters -}}
    {{- $installs = append $installs (dict "key" $cl "cluster" $cl "service" $p.name "source" (include "cd-project.clusterSource" (dict "project" $p "cluster" $cl) | fromYaml)) -}}
  {{- end -}}
{{- end -}}
{{- /* Each companion beside each install: `<key>-<companion>`, the service
       `<service>-<companion>`, in the install's namespace. */ -}}
{{- $parts := list -}}
{{- range $i := $installs -}}
  {{- range $cn := keys ($p.companions | default dict) | sortAlpha -}}
    {{- $part := dict "key" (printf "%s-%s" $i.key $cn) "cluster" $i.cluster "service" (printf "%s-%s" $i.service $cn) "companion" $cn "namespace" $i.service -}}
    {{- with $i.customer -}}{{- $_ := set $part "customer" . -}}{{- end -}}
    {{- $parts = append $parts $part -}}
  {{- end -}}
{{- end -}}
{{- $all := concat $installs $parts -}}
{{- $seenKeys := dict -}}
{{- range $i := $all -}}
  {{- if hasKey $seenKeys $i.key -}}
    {{- fail (printf "cd-project: %q is the key of two installs (a cluster or Stage and a companion beside another): name the companion or the cluster apart" $i.key) -}}
  {{- end -}}
  {{- $_ := set $seenKeys $i.key true -}}
{{- end -}}
{{- toYaml (dict "installs" $all "customers" $customers) -}}
{{- end -}}

{{/*
A companion's product facts on one install: the platform's
(`products.<key>-<companion>`: chartVersion, platformValues, a namespace of
its own) with the company's artifact in them and the kind static-site. The
artifact is the company's (project.companions.<name>.artifact), like the
chart source, so a product that names one, or a kind, is refused. The
namespace is the install's unless the platform names another.
As YAML for `fromYaml`. Call with (dict "key" .. "product" .. "companion" .. "namespace" ..).
*/}}
{{- define "cd-project.companionFacts" -}}
{{- range $k := list "artifact" "kind" -}}
  {{- if hasKey $.product $k -}}
    {{- fail (printf "cd-project: products.%s.%s: a companion's artifact and kind are the company's (project.companions)" $.key $k) -}}
  {{- end -}}
{{- end -}}
{{- $facts := deepCopy .product -}}
{{- $_ := set $facts "kind" "static-site" -}}
{{- $_ := set $facts "artifact" (dict "ref" (trimPrefix "oci://" .companion.artifact)) -}}
{{- if not (hasKey $facts "namespace") -}}{{- $_ := set $facts "namespace" .namespace -}}{{- end -}}
{{- toYaml $facts -}}
{{- end -}}

{{/*
A company's semver range within the platform's (`pipeline.project.semver`):
both must hold, as one constraint. AND is a space; an OR (`||`) on either
side is distributed, so `a || b` within `c` is `a c || b c`. The company's
range alone where the platform gives none.
Call with (dict "own" <the company's range> "platform" <the platform's, or "">).
*/}}
{{- define "cd-project.withPlatformRange" -}}
{{- if not .platform -}}
{{- .own -}}
{{- else -}}
{{- $out := list -}}
{{- range $a := splitList "||" .own -}}
  {{- range $b := splitList "||" $.platform -}}
    {{- $out = append $out (printf "%s %s" (trim $a) (trim $b)) -}}
  {{- end -}}
{{- end -}}
{{- join " || " $out -}}
{{- end -}}
{{- end -}}

{{/*
What the platform says about the project's Kargo Project (`pipeline.project`),
as the keys of cd-pipeline's project map, in YAML for `fromYaml`; an empty map
when the platform says nothing, and the Project is then named after the
project, as it always was.

- name: the Kargo Project, and so the namespace of the Warehouse, Stages and
  ProjectConfig: `pipeline.project.name` with {project} and {product}
  (project.name), {vendor} and {customer} filled. With it the project's own
  name becomes cd-pipeline's `slug`, which the Applications a Stage watches,
  the pin key and the check names are made from, so none of those move. The
  Warehouse keeps the project's own name too.
- vendor, customer: the tokens of the same names in cd-pipeline's templates.

The name is the platform's alone: `pipeline` is sealed by platformDigest and
the schema gives a company's `project` no key for it.
Call with the chart's root context.
*/}}
{{- define "cd-project.kargoProject" -}}
{{- $p := .Values.project -}}
{{- $k := .Values.pipeline.project | default dict -}}
{{- $out := dict -}}
{{- range $key := list "vendor" "customer" -}}
  {{- with index $k $key -}}{{- $_ := set $out $key . -}}{{- end -}}
{{- end -}}
{{- with $k.name -}}
  {{- $name := include "cd-pipeline.fill" (dict "s" . "tokens" (merge (dict "project" $p.name "product" $p.name) $out)) -}}
  {{- if not (regexMatch "^[a-z0-9]([-a-z0-9]{0,61}[a-z0-9])?$" $name) -}}
    {{- fail (printf "cd-project: pipeline.project.name %q gives %q, which is not a namespace name (lower-case letters, digits and -, at most 63; tokens {project}, {product}, and {vendor} and {customer} where pipeline.project sets them)" . $name) -}}
  {{- end -}}
  {{- $_ := set $out "name" $name -}}
  {{- $_ := set $out "slug" $p.name -}}
  {{- /* The Warehouse keeps the project's own name: the Project is renamed, the product is not. */ -}}
  {{- $_ := set $out "warehouse" (dict "name" $p.name) -}}
{{- end -}}
{{- toYaml $out -}}
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

{{/*
The chart source a cluster installs from: project.source, or, for a cluster
whose Stage pipeline.stageSources names, that source laid over it (`kind` stays
project.source's; `infraChart`, where given, moves to the source's
repository). As YAML for `fromYaml`. Call with (dict "project" .. "cluster" ..).
*/}}
{{- define "cd-project.clusterSource" -}}
{{- $p := .project -}}
{{- $src := deepCopy $p.source -}}
{{- with index ($p.pipeline.stageSources | default dict) .cluster -}}
  {{- $sv := index $p.sources . -}}
  {{- $_ := set $src "chart" $sv.chart -}}
  {{- $_ := set $src "semver" $sv.semver -}}
  {{- $_ := unset $src "versionPrefix" -}}
  {{- $_ := unset $src "subscribeAs" -}}
  {{- if hasKey $sv "versionPrefix" -}}{{- $_ := set $src "versionPrefix" $sv.versionPrefix -}}{{- end -}}
  {{- with $sv.subscribeAs -}}{{- $_ := set $src "subscribeAs" . -}}{{- end -}}
  {{- if hasKey $src "infraChart" -}}{{- $_ := set $src "infraChart" (printf "%s-infra" $sv.chart) -}}{{- end -}}
{{- end -}}
{{- toYaml $src -}}
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
