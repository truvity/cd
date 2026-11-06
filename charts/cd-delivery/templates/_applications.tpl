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
{{- include "cd-delivery.assertKeys" (dict "what" "platform" "got" $plat "allowed" (list "tier" "finalizer" "sync" "syncRetry" "clusterName" "argocdNamespace" "applicationPrefix" "applicationSuffixes" "appProject" "applicationLabels" "waves" "chartRegistry" "cloud" "postgres" "events" "database" "identity" "telemetry" "e2e" "availability" "alerts" "dashboards" "staticSite")) -}}
{{- range $name := (keys $products | sortAlpha) -}}
{{- $p := get $products $name -}}
{{- $kind := $p.kind | default "product" -}}
{{- if not (has $kind (list "product" "chart" "static-site")) -}}
{{- fail (printf "cd-delivery: products.%s.kind %q is not a kind of product (product, chart, static-site)" $name $kind) -}}
{{- end -}}
{{- if eq $kind "static-site" -}}
{{- /* A site is two Applications of one chart release, pinned by a digest. */ -}}
{{- include "cd-delivery.assertKeys" (dict "what" (printf "products.%s (kind static-site)" $name) "got" $p "allowed" (list "kind" "pin" "chartVersion" "repository" "artifact" "namespace" "values" "platformValues")) -}}
{{- $docs = append $docs (include "cd-delivery.staticSiteProduct" (dict "plat" $plat "name" $name "p" $p)) -}}
{{- else if eq $kind "chart" -}}
{{- /* One chart, one Application: no rings, no platform facts handed down. */ -}}
{{- include "cd-delivery.assertKeys" (dict "what" (printf "products.%s (kind chart)" $name) "got" $p "allowed" (list "kind" "pin" "repository" "chart" "namespace" "values")) -}}
{{- $docs = append $docs (include "cd-delivery.chartProduct" (dict "plat" $plat "name" $name "p" $p)) -}}
{{- else -}}
{{- include "cd-delivery.assertKeys" (dict "what" (printf "products.%s" $name) "got" $p "allowed" (list "kind" "pin" "interface" "repository" "chart" "hostname" "parentRef" "surfaces" "bucketSlug" "workloadIdentity" "natsIdentity" "mtls" "postgres" "e2e" "faro" "alerts" "dashboards" "identityProviders" "access" "product" "values" "ignoreDifferences" "platformValues" "layers")) -}}
{{- $docs = append $docs (include "cd-delivery.product" (dict "plat" $plat "name" $name "p" $p)) -}}
{{- end -}}
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
{{- /* The platform's pin-gated layers that apply to this pin; a required one that does not is a refusal. */ -}}
{{- $layers := (include "cd-delivery.activeLayers" (dict "name" $name "p" $p) | fromYaml).layers -}}
{{- $ctx := dict "plat" $plat "name" $name "p" $p "i" $i "layers" $layers -}}
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
A product of kind `chart`: one chart, one Application (the application ring's
skeleton, named `<prefix><product>`), with only `values.app` as its payload and
nothing composed from the platform's facts. Call with
(dict "plat" <platform> "name" <product> "p" <product map>).
*/}}
{{- define "cd-delivery.chartProduct" -}}
{{- $p := .p -}}
{{- range $ring := (keys ($p.values | default dict)) -}}
{{- if ne $ring "app" -}}
{{- fail (printf "cd-delivery: products.%s (kind chart): kind chart takes values.app only, got values.%s" $.name $ring) -}}
{{- end -}}
{{- end -}}
{{- $ctx := dict "plat" .plat "name" .name "p" $p "i" 0 -}}
{{- include "cd-delivery.application" (dict "ctx" $ctx "ring" "app" "values" (dict)) -}}
{{- end -}}

{{/*
A product of kind `static-site`: the two Applications of a static site, the
infrastructure ring `<name>-infra` (the site's cloud identity) and the site
`<name>`, both of one release of the `static-site-infra` and `static-site`
charts (`chartVersion`) and both installed as the Helm release `<name>`. The
product's `pin` is the digest of the site artifact that runs, never a tag.
The chart writes the keys that tie the two Applications and the artifact
together (below); the registry, identity and server settings are the
platform's `platformValues`, and the product's own `values` come last. None of
them may set what the chart writes. Call with
(dict "plat" <platform> "name" <product> "p" <product map>).
*/}}
{{- define "cd-delivery.staticSiteProduct" -}}
{{- $name := .name -}}
{{- $p := .p -}}
{{- $what := printf "products.%s (kind static-site)" $name -}}
{{- $version := required (printf "cd-delivery: %s: chartVersion is required" $what) $p.chartVersion -}}
{{- $ref := required (printf "cd-delivery: %s: artifact.ref is required" $what) ($p.artifact | default dict).ref -}}
{{- if regexMatch "[:@][^/]*$" (toString $ref) -}}
{{- fail (printf "cd-delivery: %s: artifact.ref must be a repository without a tag or digest, got %q (the digest is the pin)" $what $ref) -}}
{{- end -}}
{{- $digest := toString (required (printf "cd-delivery: %s: pin is required" $what) $p.pin) -}}
{{- if not (regexMatch "^sha256:[0-9a-f]{64}$" $digest) -}}
{{- fail (printf "cd-delivery: %s: pin must be a digest (sha256: and 64 lower-case hex digits), got %q" $what $digest) -}}
{{- end -}}
{{- /* What the chart writes, per ring. */ -}}
{{- $written := dict "infra" (list "fullnameOverride" "installName" "serviceAccount.name") "app" (list "fullnameOverride" "serviceAccount.name" "site.artifact.ref" "site.artifact.digest") -}}
{{- $composed := dict
      "infra" (dict "fullnameOverride" $name "installName" $name "serviceAccount" (dict "name" $name))
      "app" (dict "fullnameOverride" $name "serviceAccount" (dict "name" $name) "site" (dict "artifact" (dict "ref" $ref "digest" $digest))) -}}
{{- range $channel := list "platformValues" "values" -}}
{{- range $ring := (keys (get $p $channel | default dict) | sortAlpha) -}}
{{- if not (has $ring (list "infra" "app")) -}}
{{- fail (printf "cd-delivery: %s: kind static-site takes values.infra and values.app only, got %s.%s" $what $channel $ring) -}}
{{- end -}}
{{- include "cd-delivery.assertUnwritten" (dict "what" $what "channel" (printf "%s.%s" $channel $ring) "payload" (index (get $p $channel) $ring) "paths" (get $written $ring)) -}}
{{- end -}}
{{- end -}}
{{- $docs := list -}}
{{- range $ring := list "infra" "app" -}}
{{- $values := mustMergeOverwrite (deepCopy (get $composed $ring)) (deepCopy (dig "platformValues" $ring (dict) $p)) (deepCopy (dig "values" $ring (dict) $p)) -}}
{{- $ctx := dict "plat" $.plat "name" $name "p" $p "i" 0 "kind" "static-site" "chart" "static-site" "revision" (toString $version) "releaseName" $name -}}
{{- $docs = append $docs (include "cd-delivery.application" (dict "ctx" $ctx "ring" $ring "values" $values)) -}}
{{- end -}}
{{- join "\n" $docs -}}
{{- end -}}

{{/*
Refuse a payload that sets a path the chart writes, or replaces a map on the
way to it with a value that is not a map. `paths` are dotted. Call with
(dict "what" <product for the message> "channel" <e.g. platformValues.app>
"payload" <map> "paths" <list>).
*/}}
{{- define "cd-delivery.assertUnwritten" -}}
{{- range $path := .paths -}}
{{- $keys := splitList "." $path -}}
{{- $cur := $.payload -}}
{{- range $i, $key := $keys -}}
{{- if kindIs "map" $cur -}}
{{- if hasKey $cur $key -}}
{{- $next := index $cur $key -}}
{{- if eq (int $i) (sub (len $keys) 1 | int) -}}
{{- fail (printf "cd-delivery: %s: %s.%s is written by the chart and cannot be set" $.what $.channel $path) -}}
{{- else if not (kindIs "map" $next) -}}
{{- fail (printf "cd-delivery: %s: %s.%s replaces a map the chart writes into (%s)" $.what $.channel (join "." (slice $keys 0 (add1 (int $i)))) $path) -}}
{{- end -}}
{{- $cur = $next -}}
{{- else -}}
{{- $cur = dict -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
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
{{- /* The Application's NAME may take its own suffix per ring
       (platform.applicationSuffixes, e.g. infra: "-inf", app: "-app"); the
       chart, its repository and the Helm release keep `$suffix`, so the
       objects the product's charts render keep their names. */ -}}
{{- $nameSuffix := $suffix -}}
{{- with $plat.applicationSuffixes -}}{{- if hasKey . $ring -}}{{- $nameSuffix = index . $ring -}}{{- end -}}{{- end -}}
{{- $cluster := required "cd-delivery: platform.clusterName is required" $plat.clusterName -}}
{{- $labels := dict -}}
{{- range $k, $v := ($plat.applicationLabels | default dict) -}}
{{- $_ := set $labels $k (include "cd-delivery.fill" (dict "s" $v "product" $name "cluster" $cluster)) -}}
{{- end -}}
{{- if not (hasKey $plat "applicationPrefix") -}}{{- fail "cd-delivery: platform.applicationPrefix is required (it may be empty)" -}}{{- end -}}
{{- $kind := $c.kind | default $p.kind | default "product" -}}
{{- $meta := dict "name" (printf "%s%s%s" (toString $plat.applicationPrefix) $name $nameSuffix) "namespace" (required "cd-delivery: platform.argocdNamespace is required" $plat.argocdNamespace) "annotations" (dict) -}}
{{- $e2eOpts := $p.e2e | default dict -}}
{{- /* A Kargo Stage may only sync an Application that names it. Only the
       end-to-end Application is ever handed to a Stage this way. */ -}}
{{- if and (eq $ring "e2e") $e2eOpts.kargoStage -}}{{- $_ := set $meta.annotations "kargo.akuity.io/authorized-stage" $e2eOpts.kargoStage -}}{{- end -}}
{{- /* The platform's ordering metadata of a static site, verbatim: on the
       infrastructure Application and on the site's. */ -}}
{{- if eq $kind "static-site" -}}
{{- if eq $ring "infra" -}}{{- range $k, $v := (($plat.staticSite | default dict).infraLabels | default dict) -}}{{- $_ := set $labels $k $v -}}{{- end -}}{{- end -}}
{{- if eq $ring "app" -}}{{- range $k, $v := (($plat.staticSite | default dict).siteAnnotations | default dict) -}}{{- $_ := set $meta.annotations $k $v -}}{{- end -}}{{- end -}}
{{- end -}}
{{- if not $meta.annotations -}}{{- $_ := unset $meta "annotations" -}}{{- end -}}
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
{{- /* The end-to-end ring never retries. Once its suite runs as a sync hook
       that Kargo triggers, Kargo copies spec.syncPolicy.retry into the
       operation it writes, and an unlimited retry would rerun a failed suite
       forever. A failed run is a result to report, not a state to converge. */ -}}
{{- $syncPolicy := dict "automated" (dict "prune" $prune "selfHeal" $self) "retry" $retry "syncOptions" (list (printf "ServerSideApply=%t" $ssa) (printf "CreateNamespace=%t" $cns)) -}}
{{- if eq $ring "e2e" -}}{{- $_ := unset $syncPolicy "retry" -}}{{- end -}}
{{- /* With `e2e.sync: manual` nothing syncs the end-to-end Application by
       itself: the Kargo Stage that runs it writes the sync operation. */ -}}
{{- if and (eq $ring "e2e") (eq ($e2eOpts.sync | default "auto") "manual") -}}{{- $_ := unset $syncPolicy "automated" -}}{{- end -}}
{{- $repo := required (printf "cd-delivery: products.%s.repository or platform.chartRegistry is required" $name) ($p.repository | default $plat.chartRegistry) -}}
{{- /* The payload channels, in order: what the chart composed, the platform's
       own payload (platformValues), the platform's layers whose pin gate is
       open, in list order, and last the product's own `values`, so that
       wins. A value that is empty (an empty string, zero, false) cannot
       override one an earlier step set. */ -}}
{{- $values := mustMergeOverwrite (deepCopy .values) (deepCopy (dig "platformValues" $ring (dict) $p)) -}}
{{- range $layer := $c.layers -}}
{{- $values = mustMergeOverwrite $values (deepCopy (dig "values" $ring (dict) $layer)) -}}
{{- end -}}
{{- $values = mustMergeOverwrite $values (deepCopy (dig "values" $ring (dict) $p)) -}}
{{- /* The charts a product installs are its own unless it names another
       product's (`chart`): one product installed as several tenants, each
       its own product here (namespace, release, Applications), all from one
       set of charts. */ -}}
{{- $chart := $c.chart | default $p.chart | default $name -}}
{{- /* A static site's two charts are one release (`chartVersion`) and share the
       Helm release name; every other kind pins the product's `pin` and names
       the release after the ring. */ -}}
{{- $revision := $c.revision | default (toString $p.pin) -}}
{{- $release := $c.releaseName | default (printf "%s%s" $name $suffix) -}}
{{- $source := dict "repoURL" (printf "%s/%s%s" $repo $chart $suffix) "path" "." "targetRevision" $revision "helm" (dict "releaseName" $release "valuesObject" $values) -}}
{{- /* A chart-kind or static-site product from an index repository names the chart; from an OCI one the chart is part of the address. */ -}}
{{- if and (has $kind (list "chart" "static-site")) (not (hasPrefix "oci://" $repo)) -}}
{{- $source = dict "repoURL" $repo "chart" (printf "%s%s" $chart $suffix) "targetRevision" $revision "helm" (dict "releaseName" $release "valuesObject" $values) -}}
{{- end -}}
{{- $spec := dict "project" (include "cd-delivery.fill" (dict "s" (required "cd-delivery: platform.appProject is required" $plat.appProject) "product" $name "cluster" $cluster)) "sources" (list $source) "destination" (dict "name" $cluster "namespace" ($p.namespace | default $name)) "syncPolicy" $syncPolicy -}}
{{- $ignore := dig "ignoreDifferences" $ring (list) $p -}}
{{- /* The platform's ignoreDifferences for alert rules (platform.alerts), on
       the application ring of a product whose alerts reach its chart: the
       live object of a rule kind with server-side defaults never matches the
       render. First, before the product's own. */ -}}
{{- if and (eq $ring "app") (ge (int $c.i) 12) (($p.alerts | default dict).enabled) -}}
{{- $ignore = concat ((($plat.alerts | default dict).ignoreDifferences) | default list) $ignore -}}
{{- end -}}
{{- with $ignore -}}{{- $_ := set $spec "ignoreDifferences" . -}}{{- end -}}
{{- $app := dict "apiVersion" "argoproj.io/v1alpha1" "kind" "Application" "metadata" $meta "spec" $spec -}}
{{- print "---\n" (toYaml $app) -}}
{{- end -}}
