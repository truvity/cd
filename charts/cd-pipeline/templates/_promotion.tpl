{{/*
A Stage's promotion: write the promoted pin to the repository's values file,
by pull request on every hop after the project's first Stage and straight to
the branch on the first. As YAML for `fromYaml`: the promotion template's spec.
Call with (dict "root" . "p" <project> "s" <stage> "stages" <name -> stage> "slug" .. "eff" <cd-pipeline.effective>).
*/}}
{{- define "cd-pipeline.promotion" -}}
{{- $r := .root -}}
{{- $v := $r.Values -}}
{{- $pr := .eff.promotion -}}
{{- $p := .p -}}
{{- $s := .s -}}
{{- $chart := $p.chart | default dict -}}
{{- $chartName := $chart.name | default "" -}}
{{- $prefix := $chart.versionPrefix | default "" -}}
{{- $repo := required (printf "cd-pipeline: projects.%s: git.repoURL is required (the release's git.repoURL, or the project's own)" $p.name) .eff.git.repoURL -}}
{{- $upstream := $s.from | default "" -}}
{{- $mode := $s.mode | default (ternary "pr" "direct" (ne $upstream "")) -}}
{{- $retry := dict "errorThreshold" $pr.retry.errorThreshold "timeout" $pr.retry.timeout -}}
{{- /* A Stage of a customer's chain fills `{stage}` with its Stage of
       stageOrder (the cluster) and `{customer}` with its customer. */ -}}
{{- $own := dict "stage" ($s.env | default $s.name) "pinKey" ($p.pinKey | default "") -}}
{{- with $s.customer -}}{{- $_ := set $own "customer" . -}}{{- end -}}
{{- $tokens := merge $own (include "cd-pipeline.projectTokens" $p | fromYaml) -}}
{{- range $k := list "pinFile" "pinKey" "commitMessage" "prTitle" -}}
{{- include "cd-pipeline.checkTokens" (dict "p" $p "where" (printf "promotion.%s" $k) "s" (index $pr $k)) -}}
{{- end -}}
{{- $chm := include "cd-pipeline.chartMode" (dict "name" $p.name "chart" $chart) | fromYaml -}}
{{- $chartless := include "cd-pipeline.chartless" (dict "chart" $chart "companions" $p.companions) | eq "true" -}}
{{- $from := include "cd-pipeline.chartFrom" (dict "name" $chartName "vars" true "image" $chm.image) -}}
{{- $digestPin := $chm.digest -}}
{{- /* With companions alone, the first one (by name) stands for the version
       in the wording of the commit and the pull request. */ -}}
{{- if $chartless -}}
{{- $first := index $p.companions (first (keys $p.companions | sortAlpha)) -}}
{{- $fm := include "cd-pipeline.chartMode" (dict "name" $p.name "chart" $first) | fromYaml -}}
{{- $from = include "cd-pipeline.chartFrom" (dict "repoURL" $fm.repo "name" ($first.name | default "") "vars" false "image" $fm.image) -}}
{{- $chm = $fm -}}
{{- $digestPin = $fm.digest -}}
{{- end -}}
{{- $version := printf "${{ %s.%s }}" $from (ternary (ternary "Digest" "Tag" $chm.digest) "Version" $chm.image) -}}
{{- /* A digest pin also offers the discovered tag, for the wording of the commit and the pull request. */ -}}
{{- $wording := deepCopy $tokens -}}
{{- if $digestPin -}}{{- $_ := set $wording "tag" (printf "${{ %s.Tag }}" $from) -}}{{- end -}}
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
{{- $pinKey := include "cd-pipeline.fill" (dict "s" $pr.pinKey "tokens" $tokens) -}}
{{- $updates := ternary list (list (dict "key" $pinKey "value" (printf "%s%s" $prefix $version))) $chartless -}}
{{- /* One pin per companion, in the same file and commit: the artifact of the
       same Freight, read by its repository. */ -}}
{{- range $cn := keys ($p.companions | default dict) | sortAlpha -}}
{{- $co := index $p.companions $cn -}}
{{- $key := printf "%s-%s" $pinKey $cn -}}
{{- with $co.pinKey -}}
{{- include "cd-pipeline.checkTokens" (dict "p" $p "where" (printf "companions.%s.pinKey" $cn) "s" .) -}}
{{- $key = include "cd-pipeline.fill" (dict "s" . "tokens" $tokens) -}}
{{- end -}}
{{- $cm := include "cd-pipeline.chartMode" (dict "name" $p.name "chart" $co) | fromYaml -}}
{{- $cfrom := include "cd-pipeline.chartFrom" (dict "repoURL" $cm.repo "name" ($co.name | default "") "vars" false "image" $cm.image) -}}
{{- $updates = append $updates (dict "key" $key "value" (printf "%s${{ %s.%s }}" ($co.versionPrefix | default "") $cfrom (ternary (ternary "Digest" "Tag" $cm.digest) "Version" $cm.image))) -}}
{{- end -}}
{{- $pinPath := printf "./repo/%s" (include "cd-pipeline.fill" (dict "s" $pr.pinFile "tokens" $tokens)) -}}
{{- /* A missing pin file, seeded: `yaml-update` neither creates a file nor
       adds a key, so a Stage's first promotion into a folder with no pin
       file wrote nothing. With seedPinFile the promotion first writes the
       file with every key it is about to write, each "" (dormant). The
       step refuses an existing file (overwrite false), and continueOnError
       lets the promotion go on: every promotion after a Stage's first shows
       this one step failed ("already exists") and still succeeds. It never
       adds a key to a file that exists. */ -}}
{{- if $pr.seedPinFile -}}
{{- $seed := dict -}}
{{- range $u := $updates -}}
{{- if contains "\\" $u.key -}}
{{- fail (printf "cd-pipeline: projects.%s: promotion.seedPinFile cannot seed the escaped key %q" $p.name $u.key) -}}
{{- end -}}
{{- $parts := splitList "." $u.key -}}
{{- $m := $seed -}}
{{- range $i, $part := $parts -}}
{{- if eq (add1 $i) (len $parts) -}}
{{- $_ := set $m $part "" -}}
{{- else -}}
{{- if not (kindIs "map" (get $m $part)) -}}{{- $_ := set $m $part (dict) -}}{{- end -}}
{{- $m = get $m $part -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- $contents := printf "# Written by Kargo. Seeded by the first promotion: \"\" is dormant.\n%s\n" (toYaml $seed) -}}
{{- $steps = append $steps (dict "uses" "file-write" "as" "seed-pin" "continueOnError" true "config" (dict "path" $pinPath "contents" $contents "overwrite" false)) -}}
{{- end -}}
{{- $steps = append $steps (dict "uses" "yaml-update" "config" (dict "path" $pinPath "updates" $updates)) -}}
{{- $steps = append $steps (dict "uses" "git-commit" "as" "commit" "retry" $retry "config" (dict "path" "./repo" "message" (include "cd-pipeline.fill" (dict "s" $pr.commitMessage "tokens" (merge (dict "version" $version) $wording))))) -}}
{{- if eq $mode "pr" -}}
{{- $committed := "${{ status('commit') == 'Succeeded' }}" -}}
{{- $steps = append $steps (dict "uses" "git-push" "as" "push" "if" $committed "retry" $retry "config" (dict "path" "./repo" "generateTargetBranch" true)) -}}
{{- $openPR := dict "repoURL" "${{ vars.gitRepo }}" "provider" $pr.provider "sourceBranch" "${{ outputs.push.branch }}" "targetBranch" $pr.branch "title" (include "cd-pipeline.fill" (dict "s" $pr.prTitle "tokens" $wording)) -}}
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
{{- $steps = append $steps (dict "uses" "argocd-update" "as" "e2e" "retry" (dict "timeout" "25m0s") "config" (dict "apps" (list (dict "name" $s.e2eApplication "namespace" $v.argocd.namespace)))) -}}
{{- end -}}
{{- $vars := list (dict "name" "gitRepo" "value" $repo) -}}
{{- if not $chartless -}}{{- $vars = concat $vars (list (dict "name" "chartRepo" "value" $chm.repo) (dict "name" "chartName" "value" $chartName)) -}}{{- end -}}
{{- toYaml (dict "vars" $vars "steps" $steps) -}}
{{- end -}}
