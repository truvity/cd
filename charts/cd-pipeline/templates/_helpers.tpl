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
object has one, the sync-wave annotation, the chart-wide annotations, any further
annotations of the object and the chart-wide labels. Call with (dict "root" . "name" .. "ns" .. "wave" .. "annotations" ..).
*/}}
{{- define "cd-pipeline.meta" -}}
{{- $meta := dict "name" .name -}}
{{- if .ns -}}{{- $_ := set $meta "namespace" .ns -}}{{- end -}}
{{- $ann := dict -}}
{{- if not (kindIs "invalid" .wave) -}}{{- $_ := set $ann "argocd.argoproj.io/sync-wave" (toString (int .wave)) -}}{{- end -}}
{{- range $k, $v := (.root.Values.annotations | default dict) -}}{{- $_ := set $ann $k (toString $v) -}}{{- end -}}
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
Call with (dict "repoURL" .. "name" .. "vars" <bool> "image" <bool>); with `image` the call is `imageFrom` and `repoURL` is the image repository.
*/}}
{{- define "cd-pipeline.chartFrom" -}}
{{- if .image -}}
{{- /* An image subscription (chart.subscribeAs: image): Kargo's `imageFrom`, whose Tag is the discovered tag as it is. */ -}}
{{- if .vars -}}{{- print "imageFrom(vars.chartRepo)" -}}{{- else -}}{{- printf "imageFrom(%q)" .repoURL -}}{{- end -}}
{{- else if .vars -}}
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

{{/*
The chart's subscription mode and the repository the promotion names. Returns
YAML for `fromYaml`: image (bool), digest (bool: the pin is the artifact's
digest, not its tag), strict (the subscription's `strictSemvers`) and repo (the
image repository for an image subscription, with any `oci://` dropped; the
chart's repoURL otherwise).
Call with (dict "name" <project> "chart" <chart map>).
*/}}
{{- define "cd-pipeline.chartMode" -}}
{{- $c := .chart -}}
{{- $image := eq ($c.subscribeAs | default "chart") "image" -}}
{{- $digest := eq ($c.pin | default "tag") "digest" -}}
{{- if and $image (or $c.name $c.versionPrefix) -}}{{- fail (printf "cd-pipeline: projects.%s.chart.subscribeAs image takes neither name nor versionPrefix (the tag is written as discovered)" .name) -}}{{- end -}}
{{- if and $digest (not $image) -}}{{- fail (printf "cd-pipeline: projects.%s.chart.pin digest needs subscribeAs image (only an image subscription discovers a digest)" .name) -}}{{- end -}}
{{- if and (hasKey $c "strictSemvers") (not $image) -}}{{- fail (printf "cd-pipeline: projects.%s.chart.strictSemvers needs subscribeAs image" .name) -}}{{- end -}}
{{- toYaml (dict "image" $image "digest" $digest "strict" ($c.strictSemvers | default false) "repo" (ternary (trimPrefix "oci://" ($c.repoURL | default "")) ($c.repoURL | default "") $image)) -}}
{{- end -}}

{{/*
What a project promotes with: the release's `git` and `promotion` with the
project's own laid over them, as YAML for `fromYaml` (`git`, `promotion`).
A project's key replaces the release's, except that the `retry` and `gate`
maps of `promotion` merge key by key; `git.credentials: false` removes the
credentials for that project's namespace. Refuses a project `pinFile` that
leaves the repository; a missing `git.repoURL` is refused where it is used.
Call with (dict "root" . "p" <project map>).
*/}}
{{- define "cd-pipeline.effective" -}}
{{- $v := .root.Values -}}
{{- $p := .p -}}
{{- $git := deepCopy ($v.git | default dict) -}}
{{- range $k, $x := ($p.git | default dict) -}}
{{- if and (eq $k "credentials") (kindIs "bool" $x) -}}
{{- $_ := unset $git "credentials" -}}
{{- else -}}
{{- $_ := set $git $k $x -}}
{{- end -}}
{{- end -}}
{{- $pr := deepCopy $v.promotion -}}
{{- range $k, $x := ($p.promotion | default dict) -}}
{{- if and (kindIs "map" $x) (kindIs "map" (get $pr $k)) -}}
{{- $cur := get $pr $k -}}
{{- range $k2, $x2 := $x -}}{{- $_ := set $cur $k2 $x2 -}}{{- end -}}
{{- else -}}
{{- $_ := set $pr $k $x -}}
{{- end -}}
{{- end -}}
{{- if hasKey ($p.promotion | default dict) "pinFile" -}}
{{- include "cd-pipeline.checkPinFile" (dict "where" (printf "projects.%s: promotion.pinFile" $p.name) "file" $pr.pinFile) -}}
{{- end -}}
{{- toYaml (dict "git" $git "promotion" $pr) -}}
{{- end -}}

{{/*
A pin file is written inside the repository a promotion clones: it is not
absolute and does not climb out with `..`. Call with (dict "where" .. "file" ..).
*/}}
{{- define "cd-pipeline.checkPinFile" -}}
{{- if or (hasPrefix "/" .file) (contains ".." .file) -}}
{{- fail (printf "cd-pipeline: %s must be a path inside the repository (not absolute, no ..), got %q" .where .file) -}}
{{- end -}}
{{- end -}}
