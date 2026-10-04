{{/*
What the chart composes for each ring, as YAML the caller parses. Every key is
passed by name (truvity/policy's platform contract, section 10): nothing here
is derived from a naming convention of an estate. The names the chart builds
(`<product>-infra-pg-rw`, the runtime role, the secrets) are the delivery
chart's own convention and are the same on every platform that uses it.

A key that a product's charts accept only from some interface is written
under `ge $i N`, where N is the registry's step (truvity/policy
docs/contracts/delivery-interface.md), and nowhere else: the render never
compares a version.

All three take the product context (dict "plat" "name" "p" "i").
*/}}

{{/* The infrastructure ring: the objects the install owns. */}}
{{- define "cd-delivery.infraValues" -}}
{{- $plat := .plat -}}
{{- $name := .name -}}
{{- $p := .p -}}
{{- $i := .i -}}
{{- $cluster := $plat.clusterName -}}
{{- $pg := required "cd-delivery: platform.postgres is required" $plat.postgres -}}
{{- $pgp := $p.postgres | default dict -}}
{{- $ev := required "cd-delivery: platform.events is required" $plat.events -}}
{{- if ge $i 2 }}
installName: {{ $name | quote }}
{{- end }}
tier: {{ $plat.tier | default "primary" | quote }}
{{- with $p.bucketSlug }}
{{- $slug := . }}
{{- $cloud := required (printf "cd-delivery: platform.cloud is required by products.%s, which has a bucketSlug" $name) $plat.cloud }}
{{- $iam := include "cd-delivery.fill" (dict "s" (required "cd-delivery: platform.cloud.iamNameTemplate is required" $cloud.iamNameTemplate) "product" $name "cluster" $cluster "slug" $slug) }}
{{- if gt (len $iam) 64 }}
{{- fail (printf "cd-delivery: products.%s: the cloud role name %q is %d characters, over the 64 a role name may have" $name $iam (len $iam)) }}
{{- end }}
cloud:
  bucket: {{ include "cd-delivery.fill" (dict "s" (required "cd-delivery: platform.cloud.bucketNameTemplate is required" $cloud.bucketNameTemplate) "product" $name "cluster" $cluster "slug" $slug) | quote }}
  iamName: {{ $iam | quote }}
  clusterName: {{ $cluster | quote }}
  accountID: {{ required "cd-delivery: platform.cloud.accountID is required" $cloud.accountID | quote }}
  region: {{ required "cd-delivery: platform.cloud.region is required" $cloud.region | quote }}
  serviceAccount: {{ $name | quote }}
  {{- with $cloud.permissionsBoundary }}
  permissionsBoundary: {{ . | quote }}
  {{- end }}
{{- end }}
postgres:
  {{- if $pgp.platformOwned }}
  {{- if lt $i 9 }}
  {{- fail (printf "cd-delivery: products.%s: postgres.platformOwned needs interface 9, the product declares %d" $name $i) }}
  {{- end }}
  platformOwned: true
  {{- end }}
  instances: {{ $pg.instances }}
  storage: {{ $pg.storage | quote }}
  {{- if (hasKey $pgp "runtimeRole") | ternary $pgp.runtimeRole true }}
  runtimeRole: {{ printf "%s_app" (replace "-" "_" $name) | quote }}
  runtimePasswordSecret: {{ printf "%s-pg-runtime" $name | quote }}
  runtimePassword:
    generate: true
  {{- end }}
  {{- with $pg.labels }}
  labels:
    {{- range $k, $v := . }}
    {{ $k | quote }}: {{ include "cd-delivery.fill" (dict "s" $v "product" $name "cluster" $cluster) | quote }}
    {{- end }}
  {{- end }}
  {{- with $pg.scheduling }}
  scheduling:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with $pgp.backup }}
  backup:
    objectStoreName: {{ .objectStoreName | quote }}
    serverName: {{ .serverName | quote }}
  {{- end }}
  {{- if $pgp.serverTLS }}
  serverTLS:
    secretName: {{ printf "%s-infra-pg-server-tls" $name | quote }}
    caSecretName: {{ printf "%s-infra-pg-server-ca" $name | quote }}
  {{- end }}
events:
  storage: {{ $ev.storage | quote }}
  account: {{ include "cd-delivery.fill" (dict "s" $ev.account "product" $name "cluster" $cluster) | quote }}
  replicas: {{ $ev.replicas }}
  maxAge: {{ $ev.maxAge | quote }}
{{- end -}}

{{/* The application ring. */}}
{{- define "cd-delivery.appValues" -}}
{{- $plat := .plat -}}
{{- $name := .name -}}
{{- $p := .p -}}
{{- $i := .i -}}
{{- $cluster := $plat.clusterName -}}
{{- $pgp := $p.postgres | default dict -}}
{{- $ev := required "cd-delivery: platform.events is required" $plat.events -}}
{{- $tel := $plat.telemetry | default dict -}}
{{- $faro := $p.faro | default dict -}}
{{- $faroOn := and $faro.enabled (ge $i 10) -}}
{{- if ge $i 2 }}
installName: {{ $name | quote }}
{{- end }}
database:
  host: {{ printf "%s-infra-pg-rw" $name | quote }}
  {{- if and $pgp.serverTLS (ge $i 8) }}
  {{- $root := required "cd-delivery: platform.database.rootCA is required where a database serves a certificate" (($plat.database | default dict).rootCA) }}
  tls:
    mode: verify-full
    rootCA:
      configMapName: {{ $root.configMapName | quote }}
      key: {{ $root.key | quote }}
  {{- end }}
  owner:
    passwordSecret: {{ printf "%s-infra-pg-app" $name | quote }}
  app:
    role: {{ printf "%s_app" (replace "-" "_" $name) | quote }}
    passwordSecret: {{ printf "%s-pg-runtime" $name | quote }}
events:
  url: {{ $ev.url | quote }}
  {{- with $ev.authAudience }}
  auth:
    audience: {{ . | quote }}
  {{- end }}
  {{- if and $p.natsIdentity $p.workloadIdentity (ge $i 7) }}
  {{- $nt := required "cd-delivery: platform.events.tls is required where a product has natsIdentity" $ev.tls }}
  tls:
    enabled: true
    serverName: {{ $nt.serverName | quote }}
    caConfigMap: {{ $nt.caConfigMap | quote }}
  {{- end }}
{{- with $tel.endpoint }}
otel:
  endpoint: {{ . | quote }}
  {{- with $tel.sampleRatio }}
  sampleRatio: {{ . | quote }}
  {{- end }}
{{- end }}
{{- if $p.workloadIdentity }}
{{- $id := required "cd-delivery: platform.identity is required by a product with workloadIdentity" $plat.identity }}
{{- $mtls := $p.mtls | default dict }}
tls:
  mode: {{ $id.mode | default "permissive" | quote }}
  trustDomain: {{ required "cd-delivery: platform.identity.trustDomain is required" $id.trustDomain | quote }}
  grantRequest: {{ hasKey $id "grantRequest" | ternary $id.grantRequest false }}
  {{- with $mtls.strict }}
  {{- if lt $i 5 }}
  {{- fail (printf "cd-delivery: products.%s: mtls.strict renders per-component tls, which needs interface 5, the product declares %d" $name $i) }}
  {{- end }}
  components:
    {{- range . }}
    {{ . | quote }}:
      mode: strict
    {{- end }}
  {{- end }}
  {{- with $mtls.peers }}
  peers:
    {{- toYaml . | nindent 4 }}
  {{- end }}
{{- end }}
{{- with $p.bucketSlug }}
{{- $cloud := required (printf "cd-delivery: platform.cloud is required by products.%s, which has a bucketSlug" $name) $plat.cloud }}
archive:
  bucket:
    name: {{ include "cd-delivery.fill" (dict "s" $cloud.bucketNameTemplate "product" $name "cluster" $cluster "slug" .) | quote }}
    region: {{ $cloud.region | quote }}
{{- end }}
serviceAccount:
  app:
    name: {{ $name | quote }}
{{- if $faroOn }}
web:
  faro:
    enabled: true
    apiKey: {{ required (printf "cd-delivery: products.%s.faro.apiKey is required" $name) $faro.apiKey | quote }}
    environment: {{ $cluster | quote }}
    sampleRate: {{ hasKey $faro "sampleRate" | ternary $faro.sampleRate 1 }}
{{- end }}
route:
  enabled: true
  hostname: {{ required (printf "cd-delivery: products.%s.hostname is required" $name) $p.hostname | quote }}
  {{- if $faroOn }}
  {{- $fr := required (printf "cd-delivery: products.%s.faro.route is required" $name) $faro.route }}
  faro:
    enabled: true
    ruleName: {{ $fr.ruleName | quote }}
    path: {{ $fr.path | quote }}
    rewritePath: {{ $fr.rewritePath | quote }}
    backend:
      name: {{ $fr.backend.name | quote }}
      namespace: {{ $fr.backend.namespace | quote }}
      port: {{ $fr.backend.port }}
    {{- if and (ge $i 11) $fr.requestBufferLimit }}
    requestBufferLimit: {{ $fr.requestBufferLimit | quote }}
    {{- end }}
  {{- end }}
  {{- with $p.parentRef }}
  parentRef:
    {{- toYaml . | nindent 4 }}
  {{- end }}
  {{- with $p.surfaces }}
  surfaces:
    {{- range . }}
    - name: {{ .name | quote }}
      hostname: {{ .hostname | quote }}
      {{- with $p.parentRef }}
      parentRef:
        {{- toYaml . | nindent 8 }}
      {{- end }}
    {{- end }}
  {{- end }}
{{- with $p.identityProviders }}
identityProviders:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- if ge $i 12 }}
{{- with $p.alerts }}
alerts:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end }}
{{- with $p.access }}
access:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with $p.product }}
product:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end -}}

{{/* The end-to-end ring: the product's own suite and prober, in its namespace. */}}
{{- define "cd-delivery.e2eValues" -}}
{{- $plat := .plat -}}
{{- $name := .name -}}
{{- $p := .p -}}
{{- $i := .i -}}
{{- $cluster := $plat.clusterName -}}
{{- $e2e := $p.e2e -}}
{{- $pe := $plat.e2e | default dict -}}
{{- $tel := $plat.telemetry | default dict -}}
{{- $ident := and $p.workloadIdentity $e2e.proberGranted (ge $i 4) -}}
{{- if not $p.bucketSlug }}
{{- fail (printf "cd-delivery: products.%s: the end-to-end chart needs a bucketSlug, there is no safe default to give its archive" $name) }}
{{- end }}
{{- $cloud := required (printf "cd-delivery: platform.cloud is required by products.%s, which has a bucketSlug" $name) $plat.cloud }}
appRelease: {{ $name | quote }}
mode: {{ required "cd-delivery: platform.e2e.mode is required by a product with end-to-end" $pe.mode | quote }}
database:
  host: {{ printf "%s-infra-pg-rw" $name | quote }}
  name: {{ replace "-" "_" $name | quote }}
  owner:
    role: {{ printf "%s_owner" (replace "-" "_" $name) | quote }}
  app:
    role: {{ printf "%s_app" (replace "-" "_" $name) | quote }}
    passwordSecret: {{ printf "%s-pg-runtime" $name | quote }}
events:
  {{- toYaml (required (printf "cd-delivery: products.%s.e2e.events is required" $name) $e2e.events) | nindent 2 }}
archive:
  bucket: {{ include "cd-delivery.fill" (dict "s" $cloud.bucketNameTemplate "product" $name "cluster" $cluster "slug" $p.bucketSlug) | quote }}
  region: {{ $cloud.region | quote }}
{{- with $pe.traces }}
traces:
  url: {{ .url | quote }}
  tokenExchange:
    tokenURL: {{ .tokenURL | quote }}
    client: {{ .client | quote }}
    audience: {{ .audience | quote }}
  caConfigMap: {{ .caConfigMap | quote }}
{{- end }}
{{- with $tel.endpoint }}
otel:
  endpoint: {{ . | quote }}
  {{- with $tel.sampleRatio }}
  sampleRatio: {{ . | quote }}
  {{- end }}
{{- end }}
serviceAccount:
  create: true
  name: {{ printf "%s-e2e" $name | quote }}
job:
  annotations:
    {{- if $e2e.hook }}
    argocd.argoproj.io/hook: Sync
    argocd.argoproj.io/hook-delete-policy: BeforeHookCreation
    argocd.argoproj.io/sync-wave: "1"
    {{- else }}
    argocd.argoproj.io/sync-options: "Force=true,Replace=true"
    {{- end }}
  {{- if $e2e.hook }}
  {{- /* A hook Job is not tracked, so nothing prunes old versions and
         self-heal no longer recreates one that deleted itself. */}}
  ttlSecondsAfterFinished: {{ $e2e.ttlSecondsAfterFinished | default 86400 }}
  {{- end }}
{{- if $ident }}
{{- $id := required "cd-delivery: platform.identity is required by a product with workloadIdentity" $plat.identity }}
tls:
  mode: {{ $id.mode | default "permissive" | quote }}
  trustDomain: {{ required "cd-delivery: platform.identity.trustDomain is required" $id.trustDomain | quote }}
  grantRequest: {{ hasKey $id "grantRequest" | ternary $id.grantRequest false }}
  peers:
    {{- if ge $i 6 }}
    {{- range (required (printf "cd-delivery: products.%s.e2e.peerComponents is required" $name) $e2e.peerComponents) }}
    - namespace: {{ $name | quote }}
      serviceAccount: {{ printf "%s-%s" $name . | quote }}
    {{- end }}
    {{- else }}
    - namespace: {{ $name | quote }}
      serviceAccount: {{ $name | quote }}
    {{- end }}
{{- end }}
prober:
  enabled: true
  interval: {{ $pe.proberInterval | default "30s" | quote }}
  {{- if $ident }}
  serviceAccount:
    name: {{ printf "%s-e2e-prober" $name | quote }}
  {{- end }}
{{- end -}}
