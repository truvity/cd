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
- autoPromote: a Stage with no upstream, and every Stage `autoPromote` lists.
- applications: `applicationsByStage` for the Stage, else `applications`, else
  the project's slug, each through `graph.applicationName`.
- e2eApplication: on the Stages `e2eStages` lists, `e2eApplication` (default
  `<slug>-e2e`) through `graph.applicationName`.
- verification: the promoted-version check when `graph.promotedVersion`, the
  metrics gate on the Stages `metricsGateStages` lists.
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
{{- $chains := deepCopy ($g.upstreams | default dict) -}}
{{- range $st, $cands := ($p.upstreams | default dict) -}}{{- $_ := set $chains $st $cands -}}{{- end -}}
{{- $auto := $p.autoPromote | default list -}}
{{- $upstream := dict -}}
{{- $first := "" -}}
{{- range $st := $order -}}
{{- range $u := (index $chains $st | default list) -}}
{{- if and (has $u $order) (not (hasKey $upstream $st)) -}}{{- $_ := set $upstream $st $u -}}{{- end -}}
{{- end -}}
{{- if not (hasKey $upstream $st) -}}{{- $first = $st -}}{{- end -}}
{{- end -}}
{{- $last := last $order -}}
{{- $appName := $g.applicationName | default "{stage}-{app}" -}}
{{- $e2eApp := $p.e2eApplication | default (printf "%s-e2e" $slug) -}}
{{- $stages := list -}}
{{- range $st := $order -}}
{{- $apps := list -}}
{{- range $a := (index ($p.applicationsByStage | default dict) $st | default $p.applications | default (list $slug)) -}}
{{- $apps = append $apps (include "cd-pipeline.fill" (dict "s" $appName "tokens" (dict "stage" $st "app" $a))) -}}
{{- end -}}
{{- $s := dict "name" $st "applications" $apps -}}
{{- if hasKey $upstream $st -}}{{- $_ := set $s "from" (index $upstream $st) -}}{{- end -}}
{{- if or (not (hasKey $upstream $st)) (has $st $auto) -}}{{- $_ := set $s "autoPromote" true -}}{{- end -}}
{{- if has $st ($p.e2eStages | default list) -}}
{{- $_ := set $s "e2eApplication" (include "cd-pipeline.fill" (dict "s" $appName "tokens" (dict "stage" $st "app" $e2eApp))) -}}
{{- end -}}
{{- $verif := dict "promotedVersion" ($g.promotedVersion | default false) -}}
{{- if has $st ($p.metricsGateStages | default list) -}}{{- $_ := set $verif "metricsGate" true -}}{{- end -}}
{{- $_ := set $s "verification" $verif -}}
{{- $stages = append $stages $s -}}
{{- end -}}
{{- $acc := $p.access | default dict -}}
{{- $subjects := $acc.subjects | default list -}}
{{- if $acc.approvers -}}
{{- $ap := $g.approvers | default dict -}}
{{- $claims := $ap.claims -}}
{{- if not $claims -}}{{- fail "cd-pipeline: graph.approvers.claims is required by access.approvers" -}}{{- end -}}
{{- range $st := $order -}}
{{- if ne $st $first -}}
{{- $tokens := dict "stage" $st "slug" $slug "project" $name -}}
{{- $subjects = append $subjects (dict "name" (include "cd-pipeline.fill" (dict "s" ($ap.subject | default "approver-{stage}") "tokens" $tokens)) "claims" (include "cd-pipeline.fillClaims" (dict "claims" $claims "tokens" $tokens) | fromYaml) "stages" (list $first $st) "approve" (eq $st $last)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if $acc.admins -}}
{{- $ad := $g.admins | default dict -}}
{{- $claims := $ad.claims -}}
{{- if not $claims -}}{{- fail "cd-pipeline: graph.admins.claims is required by access.admins" -}}{{- end -}}
{{- $tokens := dict "slug" $slug "project" $name -}}
{{- $subjects = append $subjects (dict "name" ($ad.subject | default "admin") "claims" (include "cd-pipeline.fillClaims" (dict "claims" $claims "tokens" $tokens) | fromYaml) "stages" $order "approve" true) -}}
{{- end -}}
{{- $_ := set $p "access" (dict "subjects" $subjects) -}}
{{- $_ := set $p "stages" $stages -}}
{{- range $k := list "stageOrder" "upstreams" "autoPromote" "applications" "applicationsByStage" "e2eStages" "e2eApplication" "metricsGateStages" -}}
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
{{- $ep := required (printf "cd-pipeline: metricsGate.endpoints.%s is required by the metrics gate of projects.%s" $s.name $p.name) (index ($mg.endpoints | default dict) $s.name) -}}
{{- $tok := $mg.token -}}
{{- $ca := $mg.caBundle -}}
{{- $tokens := dict "stage" $s.name "slug" $slug "project" $p.name -}}
{{- $env := list (dict "name" "CHART_VERSION" "value" "{{args.chart-version}}") (dict "name" "PROJECT_NAME" "value" $slug) (dict "name" "ISSUER_HOST" "value" (required "cd-pipeline: metricsGate.issuerHost is required by a metrics gate" $mg.issuerHost)) (dict "name" "METRICS_BASE_URL" "value" (required (printf "cd-pipeline: metricsGate.endpoints.%s.url is required" $s.name) $ep.url)) (dict "name" "SA_TOKEN_FILE" "value" (printf "%s/token" $tok.mountPath)) -}}
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
