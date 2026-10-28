package delivery

import (
	"fmt"
	"slices"
)

// CompanyCheck is the status check a company delivery repository requires on
// its default branch (the cd-company model).
const CompanyCheck = "check"

// BranchRuleset is one branch ruleset of a repository, reduced to what the
// delivery model reads.
type BranchRuleset struct {
	// Pattern is the branch pattern, for example "~DEFAULT_BRANCH" or a name.
	Pattern string
	// RequiredChecks are the status checks the ruleset requires.
	RequiredChecks []string
	// BypassApps are the identifiers of the Apps that may bypass the ruleset.
	BypassApps []string
}

// RepoFacts is what ValidateCompanyRepo needs to know about a company
// delivery repository, as resolved by the caller's repository settings.
type RepoFacts struct {
	// Name is the repository's full name, used in messages (owner/repo).
	Name string
	// DefaultBranch is the repository's default branch.
	DefaultBranch string
	// ClassicProtection reports whether classic branch protection is on.
	ClassicProtection bool
	// ReviewRequired reports whether classic protection requires a human
	// approval.
	ReviewRequired bool
	// ClassicRequiredChecks are the status checks classic protection requires.
	ClassicRequiredChecks []string
	// Rulesets are the repository's branch rulesets.
	Rulesets []BranchRuleset
}

// ValidateCompanyRepo checks a company delivery repository, the one Kargo
// writes version pins to, against the cd-company model: the delivery branch is
// the default branch; no human approval is required; CompanyCheck is required
// by a ruleset on the default branch (pattern "~DEFAULT_BRANCH" or the branch
// name) that names writeApp, Kargo's write-back App, as a bypass; and classic
// protection does not require CompanyCheck, because it cannot carry an App
// bypass and would refuse Kargo's commit. It returns one error per violation.
func ValidateCompanyRepo(branch, writeApp string, repo RepoFacts) []error {
	var errs []error

	if repo.DefaultBranch != branch {
		errs = append(errs, fmt.Errorf("branch %q does not match %s default branch %q",
			branch, repo.Name, repo.DefaultBranch))
	}

	if repo.ClassicProtection && repo.ReviewRequired {
		errs = append(errs, fmt.Errorf("%s must not require a human approval (review: none)", repo.Name))
	}

	if repo.ClassicProtection && slices.Contains(repo.ClassicRequiredChecks, CompanyCheck) {
		errs = append(errs, fmt.Errorf("%s must require `%s` in its branch ruleset, not in classic protection, which would refuse Kargo's bypass",
			repo.Name, CompanyCheck))
	}

	bypass, required := false, false

	for _, ruleset := range repo.Rulesets {
		if ruleset.Pattern != "~DEFAULT_BRANCH" && ruleset.Pattern != branch {
			continue
		}

		bypass = bypass || slices.Contains(ruleset.BypassApps, writeApp)
		required = required || slices.Contains(ruleset.RequiredChecks, CompanyCheck)
	}

	if !required {
		errs = append(errs, fmt.Errorf("%s must require the `%s` context on its default branch (a branch ruleset)",
			repo.Name, CompanyCheck))
	}

	if !bypass {
		errs = append(errs, fmt.Errorf("no branch ruleset on the default branch names %q as a bypass App", writeApp))
	}

	return errs
}
