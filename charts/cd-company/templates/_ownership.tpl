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
