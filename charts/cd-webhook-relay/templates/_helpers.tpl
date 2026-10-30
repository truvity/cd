{{- define "cd-webhook-relay.name" -}}
{{- if contains "webhook-relay" .Release.Name -}}{{ .Release.Name | trunc 63 | trimSuffix "-" }}{{- else -}}{{ printf "%s-webhook-relay" .Release.Name | trunc 63 | trimSuffix "-" }}{{- end -}}
{{- end -}}

{{- define "cd-webhook-relay.selectorLabels" -}}
app.kubernetes.io/name: webhook-relay
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "cd-webhook-relay.labels" -}}
{{ include "cd-webhook-relay.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/version: {{ .Chart.Version | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{- end -}}

{{/* The path a Secret key is mounted at: /etc/webhook-relay/keys/<secret>/<key>. */}}
{{- define "cd-webhook-relay.secretPath" -}}
/etc/webhook-relay/keys/{{ .name }}/{{ .key }}
{{- end -}}

{{/*
The relay's configuration file, as the ConfigMap holds it. Secrets are named
by mounted file path, never by value.
*/}}
{{- define "cd-webhook-relay.config" -}}
{{- $url := required "cd-webhook-relay: target.url is required" .Values.target.url -}}
{{- $tname := required "cd-webhook-relay: target.secret.name is required" .Values.target.secret.name -}}
{{- $tkey := required "cd-webhook-relay: target.secret.key is required" .Values.target.secret.key -}}
{{- if not .Values.senders -}}{{- fail "cd-webhook-relay: senders needs at least one entry" -}}{{- end -}}
{{- $cfg := dict "listen" ":8080" -}}
{{- if .Values.maxBodyBytes }}{{ $_ := set $cfg "maxBodyBytes" .Values.maxBodyBytes }}{{ end -}}
{{- if .Values.readTimeout }}{{ $_ := set $cfg "readTimeout" .Values.readTimeout }}{{ end -}}
{{- if .Values.writeTimeout }}{{ $_ := set $cfg "writeTimeout" .Values.writeTimeout }}{{ end -}}
{{- if .Values.targetTimeout }}{{ $_ := set $cfg "targetTimeout" .Values.targetTimeout }}{{ end -}}
{{- $senders := list -}}
{{- $names := dict -}}
{{- range .Values.senders -}}
{{- if hasKey $names .name -}}{{- fail (printf "cd-webhook-relay: two senders are named %s" .name) -}}{{- end -}}
{{- $_ := set $names .name true -}}
{{- $senders = append $senders (dict "name" .name "owners" .owners "secretFile" (include "cd-webhook-relay.secretPath" .secret)) -}}
{{- end -}}
{{- $_ := set $cfg "senders" $senders -}}
{{- $_ := set $cfg "target" (dict "url" $url "secretFile" (include "cd-webhook-relay.secretPath" (dict "name" $tname "key" $tkey))) -}}
{{ toYaml $cfg }}
{{- end -}}
