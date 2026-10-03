{{/*
Replace the {token}s of a string. Call with (dict "s" <string> "tokens" <map>).
*/}}
{{- define "cd-pipeline.fill" -}}
{{- $s := .s -}}
{{- range $k, $v := .tokens -}}
{{- $s = replace (printf "{%s}" $k) (toString $v) $s -}}
{{- end -}}
{{- $s -}}
{{- end -}}

{{/*
The metadata of one object, as YAML for `fromYaml`: name, namespace when the
object has one, the sync-wave annotation, any further annotations and the
chart-wide labels. Call with (dict "root" . "name" .. "ns" .. "wave" .. "annotations" ..).
*/}}
{{- define "cd-pipeline.meta" -}}
{{- $meta := dict "name" .name -}}
{{- if .ns -}}{{- $_ := set $meta "namespace" .ns -}}{{- end -}}
{{- $ann := dict -}}
{{- if not (kindIs "invalid" .wave) -}}{{- $_ := set $ann "argocd.argoproj.io/sync-wave" (toString (int .wave)) -}}{{- end -}}
{{- range $k, $v := (.annotations | default dict) -}}{{- $_ := set $ann $k $v -}}{{- end -}}
{{- if $ann -}}{{- $_ := set $meta "annotations" $ann -}}{{- end -}}
{{- with .root.Values.labels -}}{{- $_ := set $meta "labels" . -}}{{- end -}}
{{- toYaml $meta -}}
{{- end -}}

{{/*
One document: `---` and the object. Call with the object (a map).
*/}}
{{- define "cd-pipeline.doc" -}}
{{- print "---\n" (toYaml .) "\n" -}}
{{- end -}}

{{/*
Kargo's `chartFrom` call for a chart. `vars` true reads the promotion's own
variables (chartRepo, and chartName where the chart has one); false spells the
repository and name out, as a Stage's verification arguments must.
Call with (dict "repoURL" .. "name" .. "vars" <bool>).
*/}}
{{- define "cd-pipeline.chartFrom" -}}
{{- if .vars -}}
{{- printf "chartFrom(vars.chartRepo%s)" (ternary ", vars.chartName" "" (ne .name "")) -}}
{{- else -}}
{{- printf "chartFrom(%q%s)" .repoURL (ternary (printf ", %q" .name) "" (ne .name "")) -}}
{{- end -}}
{{- end -}}

{{/*
The AnalysisTemplate of a Job check, as one document. The Job runs as the given
ServiceAccount under the `restricted` Pod Security profile and decides by its
exit code; the template is one measurement, run once, with no retry.
Call with (dict "root" . "name" .. "ns" .. "metric" .. "args" <names> "sa" ..
"image" .. "command" .. "env" .. "mounts" .. "volumes" .. "deadline" <seconds>
"ttl" <seconds, or nil for the default>).
*/}}
{{- define "cd-pipeline.jobCheck" -}}
{{- $r := .root -}}
{{- $jobs := $r.Values.jobs -}}
{{- $container := dict "name" "check" "securityContext" (dict "allowPrivilegeEscalation" false "capabilities" (dict "drop" (list "ALL"))) "image" .image "command" .command -}}
{{- if .env -}}{{- $_ := set $container "env" .env -}}{{- end -}}
{{- if .mounts -}}{{- $_ := set $container "volumeMounts" .mounts -}}{{- end -}}
{{- $podSpec := dict "serviceAccountName" .sa "restartPolicy" "Never" "securityContext" (dict "runAsNonRoot" true "runAsUser" $jobs.runAsUser "runAsGroup" $jobs.runAsGroup "seccompProfile" (dict "type" "RuntimeDefault")) "containers" (list $container) -}}
{{- if .volumes -}}{{- $_ := set $podSpec "volumes" .volumes -}}{{- end -}}
{{- $ttl := .ttl | default $jobs.ttlSecondsAfterFinished -}}
{{- $jobSpec := dict "backoffLimit" 0 "ttlSecondsAfterFinished" $ttl "activeDeadlineSeconds" .deadline "template" (dict "spec" $podSpec) -}}
{{- $metric := dict "name" .metric "count" 1 "failureLimit" 0 "provider" (dict "job" (dict "spec" $jobSpec)) -}}
{{- $args := list -}}
{{- range $a := .args -}}{{- $args = append $args (dict "name" $a) -}}{{- end -}}
{{- $at := dict "apiVersion" "argoproj.io/v1alpha1" "kind" "AnalysisTemplate" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" .name "ns" .ns "wave" $r.Values.waves.config) | fromYaml) "spec" (dict "args" $args "metrics" (list $metric)) -}}
{{- include "cd-pipeline.doc" $at -}}
{{- end -}}

{{/*
The script volume and mount of a check whose script is a ConfigMap.
*/}}
{{- define "cd-pipeline.scriptVolume" -}}
{{- toYaml (dict "name" "script" "configMap" (dict "name" .configMap "defaultMode" 365)) -}}
{{- end -}}
