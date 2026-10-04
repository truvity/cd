{{/*
A Stage's promotion: write the promoted pin to the repository's values file,
by pull request on every hop after the project's first Stage and straight to
the branch on the first. As YAML for `fromYaml`: the promotion template's spec.
Call with (dict "root" . "p" <project> "s" <stage> "stages" <name -> stage> "slug" ..).
*/}}
{{- define "cd-pipeline.promotion" -}}
{{- $r := .root -}}
{{- $v := $r.Values -}}
{{- $pr := $v.promotion -}}
{{- $p := .p -}}
{{- $s := .s -}}
{{- $chart := $p.chart | default dict -}}
{{- $chartName := $chart.name | default "" -}}
{{- $prefix := $chart.versionPrefix | default "" -}}
{{- $repo := required "cd-pipeline: git.repoURL is required" ($v.git).repoURL -}}
{{- $upstream := $s.from | default "" -}}
{{- $mode := $s.mode | default (ternary "pr" "direct" (ne $upstream "")) -}}
{{- $retry := dict "errorThreshold" $pr.retry.errorThreshold "timeout" $pr.retry.timeout -}}
{{- $tokens := dict "stage" $s.name "project" $p.name "slug" .slug "pinKey" ($p.pinKey | default "") -}}
{{- $from := include "cd-pipeline.chartFrom" (dict "name" $chartName "vars" true) -}}
{{- $version := printf "${{ %s.Version }}" $from -}}
{{- $steps := list -}}

{{- /* The gate: before a hop moves a pin, the Applications that the version it
       promotes from already runs in must be Healthy and Synced. The first Stage
       has none, and no Stage waits on the Applications it is about to update. */ -}}
{{- if $upstream -}}
{{- $apps := list -}}
{{- range $a := (get $.stages $upstream).applications -}}
{{- $apps = append $apps (dict "name" $a "namespace" $v.argocd.namespace "waitFor" (list "health" "sync" "operation")) -}}
{{- end -}}
{{- if $apps -}}
{{- $steps = append $steps (dict "uses" "argocd-wait" "as" "gate" "retry" (dict "timeout" $pr.gate.timeout) "config" (dict "apps" $apps)) -}}
{{- end -}}
{{- end -}}

{{- $steps = append $steps (dict "uses" "git-clone" "retry" $retry "config" (dict "repoURL" "${{ vars.gitRepo }}" "checkout" (list (dict "branch" $pr.branch "path" "./repo")))) -}}
{{- $steps = append $steps (dict "uses" "yaml-update" "config" (dict "path" (printf "./repo/%s" (include "cd-pipeline.fill" (dict "s" $pr.pinFile "tokens" $tokens))) "updates" (list (dict "key" (include "cd-pipeline.fill" (dict "s" $pr.pinKey "tokens" $tokens)) "value" (printf "%s%s" $prefix $version))))) -}}
{{- $steps = append $steps (dict "uses" "git-commit" "as" "commit" "retry" $retry "config" (dict "path" "./repo" "message" (include "cd-pipeline.fill" (dict "s" $pr.commitMessage "tokens" (merge (dict "version" $version) $tokens))))) -}}
{{- if eq $mode "pr" -}}
{{- $committed := "${{ status('commit') == 'Succeeded' }}" -}}
{{- $steps = append $steps (dict "uses" "git-push" "as" "push" "if" $committed "retry" $retry "config" (dict "path" "./repo" "generateTargetBranch" true)) -}}
{{- $openPR := dict "repoURL" "${{ vars.gitRepo }}" "provider" $pr.provider "sourceBranch" "${{ outputs.push.branch }}" "targetBranch" $pr.branch "title" (include "cd-pipeline.fill" (dict "s" $pr.prTitle "tokens" $tokens)) -}}
{{- if $pr.prLabels -}}{{- $_ := set $openPR "labels" $pr.prLabels -}}{{- end -}}
{{- $steps = append $steps (dict "uses" "git-open-pr" "as" "open-pr" "if" $committed "retry" $retry "config" $openPR) -}}
{{- /* No timeout on the wait: it waits on a person merging, which is not bounded by the transient-failure budget. */ -}}
{{- $steps = append $steps (dict "uses" "git-wait-for-pr" "if" $committed "retry" (dict "errorThreshold" $pr.retry.errorThreshold) "config" (dict "repoURL" "${{ vars.gitRepo }}" "provider" $pr.provider "prNumber" "${{ outputs['open-pr'].pr.id }}")) -}}
{{- else -}}
{{- $steps = append $steps (dict "uses" "git-push" "retry" $retry "config" (dict "path" "./repo" "targetBranch" $pr.branch)) -}}
{{- end -}}
{{- /* The end-to-end run: a Stage that names its end-to-end Application syncs it
       last, once the pin is in the repository. The Application's hook runs the
       suite, and the step waits for the operation, so the timeout covers the
       suite and the rollout it waits for. The Application must authorize this
       Stage; it is not in `applications`, so no later Stage waits on it. */ -}}
{{- if $s.e2eApplication -}}
{{- $steps = append $steps (dict "uses" "argocd-update" "as" "e2e" "retry" (dict "timeout" "25m") "config" (dict "apps" (list (dict "name" $s.e2eApplication "namespace" $v.argocd.namespace)))) -}}
{{- end -}}
{{- $vars := list (dict "name" "gitRepo" "value" $repo) (dict "name" "chartRepo" "value" ($chart.repoURL | default "")) (dict "name" "chartName" "value" $chartName) -}}
{{- toYaml (dict "vars" $vars "steps" $steps) -}}
{{- end -}}
