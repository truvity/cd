{{/*
cd-lib: the helpers a platform's Application templates share. A library chart
renders nothing; a consumer declares it as a dependency and calls the named
templates below with `include`.

What the helpers read from the consumer's values (the consumer's own, not this
chart's; there is no values.yaml here):

  clusterName       the cluster the render is for (appPrefix, project, appName)
  appNames          optional map: an Application's default name -> its name on
                    this cluster (appName, childAppName)
  overrides         optional map: component -> key -> value (override)
  controlPlaneSpot  optional bool: the cluster's control plane runs on spot
                    capacity (preferOnDemand)

Every template is called with the root context (`.`) unless its comment says
a dict.
*/}}

{{/*
An Application name prefix: "<cluster>-".
*/}}
{{- define "cd-lib.appPrefix" -}}
{{ .Values.clusterName }}-
{{- end -}}

{{/*
The Argo CD AppProject of a cluster's own platform Applications:
platform-<cluster>.
*/}}
{{- define "cd-lib.project" -}}
platform-{{ .Values.clusterName }}
{{- end -}}

{{/*
Override value lookup: the consumer's `overrides.<component>.<key>`, or the
default when it names none.
Call with (dict "root" $ "component" "<component>" "key" "<key>" "default" <value>).
*/}}
{{- define "cd-lib.override" -}}
{{- $default := .default -}}
{{- if and .root.Values.overrides (index .root.Values.overrides .component) -}}
  {{- $compOverrides := index .root.Values.overrides .component -}}
  {{- if hasKey $compOverrides .key -}}
    {{- index $compOverrides .key -}}
  {{- else -}}
    {{- $default -}}
  {{- end -}}
{{- else -}}
  {{- $default -}}
{{- end -}}
{{- end -}}

{{/*
The retry block of an Application's syncPolicy.

Argo CD 3.2 and later honours `refresh: true`: a retry first resolves the
LATEST revision of the sources instead of replaying the one that failed, so a
fix that has merged since is what the next attempt syncs. Without it a failed
revision is retried as it was, or parked until someone intervenes. `limit: -1`
retries forever, which is how a cluster converges when an Application's
dependency lands later.

Usage: {{ include "cd-lib.retry" . | nindent 4 }} under syncPolicy:.
*/}}
{{- define "cd-lib.retry" -}}
retry:
  limit: -1
  refresh: true
  backoff:
    duration: 15s
    factor: 2
    maxDuration: 5m
{{- end -}}

{{/*
An Application's name on THIS cluster: "<cluster>-<name>", renamed by the
consumer's `appNames` where it lists the name.
Call with (dict "root" $ "name" "<the name after the prefix, before the rename>").
*/}}
{{- define "cd-lib.appName" -}}
{{- $names := .root.Values.appNames | default dict -}}
{{- $name := .name -}}
{{- if hasKey $names $name }}{{ $name = index $names $name }}{{ end -}}
{{ include "cd-lib.appPrefix" .root }}{{ $name }}
{{- end -}}

{{/*
A CHILD cluster's Application name, as the cluster that manages it refers to
it (in a Kargo Stage, say): "<child>-<name>", renamed like cd-lib.appName.
Call with (dict "root" $ "cluster" "<child>" "name" "<the name after the prefix, before the rename>").
*/}}
{{- define "cd-lib.childAppName" -}}
{{- $names := .root.Values.appNames | default dict -}}
{{- $name := .name -}}
{{- if hasKey $names $name }}{{ $name = index $names $name }}{{ end -}}
{{ .cluster }}-{{ $name }}
{{- end -}}

{{/*
Whether a pin is at or above a first release ("" or not a version = off). A
pin that is not a version (a golden suite's sentinel) never gates true. Prints
"true" or nothing.
Call with (dict "pin" <pin> "since" <x.y.z or "">).
*/}}
{{- define "cd-lib.pinAtLeast" -}}
{{- $pin := toString .pin | trimPrefix "v" -}}
{{- $since := toString (.since | default "") -}}
{{- if and (regexMatch "^[0-9]+\\.[0-9]+\\.[0-9]+$" $since) (regexMatch "^[0-9]+\\.[0-9]+\\.[0-9]+" $pin) (semverCompare (printf ">=%s-0" $since) $pin) -}}true{{- end -}}
{{- end -}}

{{/*
ignoreDifferences for an Application that deploys a VMRule: the CRD's own
structural schema (operator.victoriametrics.com/v1beta1, VMRule) defaults
`record: ""` on every rule item, the API server injects it, and a chart that
sets no `record` on an alerting rule never resolves the diff without this.

Deliberately NOT for use with RespectIgnoreDifferences: ignoring a field
inside a list under it freezes the WHOLE list at its live value, so a rule
added to the chart later would never sync in. Left off, this only narrows the
diff; every sync still applies the full rendered manifest.

Only `record` is covered; a chart that mixes in recording rules needs
cd-lib.vmruleRecordAlertIgnore.

Usage: {{ include "cd-lib.vmruleRecordIgnore" . | nindent 2 }} under an
Application's spec:, at the level of destination/syncPolicy.
*/}}
{{- define "cd-lib.vmruleRecordIgnore" -}}
ignoreDifferences:
  - group: operator.victoriametrics.com
    kind: VMRule
    jqPathExpressions:
      - .spec.groups[]?.rules[]?.record
{{- end -}}

{{/*
The same, for an Application that deploys RECORDING rules as well: the CRD
defaults `alert: ""` on a rule item exactly as it defaults `record: ""`, and a
recording leaves `alert` unset. Usage as cd-lib.vmruleRecordIgnore.
*/}}
{{- define "cd-lib.vmruleRecordAlertIgnore" -}}
ignoreDifferences:
  - group: operator.victoriametrics.com
    kind: VMRule
    jqPathExpressions:
      - .spec.groups[]?.rules[]?.record
      - .spec.groups[]?.rules[]?.alert
{{- end -}}

{{/*
Preferred on-demand node affinity: the `affinity:` VALUE (nodeAffinity only),
for `nindent` under a chart's affinity key. Preferred, never required: a spot
node still takes the pod when no on-demand capacity is up. A cluster whose
`controlPlaneSpot` is set gets `{}`, no preference. It reads the Karpenter
capacity-type label, so it is for pools that allow both capacity types.
*/}}
{{- define "cd-lib.preferOnDemand" -}}
{{- if .Values.controlPlaneSpot -}}
{}
{{- else -}}
nodeAffinity:
  preferredDuringSchedulingIgnoredDuringExecution:
    - weight: 100
      preference:
        matchExpressions:
          - key: karpenter.sh/capacity-type
            operator: In
            values: [on-demand]
{{- end -}}
{{- end -}}

{{/*
Per-node soft spread: the `topologySpreadConstraints:` VALUE for the pods
matching the given labels.
ScheduleAnyway, not DoNotSchedule: a hard spread leaves a pod Pending when
the pool is down to one node (a spot reclaim), and Karpenter still tries to
satisfy a soft spread first, so it provisions the second node.
Call with (dict "labels" <map>).
*/}}
{{- define "cd-lib.spreadPerNode" -}}
- maxSkew: 1
  topologyKey: kubernetes.io/hostname
  whenUnsatisfiable: ScheduleAnyway
  labelSelector:
    matchLabels:
      {{- toYaml .labels | nindent 6 }}
{{- end -}}
