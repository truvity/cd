{{/*
The highest delivery interface this chart knows. A product that declares a
higher one reads keys this chart cannot render, so it is refused rather than
rendered short. The numbers and what each adds are the registry's
(truvity/policy docs/contracts/delivery-interface.md); this one moves when a
step is added to it, and the entry in CHANGELOG.md says which.
*/}}
{{- define "cd-delivery.maxInterface" -}}15{{- end -}}

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

{{/*
Whether a pin is at or above a first release: "true", or nothing. The pin is a
version with an optional leading `v`; one that is not a version (a sentinel, an
empty pin of a dormant product) never gates true. The comparison is
`>=<since>-0`, so a prerelease of `since` counts. The pin is moved by the
delivery tool without the platform rendering anything, which is why the chart
compares it, not the platform. Call with (dict "what" <path for the message>
"pin" <pin> "since" <x.y.z>).
*/}}
{{- define "cd-delivery.pinAtLeast" -}}
{{- $pin := toString .pin | trimPrefix "v" -}}
{{- $since := toString (.since | default "") -}}
{{- if not (regexMatch "^[0-9]+\\.[0-9]+\\.[0-9]+$" $since) -}}
{{- fail (printf "cd-delivery: %s.since must be a version x.y.z, got %q" .what $since) -}}
{{- end -}}
{{- if and (regexMatch "^[0-9]+\\.[0-9]+\\.[0-9]+" $pin) (semverCompare (printf ">=%s-0" $since) $pin) -}}true{{- end -}}
{{- end -}}

{{/*
The layers of a product (`layers`: ordered `{name, since, required, values}`)
whose pin gate is open, as YAML for `fromYaml` (`layers`, a list in the order
written). A layer with `required: true` whose gate is closed is a refusal
instead of a silent skip. Call with (dict "name" <product> "p" <product map>).
*/}}
{{- define "cd-delivery.activeLayers" -}}
{{- $name := .name -}}
{{- $p := .p -}}
{{- $active := list -}}
{{- $seen := dict -}}
{{- $rings := list "infra" "app" "e2e" -}}
{{- include "cd-delivery.assertKeys" (dict "what" (printf "products.%s.platformValues" $name) "got" ($p.platformValues | default dict) "allowed" $rings) -}}
{{- range $i, $layer := ($p.layers | default list) -}}
{{- $what := printf "products.%s.layers[%d]" $name $i -}}
{{- include "cd-delivery.assertKeys" (dict "what" $what "got" $layer "allowed" (list "name" "since" "required" "values")) -}}
{{- $lname := required (printf "cd-delivery: %s.name is required" $what) $layer.name -}}
{{- if hasKey $seen $lname -}}{{- fail (printf "cd-delivery: products.%s has two layers named %q" $name $lname) -}}{{- end -}}
{{- $_ := set $seen $lname true -}}
{{- include "cd-delivery.assertKeys" (dict "what" (printf "%s.values" $what) "got" ($layer.values | default dict) "allowed" $rings) -}}
{{- $since := required (printf "cd-delivery: %s.since is required" $what) $layer.since -}}
{{- if include "cd-delivery.pinAtLeast" (dict "what" $what "pin" $p.pin "since" $since) -}}
{{- $active = append $active $layer -}}
{{- else if $layer.required -}}
{{- fail (printf "cd-delivery: products.%s needs a pin >= %s, got %q (layer %q)" $name (toString $since) (toString $p.pin) $lname) -}}
{{- end -}}
{{- end -}}
{{- toYaml (dict "layers" $active) -}}
{{- end -}}
