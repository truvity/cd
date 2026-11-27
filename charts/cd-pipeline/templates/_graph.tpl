{{/*
A project whose Stages are derived from `stageOrder` and the promotion graph
(`graph`), as the project map `cd-pipeline.project` renders, in YAML for
`fromYaml`. A project with no `stageOrder` is returned unchanged by the caller
and never comes here. Call with (dict "root" . "p" <project map>).

The rules, for each Stage of `stageOrder`, in that order:

- upstream: the first of its candidates (the project's `upstreams` entry for
  the Stage, else `graph.upstreams`') that is also a Stage of the project. A
  Stage with no such candidate takes Freight straight from the Warehouse.
- first: the last Stage, in order, that has no upstream. Every approver may
  promote it.
- autoPromote: a Stage with no upstream, and every Stage `autoPromote` lists;
  never a Stage `noAutoPromote` lists, which is marked `noAutoPromote` instead.
- noPrereleases: every Stage `noPrereleases` lists: its promotion refuses a
  version with a prerelease part.
- applications: `applicationsByStage` for the Stage, else `applications`, else
  the project's slug, each through `graph.applicationName` ({stage}, {app},
  and the project's tokens: cd-pipeline.projectTokens).
- e2eApplication: on the Stages `e2eStages` lists, `e2eApplication` (default
  `<slug>-e2e`) through `graph.applicationName`.
- verification: the promoted-version check when `graph.promotedVersion`, the
  metrics gate on the Stages `metricsGateStages` lists.
- source: `stageSources` for the Stage (a key of `sources`), else none (the
  project's `chart`); with customers, the customer's own source, or its
  source of that name (`<customer>-<source>`). A Stage and its upstream take
  the same source. When the
  project names any source, a Stage with no upstream that is not the first of
  `stageOrder` promotes by pull request, like a hop: it is a later cluster fed
  by a Warehouse of its own, not the project's first.
- components: with `components`, each customer's chains are one for each
  component it takes, on that component's Warehouse (or, for a lane
  customer, its Warehouse of the lane's source, one Stage on the first of
  `stageOrder`); `{component}` beside `{customer}` in every name.
- access: with `access.approvers`, one subject per Stage but the first, named
  and claimed by `graph.approvers` ({stage}, {slug}), that may promote the first
  Stage and its own and, on the last Stage, approve Freight; with
  `access.admins`, one subject (`graph.admins`) that promotes every Stage and
  approves. Subjects listed in `access.subjects` come first, verbatim.
*/}}
{{- define "cd-pipeline.derive" -}}
{{- $v := .root.Values -}}
{{- $g := $v.graph | default dict -}}
{{- $p := deepCopy .p -}}
{{- $name := $p.name -}}
{{- $slug := $p.slug | default $name -}}
{{- $order := $p.stageOrder -}}
{{- if $p.stages -}}
{{- fail (printf "cd-pipeline: projects.%s sets both stages and stageOrder; a project spells its Stages out or derives them, not both" $name) -}}
{{- end -}}
{{- /* CUSTOMERS: one Kargo Project for a product with several customers.
       Each customer gets a Warehouse of its own, `<warehouse>-<customer>` (a
       source named after it, on the project's chart unless the customer
       names one), and its own chain of Stages, one for each Stage of
       stageOrder, named by graph.stageName (default `{stage}-{customer}`).
       A customer's Stages request only its Warehouse and promote only from
       its own Stages; the first of each chain promotes on its own. In the
       names of its Applications and in its pins, `{stage}` stays the Stage
       of stageOrder (the cluster) and `{customer}` is the customer. */ -}}
{{- $customers := list -}}
{{- range $c := ($p.customers | default list) -}}
{{- $customers = append $customers (ternary (dict "name" $c) $c (kindIs "string" $c)) -}}
{{- end -}}
{{- /* COMPONENTS: one Kargo Project for a product made of several parts, each
       its own chart, for several customers. Each component has one Warehouse,
       `<warehouse>-<component>`, shared by every customer that takes it, and
       one of each source it names, `<warehouse>-<component>-<source>`. Each
       customer takes the components it lists, a chain of Stages for each,
       named by graph.stageName (default `{stage}-{customer}-{component}`). A
       lane customer (customers[].lane) is a chain of one Stage, the first of
       stageOrder, on its components' Warehouses of the lane's source; it
       never promotes onward. */ -}}
{{- $components := $p.components | default dict -}}
{{- $chainsOfComponents := list -}}
{{- if $components -}}
{{- if not $customers -}}{{- fail (printf "cd-pipeline: projects.%s.components needs customers: each customer names the components it takes" $name) -}}{{- end -}}
{{- range $k := list "chart" "sources" "stageSources" "companions" -}}
{{- if index $p $k -}}{{- fail (printf "cd-pipeline: projects.%s sets components and %s: each component names its own chart (components.<component>.chart) and its sources (components.<component>.sources)" $name $k) -}}{{- end -}}
{{- end -}}
{{- $srcs := dict -}}
{{- range $cn := keys $components | sortAlpha -}}
{{- $co := index $components $cn | default dict -}}
{{- if hasKey $srcs $cn -}}{{- fail (printf "cd-pipeline: projects.%s.components: %q is also another component's Warehouse of a source (<component>-<source>); name the component or the source apart" $name $cn) -}}{{- end -}}
{{- $_ := set $srcs $cn (required (printf "cd-pipeline: projects.%s.components.%s.chart is required" $name $cn) $co.chart) -}}
{{- range $sn := keys ($co.sources | default dict) | sortAlpha -}}
{{- $key := printf "%s-%s" $cn $sn -}}
{{- if or (hasKey $srcs $key) (hasKey $components $key) -}}{{- fail (printf "cd-pipeline: projects.%s: component %q's Warehouse of source %q is named %q, as another component's is; name the component or the source apart" $name $cn $sn $key) -}}{{- end -}}
{{- $_ := set $srcs $key (index $co.sources $sn) -}}
{{- end -}}
{{- end -}}
{{- $seen := dict -}}
{{- range $c := $customers -}}
{{- if hasKey $seen $c.name -}}{{- fail (printf "cd-pipeline: projects.%s.customers: %q is listed twice" $name $c.name) -}}{{- end -}}
{{- $_ := set $seen $c.name true -}}
{{- range $k := list "chart" "sources" "companions" -}}
{{- if index $c $k -}}{{- fail (printf "cd-pipeline: projects.%s.customers.%s.%s: with components a customer takes each component's chart (components.<component>)" $name $c.name $k) -}}{{- end -}}
{{- end -}}
{{- $cc := required (printf "cd-pipeline: projects.%s.customers.%s.components is required: with components each customer names the ones it takes" $name $c.name) $c.components -}}
{{- if ne (len $cc) (len (uniq $cc)) -}}{{- fail (printf "cd-pipeline: projects.%s.customers.%s.components lists a component twice" $name $c.name) -}}{{- end -}}
{{- $laneSource := "" -}}
{{- $laneStage := "" -}}
{{- with $c.lane -}}
{{- $laneSource = required (printf "cd-pipeline: projects.%s.customers.%s.lane.source is required: the source whose Warehouses the lane takes" $name $c.name) .source -}}
{{- $laneStage = .stage | default (first $order) -}}
{{- if ne $laneStage (first $order) -}}{{- fail (printf "cd-pipeline: projects.%s.customers.%s.lane.stage: %q is not the first Stage of stageOrder (%s); a lane is a preview on the first Stage alone and never promotes onward" $name $c.name $laneStage (first $order)) -}}{{- end -}}
{{- end -}}
{{- range $cn := $cc | sortAlpha -}}
{{- if not (hasKey $components $cn) -}}{{- fail (printf "cd-pipeline: projects.%s.customers.%s.components: %q is not a component of the project (components: %s)" $name $c.name $cn (keys $components | sortAlpha | join ", ")) -}}{{- end -}}
{{- $chain := dict "name" $c.name "component" $cn -}}
{{- with $laneSource -}}
{{- if not (hasKey ((index $components $cn).sources | default dict) .) -}}{{- fail (printf "cd-pipeline: projects.%s.customers.%s.lane.source: %q is not a source of component %s (components.%s.sources)" $name $c.name . $cn $cn) -}}{{- end -}}
{{- $_ := set $chain "source" (printf "%s-%s" $cn .) -}}
{{- $_ := set $chain "stages" (list $laneStage) -}}
{{- else -}}
{{- $_ := set $chain "source" $cn -}}
{{- end -}}
{{- $chainsOfComponents = append $chainsOfComponents $chain -}}
{{- end -}}
{{- end -}}
{{- $_ := set $p "sources" $srcs -}}
{{- end -}}
{{- /* The sources the project names: with customers, `sources` is replaced
       below by one source per customer, and one per customer and named
       source. */ -}}
{{- $named := $p.sources | default dict -}}
{{- if $components -}}{{- $named = dict -}}{{- end -}}
{{- if and $customers (not $components) -}}
{{- $srcs := dict -}}
{{- $seen := dict -}}
{{- range $c := $customers -}}
{{- if hasKey $seen $c.name -}}{{- fail (printf "cd-pipeline: projects.%s.customers: %q is listed twice" $name $c.name) -}}{{- end -}}
{{- $_ := set $seen $c.name true -}}
{{- if hasKey $srcs $c.name -}}{{- fail (printf "cd-pipeline: projects.%s.customers: %q is also another customer's Warehouse of a source (<customer>-<source>); name the customer or the source apart" $name $c.name) -}}{{- end -}}
{{- $_ := set $srcs $c.name ($c.chart | default $p.chart | default dict) -}}
{{- /* A customer's own semver for a companion, in its Warehouse alone. */ -}}
{{- with $c.companions -}}
{{- $own := include "cd-pipeline.companionsFrom" (dict "companions" ($p.companions | default dict) "source" "") | fromYaml -}}
{{- range $cn, $co := . -}}
{{- if not (hasKey $own $cn) -}}{{- fail (printf "cd-pipeline: projects.%s.customers.%s.companions: %q is not a companion of the project" $name $c.name $cn) -}}{{- end -}}
{{- $_ := set (index $own $cn) "semver" $co.semver -}}
{{- end -}}
{{- $sc := $p.sourceCompanions | default dict -}}
{{- $_ := set $sc $c.name $own -}}
{{- $_ := set $p "sourceCompanions" $sc -}}
{{- end -}}
{{- /* Each named source, for this customer: a Warehouse
       `<warehouse>-<customer>-<source>` on the customer's chart from that
       source (customers[].sources.<source>, else the project's
       sources.<source>), with every companion from that source
       (companions.<name>.sources.<source>, else the companion as it is). */ -}}
{{- range $sn := keys ($c.sources | default dict) | sortAlpha -}}
{{- if not (hasKey $named $sn) -}}{{- fail (printf "cd-pipeline: projects.%s.customers.%s.sources: %q is not a key of sources" $name $c.name $sn) -}}{{- end -}}
{{- end -}}
{{- range $sn := keys $named | sortAlpha -}}
{{- $key := printf "%s-%s" $c.name $sn -}}
{{- if or (hasKey $srcs $key) (hasKey $seen $key) -}}{{- fail (printf "cd-pipeline: projects.%s: customer %q's Warehouse of source %q is named %q, as another customer's is; name the customer or the source apart" $name $c.name $sn $key) -}}{{- end -}}
{{- $_ := set $srcs $key (index ($c.sources | default dict) $sn | default (index $named $sn)) -}}
{{- with $p.companions -}}
{{- $sc := $p.sourceCompanions | default dict -}}
{{- $_ := set $sc $key (include "cd-pipeline.companionsFrom" (dict "companions" . "source" $sn) | fromYaml) -}}
{{- $_ := set $p "sourceCompanions" $sc -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $_ := set $p "sources" $srcs -}}
{{- $_ := set $p "namedSources" (keys $named | sortAlpha) -}}
{{- end -}}
{{- $chains := deepCopy ($g.upstreams | default dict) -}}
{{- range $st, $cands := ($p.upstreams | default dict) -}}{{- $_ := set $chains $st $cands -}}{{- end -}}
{{- $auto := $p.autoPromote | default list -}}
{{- $noAuto := $p.noAutoPromote | default list -}}
{{- range $st := $noAuto -}}
{{- if not (has $st $order) -}}
{{- fail (printf "cd-pipeline: projects.%s.noAutoPromote: %q is not a Stage of stageOrder" $name $st) -}}
{{- end -}}
{{- end -}}
{{- $noPre := $p.noPrereleases | default list -}}
{{- range $st := $noPre -}}
{{- if not (has $st $order) -}}
{{- fail (printf "cd-pipeline: projects.%s.noPrereleases: %q is not a Stage of stageOrder" $name $st) -}}
{{- end -}}
{{- end -}}
{{- $upstream := dict -}}
{{- $first := "" -}}
{{- range $st := $order -}}
{{- range $u := (index $chains $st | default list) -}}
{{- if and (has $u $order) (not (hasKey $upstream $st)) -}}{{- $_ := set $upstream $st $u -}}{{- end -}}
{{- end -}}
{{- if not (hasKey $upstream $st) -}}{{- $first = $st -}}{{- end -}}
{{- end -}}
{{- $last := last $order -}}
{{- $srcOf := $p.stageSources | default dict -}}
{{- $sources := $p.sources | default dict -}}
{{- range $st := keys $srcOf | sortAlpha -}}
{{- if not (has $st $order) -}}
{{- fail (printf "cd-pipeline: projects.%s.stageSources: %q is not a Stage of stageOrder" $name $st) -}}
{{- end -}}
{{- if not (hasKey $named (index $srcOf $st)) -}}
{{- fail (printf "cd-pipeline: projects.%s.stageSources.%s: %q is not a key of sources" $name $st (index $srcOf $st)) -}}
{{- end -}}
{{- end -}}
{{- range $st := $order -}}
{{- if hasKey $upstream $st -}}
{{- $u := index $upstream $st -}}
{{- if ne (index $srcOf $st | default "") (index $srcOf $u | default "") -}}
{{- fail (printf "cd-pipeline: projects.%s: Stage %q takes source %q but its upstream %q takes %q; a Stage promotes only Freight of its own source (give it no upstream to take it straight from its Warehouse)" $name $st (index $srcOf $st | default "chart") $u (index $srcOf $u | default "chart")) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $appName := $g.applicationName | default "{stage}-{app}" -}}
{{- include "cd-pipeline.checkTokens" (dict "p" $p "where" "graph.applicationName" "s" $appName) -}}
{{- $projectTokens := include "cd-pipeline.projectTokens" $p | fromYaml -}}
{{- $e2eApp := $p.e2eApplication | default (printf "%s-e2e" $slug) -}}
{{- $stages := list -}}
{{- $stageName := $g.stageName | default (ternary "{stage}-{customer}-{component}" "{stage}-{customer}" (not (empty $components))) -}}
{{- if $components -}}
{{- range $t := list "{customer}" "{component}" -}}
{{- if not (contains $t $stageName) -}}{{- fail (printf "cd-pipeline: projects.%s: graph.stageName %q has no %s, so the Stages of a project with components would not be told apart" $name $stageName $t) -}}{{- end -}}
{{- end -}}
{{- end -}}
{{- $chainsOf := ternary $customers (list (dict)) (gt (len $customers) 0) -}}
{{- if $components -}}{{- $chainsOf = $chainsOfComponents -}}{{- end -}}
{{- $firsts := list -}}
{{- $stageNames := dict -}}
{{- range $c := $chainsOf -}}
{{- $ctok := $projectTokens -}}
{{- if $c.name -}}{{- $ctok = merge (dict "customer" $c.name) $projectTokens -}}{{- end -}}
{{- /* The tokens of a chain's Stage names: its customer, and its component. */ -}}
{{- $ntok := dict "customer" $c.name -}}
{{- with $c.component -}}{{- $_ := set $ntok "component" . -}}{{- $ctok = merge (dict "component" .) $ctok -}}{{- end -}}
{{- range $st := ($c.stages | default $order) -}}
{{- $apps := list -}}
{{- range $a := (index ($p.applicationsByStage | default dict) $st | default $p.applications | default (list $slug)) -}}
{{- $apps = append $apps (include "cd-pipeline.fill" (dict "s" $appName "tokens" (merge (dict "stage" $st "app" $a) $ctok))) -}}
{{- end -}}
{{- $sname := $st -}}
{{- if $c.name -}}{{- $sname = include "cd-pipeline.fill" (dict "s" $stageName "tokens" (merge (dict "stage" $st) $ntok)) -}}{{- end -}}
{{- if $c.component -}}
{{- if hasKey $stageNames $sname -}}{{- fail (printf "cd-pipeline: projects.%s: graph.stageName %q gives %q twice; name the customers and components apart" $name $stageName $sname) -}}{{- end -}}
{{- $_ := set $stageNames $sname true -}}
{{- end -}}
{{- $s := dict "name" $sname "applications" $apps -}}
{{- if $c.name -}}{{- $_ := set $s "env" $st -}}{{- $_ := set $s "customer" $c.name -}}{{- end -}}
{{- with $c.component -}}{{- $_ := set $s "component" . -}}{{- end -}}
{{- if and (hasKey $upstream $st) (not $c.stages) -}}
{{- $u := index $upstream $st -}}
{{- if $c.name -}}{{- $u = include "cd-pipeline.fill" (dict "s" $stageName "tokens" (merge (dict "stage" $u) $ntok)) -}}{{- end -}}
{{- $_ := set $s "from" $u -}}
{{- else -}}
{{- $firsts = append $firsts $sname -}}
{{- end -}}
{{- /* The opt-out wins over both rules. */ -}}
{{- if has $st $noAuto -}}{{- $_ := set $s "noAutoPromote" true -}}
{{- else if or (not (hasKey $upstream $st)) (has $st $auto) -}}{{- $_ := set $s "autoPromote" true -}}{{- end -}}
{{- if has $st $noPre -}}{{- $_ := set $s "noPrereleases" true -}}{{- end -}}
{{- $stChart := $p.chart | default dict -}}
{{- /* A customer's Stage takes its customer's Warehouse, or, where
       stageSources names a source, its customer's Warehouse of that source. */ -}}
{{- $src := index $srcOf $st | default "" -}}
{{- if $c.component -}}{{- $src = $c.source -}}
{{- else if $c.name -}}{{- $src = ternary (printf "%s-%s" $c.name $src) $c.name (ne $src "") -}}{{- end -}}
{{- with $src -}}
{{- $_ := set $s "source" . -}}
{{- $stChart = index $sources . -}}
{{- end -}}
{{- if and $named (not (hasKey $upstream $st)) (ne $st (first $order)) -}}{{- $_ := set $s "mode" "pr" -}}{{- end -}}
{{- if has $st ($p.e2eStages | default list) -}}
{{- $_ := set $s "e2eApplication" (include "cd-pipeline.fill" (dict "s" $appName "tokens" (merge (dict "stage" $st "app" $e2eApp) $ctok))) -}}
{{- end -}}
{{- /* A digest pin has no chart version for the check to compare: the Stage omits it. */ -}}
{{- $verif := dict "promotedVersion" (and ($g.promotedVersion | default false) (ne ($stChart.pin | default "tag") "digest")) -}}
{{- if has $st ($p.metricsGateStages | default list) -}}{{- $_ := set $verif "metricsGate" true -}}{{- end -}}
{{- $_ := set $s "verification" $verif -}}
{{- $stages = append $stages $s -}}
{{- end -}}
{{- end -}}
{{- $acc := $p.access | default dict -}}
{{- $subjects := $acc.subjects | default list -}}
{{- if $acc.approvers -}}
{{- $ap := $g.approvers | default dict -}}
{{- $claims := $ap.claims -}}
{{- if not $claims -}}{{- fail "cd-pipeline: graph.approvers.claims is required by access.approvers" -}}{{- end -}}
{{- range $c := $chainsOf -}}
{{- $ntok := dict "customer" $c.name -}}
{{- with $c.component -}}{{- $_ := set $ntok "component" . -}}{{- end -}}
{{- $cfirst := $first -}}
{{- if $c.name -}}{{- $cfirst = include "cd-pipeline.fill" (dict "s" $stageName "tokens" (merge (dict "stage" $first) $ntok)) -}}{{- end -}}
{{- /* A lane's one Stage is its first: no approver but the admins'. */ -}}
{{- range $st := ($c.stages | default $order) -}}
{{- if ne $st $first -}}
{{- $tokens := dict "stage" $st "slug" $slug "project" $name -}}
{{- $sname := $st -}}
{{- /* One approver for each customer's Stage: a subject name that does not
       spell the customer takes it as a suffix, and with components the
       component after it. */ -}}
{{- $subj := $ap.subject | default "approver-{stage}" -}}
{{- if $c.name -}}
{{- if not (contains "{customer}" $subj) -}}{{- $subj = printf "%s-{customer}" $subj -}}{{- end -}}
{{- $_ := set $tokens "customer" $c.name -}}
{{- with $c.component -}}
{{- if not (contains "{component}" $subj) -}}{{- $subj = printf "%s-{component}" $subj -}}{{- end -}}
{{- $_ := set $tokens "component" . -}}
{{- end -}}
{{- $sname = include "cd-pipeline.fill" (dict "s" $stageName "tokens" (merge (dict "stage" $st) $ntok)) -}}
{{- end -}}
{{- $subjects = append $subjects (dict "name" (include "cd-pipeline.fill" (dict "s" $subj "tokens" $tokens)) "claims" (include "cd-pipeline.fillClaims" (dict "claims" $claims "tokens" $tokens) | fromYaml) "stages" (list $cfirst $sname) "approve" (eq $st $last)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $allNames := list -}}
{{- range $s := $stages -}}{{- $allNames = append $allNames $s.name -}}{{- end -}}
{{- if $acc.admins -}}
{{- $ad := $g.admins | default dict -}}
{{- $claims := $ad.claims -}}
{{- if not $claims -}}{{- fail "cd-pipeline: graph.admins.claims is required by access.admins" -}}{{- end -}}
{{- $tokens := dict "slug" $slug "project" $name -}}
{{- $subjects = append $subjects (dict "name" ($ad.subject | default "admin") "claims" (include "cd-pipeline.fillClaims" (dict "claims" $claims "tokens" $tokens) | fromYaml) "stages" $allNames "approve" true) -}}
{{- end -}}
{{- $_ := set $p "access" (dict "subjects" $subjects) -}}
{{- $_ := set $p "stages" $stages -}}
{{- if $customers -}}{{- $_ := set $p "customerWarehousesOnly" true -}}{{- end -}}
{{- if $components -}}{{- $_ := set $p "componentChains" true -}}{{- end -}}
{{- range $k := list "stageOrder" "upstreams" "autoPromote" "noAutoPromote" "noPrereleases" "stageSources" "applications" "applicationsByStage" "e2eStages" "e2eApplication" "metricsGateStages" "customers" "components" -}}
{{- $_ := unset $p $k -}}
{{- end -}}
{{- toYaml $p -}}
{{- end -}}

{{/*
A claims map with its tokens filled: every string, and every string of a list,
through cd-pipeline.fill. As YAML for `fromYaml`.
Call with (dict "claims" <map> "tokens" <map>).
*/}}
{{- define "cd-pipeline.fillClaims" -}}
{{- $out := dict -}}
{{- $tokens := .tokens -}}
{{- range $k, $v := .claims -}}
{{- if kindIs "slice" $v -}}
{{- $l := list -}}
{{- range $x := $v -}}{{- $l = append $l (ternary (include "cd-pipeline.fill" (dict "s" (toString $x) "tokens" $tokens)) $x (kindIs "string" $x)) -}}{{- end -}}
{{- $_ := set $out $k $l -}}
{{- else if kindIs "string" $v -}}
{{- $_ := set $out $k (include "cd-pipeline.fill" (dict "s" $v "tokens" $tokens)) -}}
{{- else -}}
{{- $_ := set $out $k $v -}}
{{- end -}}
{{- end -}}
{{- toYaml $out -}}
{{- end -}}

{{/*
The metrics gate of a Stage, as one `verification.checks` entry: a Job that
waits until the promoted version is the only one serving, then bakes on a
sample count of prober journeys read from a Prometheus-compatible endpoint
(files/verify-metrics-gate.sh, or the promotion-gate image's `metrics-gate`
with `metricsGate.gateImage`). As YAML for `fromYaml`.
Call with (dict "root" . "p" <project> "s" <stage> "slug" ..).
*/}}
{{- define "cd-pipeline.metricsGateCheck" -}}
{{- $r := .root -}}
{{- $v := $r.Values -}}
{{- $mg := $v.metricsGate -}}
{{- $p := .p -}}
{{- $s := .s -}}
{{- $slug := .slug -}}
{{- $pg := $p.metricsGate | default dict -}}
{{- $ep := required (printf "cd-pipeline: metricsGate.endpoints.%s is required by the metrics gate of projects.%s" ($s.env | default $s.name) $p.name) (index ($mg.endpoints | default dict) ($s.env | default $s.name)) -}}
{{- $tok := $mg.token -}}
{{- $ca := $mg.caBundle -}}
{{- $tokens := dict "stage" $s.name "slug" $slug "project" $p.name -}}
{{- $env := list (dict "name" "CHART_VERSION" "value" "{{args.chart-version}}") (dict "name" "PROJECT_NAME" "value" $slug) (dict "name" "ISSUER_HOST" "value" (required "cd-pipeline: metricsGate.issuerHost is required by a metrics gate" $mg.issuerHost)) (dict "name" "METRICS_BASE_URL" "value" (required (printf "cd-pipeline: metricsGate.endpoints.%s.url is required" ($s.env | default $s.name)) $ep.url)) (dict "name" "SA_TOKEN_FILE" "value" (printf "%s/token" $tok.mountPath)) -}}
{{- if $ep.caBundle -}}
{{- $env = append $env (dict "name" "CA_BUNDLE" "value" (printf "%s/%s" $ca.mountPath $ca.key)) -}}
{{- end -}}
{{- $env = concat $env (list (dict "name" "WORKLOAD_NAMESPACE" "value" ($pg.namespace | default $slug)) (dict "name" "WORKLOAD_RELEASE" "value" ($pg.release | default $slug)) (dict "name" "PHASE1_SECONDS" "value" (toString $mg.phase1Seconds)) (dict "name" "BAKE_SECONDS" "value" (toString $mg.bakeSeconds)) (dict "name" "SCRAPE_LAG_SECONDS" "value" (toString $mg.scrapeLagSeconds)) (dict "name" "POLL_SECONDS" "value" (toString $mg.pollSeconds)) (dict "name" "MIN_JOURNEYS" "value" (toString $mg.minJourneys)) (dict "name" "MAX_FAILURE_RATIO" "value" (toString $mg.maxFailureRatio)) (dict "name" "CHECK_FIRING_ALERTS" "value" (toString $mg.checkFiringAlerts)) (dict "name" "PROBER_JOURNEY_METRIC" "value" (toString $mg.proberJourneyMetric)) (dict "name" "E2E_JOB_REQUIRED" "value" (toString (ternary $pg.e2eJobRequired true (hasKey $pg "e2eJobRequired"))))) -}}
{{- $mounts := list (dict "name" $tok.volumeName "mountPath" $tok.mountPath "readOnly" true) -}}
{{- $vols := list (dict "name" $tok.volumeName "projected" (dict "sources" (list (dict "serviceAccountToken" (dict "audience" (required "cd-pipeline: metricsGate.token.audience is required by a metrics gate" $tok.audience) "expirationSeconds" $tok.expirationSeconds "path" "token"))))) -}}
{{- if $ep.caBundle -}}
{{- $mounts = append $mounts (dict "name" $ca.volumeName "mountPath" $ca.mountPath "readOnly" true) -}}
{{- $vols = append $vols (dict "name" $ca.volumeName "configMap" (dict "name" (required "cd-pipeline: metricsGate.caBundle.configMap is required by an endpoint with caBundle" $ca.configMap))) -}}
{{- end -}}
{{- $check := dict "name" (printf "%s-%s-metrics-gate" $slug $s.name) "metric" "metrics-gate" "serviceAccount" (include "cd-pipeline.fill" (dict "s" $mg.serviceAccount "tokens" $tokens)) -}}
{{- if $mg.gateImage -}}
{{- $_ := set $check "image" $mg.gateImage -}}
{{- $_ := set $check "command" (list "/ko-app/promotion-gate" "metrics-gate") -}}
{{- else -}}
{{- $pv := $v.promotedVersion -}}
{{- $_ := set $check "image" (required "cd-pipeline: metricsGate.image (or promotedVersion.image) is required by a metrics gate" ($mg.image | default $pv.image)) -}}
{{- $_ := set $check "script" (dict "configMap" (printf "%s-%s-verify-metrics-gate" $slug $s.name) "file" "verify-metrics-gate.sh" "content" ($r.Files.Get "files/verify-metrics-gate.sh")) -}}
{{- $env = append $env (dict "name" "JQ" "value" "/opt/jq/jq") -}}
{{- $mounts = append $mounts (dict "name" "jq-tool" "mountPath" "/opt/jq" "readOnly" true) -}}
{{- $vols = append $vols (dict "name" "jq-tool" "image" (dict "reference" (required "cd-pipeline: metricsGate.jqImage (or promotedVersion.jqImage) is required by a metrics gate" ($mg.jqImage | default $pv.jqImage)) "pullPolicy" "IfNotPresent")) -}}
{{- end -}}
{{- $_ := set $check "env" $env -}}
{{- $_ := set $check "volumeMounts" $mounts -}}
{{- $_ := set $check "volumes" $vols -}}
{{- $_ := set $check "deadlineSeconds" (add (int $mg.phase1Seconds) (int $mg.bakeSeconds) (int $v.jobs.headroomSeconds)) -}}
{{- toYaml $check -}}
{{- end -}}
