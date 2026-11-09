{{/*
The adapter: platform facts, the service allowlist and the company's pins and
ring payloads, checked for ownership and handed to `cd-delivery.applications`.
templates/applications.yaml calls it with the chart's values; cd-project calls
it once per cluster of a project, with that cluster's slice of its own values,
so both render the Applications through the one code path.
Call with (dict "platform" .. "products" .. "services" .. "pins" .. "values" ..
"chart" <the name a refusal starts with, default cd-company>
"at" <service -> the name a refusal's path shows for it, default the service>).
*/}}
{{- define "cd-company.render" -}}
{{- $v := . -}}
{{- $chart := .chart | default "cd-company" -}}
{{- $at := .at | default dict -}}
{{- $products := dict -}}
{{- range $kind := list "pins" "values" "products" -}}
  {{- range $service := keys (index $v $kind) | sortAlpha -}}
    {{- if not (has $service $v.services) -}}
      {{- fail (printf "%s: unknown service %q in %s (not in services)" $chart $service $kind) -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- range $service := $v.services -}}
  {{- $where := index $at $service | default $service -}}
  {{- if not (hasKey $v.pins $service) -}}
    {{- fail (printf "%s: missing pins.%s (use an empty string for a dormant service)" $chart $where) -}}
  {{- end -}}
  {{- $pin := index $v.pins $service -}}
  {{- if not (kindIs "string" $pin) -}}
    {{- fail (printf "%s: pins.%s must be a string" $chart $where) -}}
  {{- end -}}
  {{- $values := index $v.values $service | default dict -}}
  {{- /* The kind of product decides which rings exist and which roots are
         reserved. A service without product facts yet is a product. */ -}}
  {{- $kind := dig "kind" "product" (index $v.products $service | default dict) -}}
  {{- /* What the platform sets through the product's platformValues and its
         open layers is reserved too, beside the fixed list. The pin is the
         company's, so the layers are resolved here, at render. */ -}}
  {{- $trees := list -}}
  {{- if hasKey $v.products $service -}}
    {{- $facts := deepCopy (index $v.products $service) -}}
    {{- $_ := set $facts "pin" $pin -}}
    {{- $trees = (include "cd-company.platformPayloadTrees" (dict "name" $service "product" $facts) | fromYaml).trees -}}
  {{- end -}}
  {{- range $ring, $payload := $values -}}
    {{- if not (has $ring (list "infra" "app" "e2e")) -}}
      {{- fail (printf "%s: values.%s: unknown ring %q" $chart $where $ring) -}}
    {{- end -}}
    {{- $rings := splitList " " (include "cd-company.kindRings" $kind) -}}
    {{- if not (has $ring $rings) -}}
      {{- fail (printf "%s: values.%s.%s: kind %s takes values.%s only" $chart $where $ring $kind (join " and values." $rings)) -}}
    {{- end -}}
    {{- include "cd-company.assertOwnership" (dict "chart" $chart "path" (printf "values.%s.%s" $where $ring) "payload" $payload "owned" (include (ternary "cd-company.ownedStaticSite" "cd-company.owned" (eq $kind "static-site")) (dict "kind" $kind "ring" $ring) | fromYaml)) -}}
    {{- range $reserved := $trees -}}
      {{- if eq $reserved.ring $ring -}}
        {{- include "cd-company.assertOwnership" (dict "chart" $chart "path" (printf "values.%s.%s" $where $ring) "payload" $payload "owned" $reserved.tree) -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
  {{- if ne $pin "" -}}
    {{- if not (hasKey $v.products $service) -}}
      {{- fail (printf "%s: missing products.%s for non-dormant service" $chart $where) -}}
    {{- end -}}
    {{- $product := deepCopy (index $v.products $service) -}}
    {{- $_ := set $product "pin" $pin -}}
    {{- $_ := set $product "values" $values -}}
    {{- $_ := set $products $service $product -}}
  {{- end -}}
{{- end -}}
{{- include "cd-delivery.applications" (dict "platform" $v.platform "products" $products) -}}
{{- end -}}
