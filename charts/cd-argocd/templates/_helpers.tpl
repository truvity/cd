{{/*
Metadata block for one extra object: its name, optional namespace, and the
labels and annotations the caller gave it. NOTHING is added: no
app.kubernetes.io/* label, no helm.sh/chart, no managed-by. An estate that
adopts an object it already runs must be able to reproduce it exactly, and
every label the chart invented would be a diff.

Call with (dict "name" <string> "namespace" <string or ""> "item" <map>).
*/}}
{{- define "cd-argocd.meta" -}}
name: {{ .name | quote }}
{{- with .namespace }}
namespace: {{ . | quote }}
{{- end }}
{{- with .item.labels }}
labels:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .item.annotations }}
annotations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}
