{{/*
The highest delivery interface this chart knows. A product that declares a
higher one reads keys this chart cannot render, so it is refused rather than
rendered short. The numbers and what each adds are the registry's
(truvity/policy docs/contracts/delivery-interface.md); this one moves when a
step is added to it, and the entry in CHANGELOG.md says which.
*/}}
{{- define "cd-delivery.maxInterface" -}}11{{- end -}}

{{/*
Replace the {product}, {cluster} and {slug} tokens in a string.
Call with (dict "s" <string> "product" <string> "cluster" <string> "slug" <string>).
*/}}
{{- define "cd-delivery.fill" -}}
{{- $s := .s -}}
{{- $s = replace "{product}" (.product | default "") $s -}}
{{- $s = replace "{cluster}" (.cluster | default "") $s -}}
{{- replace "{slug}" (.slug | default "") $s -}}
{{- end -}}

{{/*
Refuse a key this chart does not read. The chart's values.schema.json says the
same when the chart is installed on its own; this is the same rule for a
caller that hands the Application template its input directly (see
`cd-delivery.applications`), where Helm validates nothing.
Call with (dict "what" <path for the message> "got" <map> "allowed" <list>).
*/}}
{{- define "cd-delivery.assertKeys" -}}
{{- $allowed := .allowed -}}
{{- range $k := keys .got -}}
{{- if not (has $k $allowed) -}}
{{- fail (printf "cd-delivery: %s: unknown key %q (allowed: %s)" $.what $k (join ", " $allowed)) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
The interface of a product as an integer, or a refusal.
Call with (dict "name" <product> "p" <product map>).
*/}}
{{- define "cd-delivery.interface" -}}
{{- $raw := toString (required (printf "cd-delivery: products.%s.interface is required" .name) .p.interface) -}}
{{- if not (regexMatch "^[0-9]+$" $raw) -}}
{{- fail (printf "cd-delivery: products.%s.interface must be a whole number, got %q" .name $raw) -}}
{{- end -}}
{{- $i := int $raw -}}
{{- $max := int (include "cd-delivery.maxInterface" .) -}}
{{- if lt $i 1 -}}
{{- fail (printf "cd-delivery: products.%s.interface is %d: the lowest interface is 1" .name $i) -}}
{{- end -}}
{{- if gt $i $max -}}
{{- fail (printf "cd-delivery: products.%s.interface is %d but this chart knows interfaces up to %d: upgrade the chart before the product moves to it" .name $i $max) -}}
{{- end -}}
{{- $i -}}
{{- end -}}
