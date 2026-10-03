{{/*
One project: its Project, Warehouse, credentials, configuration, access and
Stages, as YAML documents. Call with (dict "root" . "p" <project map>).
*/}}
{{- define "cd-pipeline.project" -}}
{{- $r := .root -}}
{{- $v := $r.Values -}}
{{- $p := .p -}}
{{- $w := $v.waves -}}
{{- $name := $p.name -}}
{{- $slug := $p.slug | default $name -}}
{{- $chart := $p.chart | default dict -}}
{{- $repoURL := $chart.repoURL | default "" -}}
{{- $chartName := $chart.name | default "" -}}
{{- $wh := $p.warehouse | default dict -}}
{{- $whName := $wh.name | default $name -}}

{{- /* Project. */ -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "kargo.akuity.io/v1alpha1" "kind" "Project" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $name "wave" $w.project "annotations" $p.annotations) | fromYaml)) -}}

{{- /* Warehouse: one chart subscription unless the project lists its own. */ -}}
{{- $subs := $wh.subscriptions -}}
{{- if not $subs -}}
{{- $sub := dict "repoURL" (required (printf "cd-pipeline: projects.%s.chart.repoURL is required (or warehouse.subscriptions)" $name) $repoURL) "semverConstraint" (required (printf "cd-pipeline: projects.%s.chart.semver is required" $name) $chart.semver) "discoveryLimit" ($chart.discoveryLimit | default 20) -}}
{{- if $chartName -}}{{- $_ := set $sub "name" $chartName -}}{{- end -}}
{{- $subs = list (dict "chart" $sub) -}}
{{- end -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "kargo.akuity.io/v1alpha1" "kind" "Warehouse" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $whName "ns" $name "wave" $w.warehouse) | fromYaml) "spec" (dict "interval" ($wh.interval | default "5m0s") "freightCreationPolicy" ($wh.freightCreationPolicy | default "Automatic") "subscriptions" $subs)) -}}

{{- /* The credentials a promotion writes back with: one ExternalSecret per project namespace. */ -}}
{{- $cred := ($v.git).credentials -}}
{{- if $cred -}}
{{- $secret := $cred.secretName | default "kargo-git-writeback" -}}
{{- $props := required "cd-pipeline: git.credentials.properties is required" $cred.properties -}}
{{- $data := list -}}
{{- range $pair := (list (list "githubAppID" $props.appID) (list "githubInstallationID" $props.installationID) (list "githubPrivateKey" $props.privateKey)) -}}
{{- $data = append $data (dict "secretKey" (index $pair 0) "remoteRef" (dict "key" (required "cd-pipeline: git.credentials.remoteKey is required" $cred.remoteKey) "property" (index $pair 1) "conversionStrategy" "Default" "decodingStrategy" "None" "metadataPolicy" "None" "nullBytePolicy" "Ignore")) -}}
{{- end -}}
{{- $tmplData := dict "repoURL" (required "cd-pipeline: git.repoURL is required" $v.git.repoURL) "githubAppID" `{{ .githubAppID }}` "githubAppInstallationID" `{{ .githubInstallationID }}` "githubAppPrivateKey" `{{ .githubPrivateKey }}` -}}
{{- $es := dict "apiVersion" "external-secrets.io/v1" "kind" "ExternalSecret" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" "kargo-git-writeback" "ns" $name "wave" $w.credentials) | fromYaml) -}}
{{- $_ := set $es.metadata "name" $secret -}}
{{- $_ := set $es "spec" (dict "refreshInterval" ($cred.refreshInterval | default "1h") "secretStoreRef" (dict "kind" $cred.secretStoreRef.kind "name" $cred.secretStoreRef.name) "target" (dict "name" $secret "creationPolicy" "Owner" "template" (dict "metadata" (dict "labels" (dict "kargo.akuity.io/cred-type" "git")) "data" $tmplData)) "data" $data) -}}
{{- include "cd-pipeline.doc" $es -}}
{{- end -}}

{{- /* ProjectConfig: which Stages promote on their own. */ -}}
{{- $policies := list -}}
{{- range $s := $p.stages -}}
{{- if $s.autoPromote -}}{{- $policies = append $policies (dict "stage" $s.name "autoPromotionEnabled" true) -}}{{- end -}}
{{- end -}}
{{- if $policies -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "kargo.akuity.io/v1alpha1" "kind" "ProjectConfig" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $name "ns" $name "wave" $w.config) | fromYaml) "spec" (dict "promotionPolicies" $policies)) -}}
{{- end -}}

{{- /* Who may promote: a ServiceAccount, Role and RoleBinding per subject. Kargo maps a token to every ServiceAccount whose claims annotation matches it. */ -}}
{{- range $s := ($p.access).subjects -}}
{{- $sa := dict "apiVersion" "v1" "kind" "ServiceAccount" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $s.name "ns" $name "wave" $w.config "annotations" (dict "rbac.kargo.akuity.io/claims" (toJson $s.claims))) | fromYaml) -}}
{{- $rules := list (dict "apiGroups" (list "kargo.akuity.io") "resources" (list "freights" "stages" "warehouses" "projectconfigs" "promotions") "verbs" (list "get" "list" "watch")) (dict "apiGroups" (list "kargo.akuity.io") "resources" (list "stages") "resourceNames" $s.stages "verbs" (list "promote")) (dict "apiGroups" (list "kargo.akuity.io") "resources" (list "promotions") "verbs" (list "create")) -}}
{{- if $s.approve -}}{{- $rules = append $rules (dict "apiGroups" (list "kargo.akuity.io") "resources" (list "freights/status") "verbs" (list "patch")) -}}{{- end -}}
{{- include "cd-pipeline.doc" $sa -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "rbac.authorization.k8s.io/v1" "kind" "Role" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $s.name "ns" $name "wave" $w.config) | fromYaml) "rules" $rules) -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "rbac.authorization.k8s.io/v1" "kind" "RoleBinding" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $s.name "ns" $name "wave" $w.config) | fromYaml) "roleRef" (dict "apiGroup" "rbac.authorization.k8s.io" "kind" "Role" "name" $s.name) "subjects" (list (dict "kind" "ServiceAccount" "name" $s.name "namespace" $name))) -}}
{{- end -}}

{{- /* Read-only for everyone the viewer ServiceAccount covers. */ -}}
{{- with $v.viewer.serviceAccount -}}
{{- $vw := $v.viewer -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "rbac.authorization.k8s.io/v1" "kind" "RoleBinding" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" ($vw.bindingName | default "viewer") "ns" $name "wave" $w.config) | fromYaml) "roleRef" (dict "apiGroup" "rbac.authorization.k8s.io" "kind" "Role" "name" (required "cd-pipeline: viewer.role is required" $vw.role)) "subjects" (list (dict "kind" "ServiceAccount" "name" $vw.serviceAccount "namespace" (required "cd-pipeline: viewer.namespace is required" $vw.namespace)))) -}}
{{- end -}}

{{- /* The Stages, each with the checks it verifies by. */ -}}
{{- $stages := dict -}}
{{- range $s := $p.stages -}}{{- $_ := set $stages $s.name $s -}}{{- end -}}
{{- range $s := $p.stages -}}
{{- if and $s.from (not (hasKey $stages $s.from)) -}}{{- fail (printf "cd-pipeline: projects.%s.stages.%s.from names %q, which is not a stage of the project" $name $s.name $s.from) -}}{{- end -}}
{{- include "cd-pipeline.stage" (dict "root" $r "p" $p "s" $s "stages" $stages "slug" $slug "whName" $whName) -}}
{{- end -}}
{{- end -}}

{{/*
One Stage and the objects its verification needs.
Call with (dict "root" . "p" <project> "s" <stage> "stages" <name -> stage> "slug" .. "whName" ..).
*/}}
{{- define "cd-pipeline.stage" -}}
{{- $r := .root -}}
{{- $v := $r.Values -}}
{{- $p := .p -}}
{{- $s := .s -}}
{{- $w := $v.waves -}}
{{- $name := $p.name -}}
{{- $slug := .slug -}}
{{- $chart := $p.chart | default dict -}}
{{- $repoURL := $chart.repoURL | default "" -}}
{{- $chartName := $chart.name | default "" -}}
{{- $prefix := $chart.versionPrefix | default "" -}}
{{- $upstream := $s.from | default "" -}}
{{- $verArg := dict "name" "chart-version" "value" (printf "${{ %s.Version }}" (include "cd-pipeline.chartFrom" (dict "repoURL" $repoURL "name" $chartName "vars" false))) -}}
{{- $verif := $s.verification | default dict -}}
{{- $templates := list -}}

{{- /* The promoted-version check: that the Applications of this Stage render the promoted chart. */ -}}
{{- if $verif.promotedVersion -}}
{{- $pv := $v.promotedVersion -}}
{{- $apps := required (printf "cd-pipeline: projects.%s.stages.%s.applications is required for the promoted-version check" $name $s.name) $s.applications -}}
{{- $sa := printf "kargo-verify-%s" $s.name -}}
{{- $role := printf "kargo-verify-%s-%s" $name $s.name -}}
{{- $cm := printf "%s-%s-verify-promoted-version" $slug $s.name -}}
{{- $at := printf "%s-%s-promoted-version" $slug $s.name -}}
{{- $ns := $v.argocd.namespace -}}
{{- $templates = append $templates $at -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "v1" "kind" "ServiceAccount" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $sa "ns" $name "wave" $w.config) | fromYaml)) -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "rbac.authorization.k8s.io/v1" "kind" "Role" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $role "ns" $ns "wave" $w.config) | fromYaml) "rules" (list (dict "apiGroups" (list "argoproj.io") "resources" (list "applications") "resourceNames" $apps "verbs" (list "get")))) -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "rbac.authorization.k8s.io/v1" "kind" "RoleBinding" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $role "ns" $ns "wave" $w.config) | fromYaml) "roleRef" (dict "apiGroup" "rbac.authorization.k8s.io" "kind" "Role" "name" $role) "subjects" (list (dict "kind" "ServiceAccount" "name" $sa "namespace" $name))) -}}
{{- $script := $pv.script | default ($r.Files.Get "files/verify-promoted-version.sh") -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "v1" "kind" "ConfigMap" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $cm "ns" $name "wave" $w.config) | fromYaml) "data" (dict "verify-promoted-version.sh" $script)) -}}
{{- $env := list (dict "name" "APPS" "value" (join " " $apps)) (dict "name" "CHART_REPO" "value" $repoURL) (dict "name" "CHART_NAME" "value" $chartName) (dict "name" "EXPECTED" "value" (printf "%s%s" $prefix "{{args.chart-version}}")) (dict "name" "WAIT_SECONDS" "value" (toString (int $pv.waitSeconds))) (dict "name" "POLL_SECONDS" "value" (toString (int $pv.pollSeconds))) (dict "name" "JQ" "value" "/opt/jq/jq") -}}
{{- if ne $ns "argocd" -}}{{- $env = append $env (dict "name" "ARGOCD_NAMESPACE" "value" $ns) -}}{{- end -}}
{{- $mounts := list (dict "name" "script" "mountPath" "/scripts" "readOnly" true) (dict "name" "jq-tool" "mountPath" "/opt/jq" "readOnly" true) -}}
{{- $vols := list (include "cd-pipeline.scriptVolume" (dict "configMap" $cm) | fromYaml) (dict "name" "jq-tool" "image" (dict "reference" (required "cd-pipeline: promotedVersion.jqImage is required" $pv.jqImage) "pullPolicy" "IfNotPresent")) -}}
{{- include "cd-pipeline.jobCheck" (dict "root" $r "name" $at "ns" $name "metric" "promoted-version" "args" (list "chart-version") "sa" $sa "image" (required "cd-pipeline: promotedVersion.image is required" $pv.image) "command" (list "/bin/sh" "/scripts/verify-promoted-version.sh") "env" $env "mounts" $mounts "volumes" $vols "deadline" (add (int $pv.waitSeconds) $v.jobs.headroomSeconds)) -}}
{{- end -}}

{{- /* Job checks of the project's own. */ -}}
{{- range $c := $verif.checks -}}
{{- $templates = append $templates $c.name -}}
{{- if hasKey $c "serviceAccount" -}}
{{- if (hasKey $c "createServiceAccount" | ternary $c.createServiceAccount true) -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "v1" "kind" "ServiceAccount" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $c.serviceAccount "ns" $name "wave" $w.config) | fromYaml)) -}}
{{- end -}}
{{- end -}}
{{- $mounts := list -}}
{{- $vols := list -}}
{{- $command := $c.command -}}
{{- if $c.script -}}
{{- $file := $c.script.file | default "verify.sh" -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "v1" "kind" "ConfigMap" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $c.script.configMap "ns" $name "wave" $w.config) | fromYaml) "data" (dict $file $c.script.content)) -}}
{{- $mounts = append $mounts (dict "name" "script" "mountPath" "/scripts" "readOnly" true) -}}
{{- $vols = append $vols (include "cd-pipeline.scriptVolume" (dict "configMap" $c.script.configMap) | fromYaml) -}}
{{- if not $command -}}{{- $command = list "/bin/sh" (printf "/scripts/%s" $file) -}}{{- end -}}
{{- end -}}
{{- $mounts = concat $mounts ($c.volumeMounts | default list) -}}
{{- $vols = concat $vols ($c.volumes | default list) -}}
{{- include "cd-pipeline.jobCheck" (dict "root" $r "name" $c.name "ns" $name "metric" ($c.metric | default "check") "args" ($c.args | default (list "chart-version")) "sa" (required "cd-pipeline: a check needs a serviceAccount" $c.serviceAccount) "image" $c.image "command" (required "cd-pipeline: a check needs a command or a script" $command) "env" $c.env "mounts" $mounts "volumes" $vols "deadline" $c.deadlineSeconds "ttl" $c.ttlSecondsAfterFinished) -}}
{{- end -}}
{{- $templates = concat $templates ($verif.analysisTemplates | default list) -}}

{{- /* The Stage. */ -}}
{{- $sources := ternary (dict "stages" (list $upstream)) (dict "direct" true) (ne $upstream "") -}}
{{- $spec := dict "requestedFreight" (list (dict "origin" (dict "kind" "Warehouse" "name" .whName) "sources" $sources)) -}}
{{- if $templates -}}
{{- $_ := set $spec "verification" (dict "analysisTemplates" (include "cd-pipeline.names" $templates | fromYamlArray) "args" (list $verArg)) -}}
{{- end -}}
{{- if $s.promotionTemplate -}}
{{- $_ := set $spec "promotionTemplate" $s.promotionTemplate -}}
{{- else -}}
{{- $_ := set $spec "promotionTemplate" (dict "spec" (include "cd-pipeline.promotion" (dict "root" $r "p" $p "s" $s "stages" .stages "slug" $slug) | fromYaml)) -}}
{{- end -}}
{{- include "cd-pipeline.doc" (dict "apiVersion" "kargo.akuity.io/v1alpha1" "kind" "Stage" "metadata" (include "cd-pipeline.meta" (dict "root" $r "name" $s.name "ns" $name "wave" $w.stage) | fromYaml) "spec" $spec) -}}
{{- end -}}

{{- define "cd-pipeline.names" -}}
{{- $out := list -}}
{{- range $n := . -}}{{- $out = append $out (dict "name" $n) -}}{{- end -}}
{{- toYaml $out -}}
{{- end -}}
