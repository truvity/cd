{{/* Paths owned by the platform, including conditional delivery-interface keys.
     Only delivery-value paths are reserved: config.cloud, for example, is
     ordinary application configuration. A parent scalar/list/null cannot
     replace a reserved subtree. Non-platform postgres tuning remains allowed. */}}
{{- define "cd-company.owned" -}}
cloud: true
events: true
identity: true
route: true
serviceAccount: true
tier: true
pin: true
chart: true
repository: true
interface: true
appProject: true
installName: true
availability: true
database: true
tls: true
archive: true
otel: true
access: true
identityProviders: true
alerts: true
observability:
  dashboards: true
web:
  faro: true
postgres:
  platformOwned: true
  instances: true
  storage: true
  runtimeRole: true
  runtimePasswordSecret: true
  runtimePassword: true
  labels: true
  scheduling: true
  backup: true
  serverTLS: true
{{ if eq . "e2e" }}
appRelease: true
mode: true
traces: true
job:
  annotations: true
  ttlSecondsAfterFinished: true
prober:
  enabled: true
  interval: true
  serviceAccount: true
{{ end }}
{{- end -}}

{{- define "cd-company.assertOwnership" -}}
{{- range $key, $value := .payload -}}
  {{- if hasKey $.owned $key -}}
    {{- $reserved := index $.owned $key -}}
    {{- $path := printf "%s.%s" $.path $key -}}
    {{- if or (not (kindIs "map" $reserved)) (not (kindIs "map" $value)) -}}
      {{- fail (printf "cd-company: %s is platform-owned" $path) -}}
    {{- end -}}
    {{- include "cd-company.assertOwnership" (dict "path" $path "payload" $value "owned" $reserved) -}}
  {{- end -}}
{{- end -}}
{{- end -}}

{{/*
The paths the platform sets through a product's `platformValues` and its open
layers (cd-delivery), as ownership trees for `cd-company.assertOwnership`: a
map for each subtree, `true` for each leaf (a scalar, a list, anything that is
not a map). A layer whose pin gate is closed sets nothing, so it reserves
nothing. The pin is the company's, so the gate is read at render time, here
and by cd-delivery with the same rule. As YAML for `fromYaml`
(`trees`: a list of {ring, tree}). Call with (dict "name" <service> "product" <product map with its pin>).
*/}}
{{- define "cd-company.platformPayloadTrees" -}}
{{- $product := .product -}}
{{- $trees := list -}}
{{- range $ring := list "infra" "app" "e2e" -}}
{{- $sources := list (dig "platformValues" $ring (dict) $product) -}}
{{- /* A dormant service (an empty pin) has no layer to resolve. */ -}}
{{- $layers := list -}}
{{- if $product.pin -}}{{- $layers = (include "cd-delivery.activeLayers" (dict "name" $.name "p" $product) | fromYaml).layers -}}{{- end -}}
{{- range $layer := $layers -}}
{{- $sources = append $sources (dig "values" $ring (dict) $layer) -}}
{{- end -}}
{{- range $source := $sources -}}
{{- $tree := include "cd-company.reservedTree" $source | fromYaml -}}
{{- if $tree -}}{{- $trees = append $trees (dict "ring" $ring "tree" $tree) -}}{{- end -}}
{{- end -}}
{{- end -}}
{{- toYaml (dict "trees" $trees) -}}
{{- end -}}

{{/* A payload as an ownership tree: maps recurse (an empty one reserves nothing), every other value is a reserved leaf. */}}
{{- define "cd-company.reservedTree" -}}
{{- $out := dict -}}
{{- range $key, $value := . -}}
{{- if kindIs "map" $value -}}
{{- $sub := include "cd-company.reservedTree" $value | fromYaml -}}
{{- if $sub -}}{{- $_ := set $out $key $sub -}}{{- end -}}
{{- else -}}
{{- $_ := set $out $key true -}}
{{- end -}}
{{- end -}}
{{- toYaml $out -}}
{{- end -}}
