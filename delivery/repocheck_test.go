package delivery

import (
	"strings"
	"testing"
)

func goodRepo() RepoFacts {
	return RepoFacts{
		Name:          "org/deliver",
		DefaultBranch: "main",
		Rulesets: []BranchRuleset{{
			Pattern: "~DEFAULT_BRANCH", RequiredChecks: []string{"check"}, BypassApps: []string{"kargo-write"},
		}},
	}
}

func TestValidateCompanyRepo(t *testing.T) {
	t.Parallel()

	byName := goodRepo()
	byName.Rulesets[0].Pattern = "main"

	for _, tc := range []struct {
		name   string
		mutate func(*RepoFacts)
		repo   *RepoFacts
		want   []string
	}{
		{name: "good"},
		{name: "ruleset by branch name", repo: &byName},
		{name: "branch mismatch", mutate: func(r *RepoFacts) { r.DefaultBranch = "trunk" },
			want: []string{`branch "main" does not match org/deliver default branch "trunk"`}},
		{name: "review required", mutate: func(r *RepoFacts) { r.ClassicProtection, r.ReviewRequired = true, true },
			want: []string{"must not require a human approval"}},
		{name: "review ignored without protection", mutate: func(r *RepoFacts) { r.ReviewRequired = true }},
		{name: "classic requires check", mutate: func(r *RepoFacts) {
			r.ClassicProtection, r.ClassicRequiredChecks = true, []string{"check"}
		}, want: []string{"not in classic protection"}},
		{name: "no check", mutate: func(r *RepoFacts) { r.Rulesets[0].RequiredChecks = nil },
			want: []string{"must require the `check` context"}},
		{name: "no bypass", mutate: func(r *RepoFacts) { r.Rulesets[0].BypassApps = []string{"other"} },
			want: []string{`names "kargo-write" as a bypass App`}},
		{name: "other pattern ignored", mutate: func(r *RepoFacts) { r.Rulesets[0].Pattern = "release/*" },
			want: []string{"must require the `check` context", "bypass App"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			repo := goodRepo()
			if tc.repo != nil {
				repo = *tc.repo
			}

			if tc.mutate != nil {
				tc.mutate(&repo)
			}

			errs := ValidateCompanyRepo("main", "kargo-write", repo)
			if len(errs) != len(tc.want) {
				t.Fatalf("got %d errors %v, want %d", len(errs), errs, len(tc.want))
			}

			for i, want := range tc.want {
				if !strings.Contains(errs[i].Error(), want) {
					t.Errorf("error %d = %q, want it to contain %q", i, errs[i], want)
				}
			}
		})
	}
}
