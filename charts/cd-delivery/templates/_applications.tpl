{{/*
The Applications of every product, as YAML documents.

This is a named template, not just the body of templates/applications.yaml,
on purpose: an installation that already composes its products' pins and
facts in its own templates (a pin lives in a file another tool writes, and
Helm cannot move a value from one chart's values to another's) calls it with
that composition, and the Applications are rendered by this one place either
way.

Call with (dict "platform" <platform map> "products" <products map>). Helm
validates only the chart's own values against values.schema.json, so a caller
that passes its own dict gets the same refusal of an unknown key from
`cd-delivery.assertKeys`, and the same `required`/`fail` checks below.
*/}}
{{- define "cd-delivery.applications" -}}
{{- $plat := .platform | default dict -}}
{{- $products := .products | default dict -}}
{{- $docs := list -}}
{{- if $products -}}
{{- include "cd-delivery.assertKeys" (dict "what" "platform" "got" $plat "allowed" (list "tier" "finalizer" "sync" "syncRetry" "clusterName" "argocdNamespace" "applicationPrefix" "appProject" "applicationLabels" "waves" "chartRegistry" "cloud" "postgres" "events" "database" "identity" "telemetry" "e2e")) -}}
{{- range $name := (keys $products | sortAlpha) -}}
{{- $p := get $products $name -}}
{{- include "cd-delivery.assertKeys" (dict "what" (printf "products.%s" $name) "got" $p "allowed" (list "pin" "interface" "repository" "hostname" "parentRef" "surfaces" "bucketSlug" "workloadIdentity" "natsIdentity" "mtls" "postgres" "e2e" "faro" "alerts" "identityProviders" "access" "product" "values" "ignoreDifferences")) -}}
{{- $docs = append $docs (include "cd-delivery.product" (dict "plat" $plat "name" $name "p" $p)) -}}
{{- end -}}
{{- end -}}
{{- join "\n" $docs -}}
{{- end -}}

{{/*
One product's Applications: the infrastructure ring, the application ring,
and the end-to-end ring where the product has one and its charts publish it.
Call with (dict "plat" <platform> "name" <product> "p" <product map>).
*/}}
{{- define "cd-delivery.product" -}}
{{- $plat := .plat -}}
{{- $name := .name -}}
{{- $p := .p -}}
{{- $i := int (include "cd-delivery.interface" (dict "name" $name "p" $p)) -}}
{{- $ctx := dict "plat" $plat "name" $name "p" $p "i" $i -}}
{{- $docs := list -}}
{{- $docs = append $docs (include "cd-delivery.application" (dict "ctx" $ctx "ring" "infra" "values" (include "cd-delivery.infraValues" $ctx | fromYaml))) -}}
{{- $docs = append $docs (include "cd-delivery.application" (dict "ctx" $ctx "ring" "app" "values" (include "cd-delivery.appValues" $ctx | fromYaml))) -}}
{{- /* The end-to-end chart exists from interface 3. A product whose pin is
       below it simply has none to install: the platform's fact says where an
       end-to-end run is wanted, the interface says whether this pin ships it. */ -}}
{{- if and (($p.e2e | default dict).enabled) (ge $i 3) -}}
{{- $docs = append $docs (include "cd-delivery.application" (dict "ctx" $ctx "ring" "e2e" "values" (include "cd-delivery.e2eValues" $ctx | fromYaml))) -}}
{{- end -}}
{{- join "\n" $docs -}}
{{- end -}}

{{/*
One Argo CD Application: the skeleton every ring shares, around the values the
ring composed. Call with (dict "ctx" <product ctx> "ring" infra|app|e2e
"values" <map>).
*/}}
{{- define "cd-delivery.application" -}}
{{- $c := .ctx -}}
{{- $plat := $c.plat -}}
{{- $name := $c.name -}}
{{- $p := $c.p -}}
{{- $ring := .ring -}}
{{- $suffix := get (dict "infra" "-infra" "app" "" "e2e" "-e2e") $ring -}}
{{- $cluster := required "cd-delivery: platform.clusterName is required" $plat.clusterName -}}
{{- $waves := required "cd-delivery: platform.waves is required" $plat.waves -}}
{{- $wave := required (printf "cd-delivery: platform.waves.%s is required" $ring) (get $waves $ring) -}}
{{- $labels := dict -}}
{{- range $k, $v := ($plat.applicationLabels | default dict) -}}
{{- $_ := set $labels $k (include "cd-delivery.fill" (dict "s" $v "product" $name "cluster" $cluster)) -}}
{{- end -}}
{{- if not (hasKey $plat "applicationPrefix") -}}{{- fail "cd-delivery: platform.applicationPrefix is required (it may be empty)" -}}{{- end -}}
{{- $meta := dict "name" (printf "%s%s%s" (toString $plat.applicationPrefix) $name $suffix) "namespace" (required "cd-delivery: platform.argocdNamespace is required" $plat.argocdNamespace) "annotations" (dict "argocd.argoproj.io/sync-wave" (toString (int $wave))) -}}
{{- if $labels -}}{{- $_ := set $meta "labels" $labels -}}{{- end -}}
{{- /* Removing an Application removes what it deployed, unless the platform
       turns the finalizer off. */ -}}
{{- if (hasKey $plat "finalizer" | ternary $plat.finalizer true) -}}{{- $_ := set $meta "finalizers" (list "resources-finalizer.argocd.argoproj.io") -}}{{- end -}}
{{- $sync := $plat.sync | default dict -}}
{{- $prune := hasKey $sync "prune" | ternary $sync.prune true -}}
{{- $self := hasKey $sync "selfHeal" | ternary $sync.selfHeal true -}}
{{- $ssa := hasKey $sync "serverSideApply" | ternary $sync.serverSideApply true -}}
{{- $cns := hasKey $sync "createNamespace" | ternary $sync.createNamespace false -}}
{{- /* A failed sync retries, and `refresh: true` (Argo CD 3.2 and later) makes
       each retry resolve the latest revision, so a fix that merged meanwhile
       is picked up instead of the failed revision being parked. No limit:
       steady-state wave gating is going away, and the retry is how a ring
       converges once what it waited for exists. platform.syncRetry overrides
       any of it. */ -}}
{{- $retry := mustMergeOverwrite (dict "limit" -1 "refresh" true "backoff" (dict "duration" "15s" "factor" 2 "maxDuration" "5m")) (deepCopy ($plat.syncRetry | default dict)) -}}
{{- $repo := required (printf "cd-delivery: products.%s.repository or platform.chartRegistry is required" $name) ($p.repository | default $plat.chartRegistry) -}}
{{- /* Merged last, so a product's own payload wins. A value that is empty (an
       empty string, zero, false) cannot override one the chart composed. */ -}}
{{- $values := mustMergeOverwrite (deepCopy .values) (deepCopy (dig "values" $ring (dict) $p)) -}}
{{- $source := dict "repoURL" (printf "%s/%s%s" $repo $name $suffix) "path" "." "targetRevision" (toString $p.pin) "helm" (dict "releaseName" (printf "%s%s" $name $suffix) "valuesObject" $values) -}}
{{- $spec := dict "project" (include "cd-delivery.fill" (dict "s" (required "cd-delivery: platform.appProject is required" $plat.appProject) "product" $name "cluster" $cluster)) "sources" (list $source) "destination" (dict "name" $cluster "namespace" $name) "syncPolicy" (dict "automated" (dict "prune" $prune "selfHeal" $self) "retry" $retry "syncOptions" (list (printf "ServerSideApply=%t" $ssa) (printf "CreateNamespace=%t" $cns))) -}}
{{- with (dig "ignoreDifferences" $ring (list) $p) -}}{{- $_ := set $spec "ignoreDifferences" . -}}{{- end -}}
{{- $app := dict "apiVersion" "argoproj.io/v1alpha1" "kind" "Application" "metadata" $meta "spec" $spec -}}
{{- print "---\n" (toYaml $app) -}}
{{- end -}}
