package chartgate

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

// SourceMapping explicitly binds a Git source (including a deployment branch)
// to a caller-owned checkout. Revision must be the full commit ID at its HEAD.
// Multiple revisions of one repository may have separate mappings and roots.
type SourceMapping struct {
	RepoURL        string `yaml:"repoURL"`
	TargetRevision string `yaml:"targetRevision"`
	Root           string `yaml:"root"`
	Revision       string `yaml:"revision"`
}

// SourceRevision records the actual source selected for a rendered Application.
type SourceRevision struct {
	RepoURL        string
	TargetRevision string
	Revision       string
	Root           string
	Ref            string
}

var commitID = regexp.MustCompile(`^([a-fA-F0-9]{40}|[a-fA-F0-9]{64})$`)

func (g *Gate) prepareSources(ctx context.Context) error {
	seen := map[string]bool{}
	for i := range g.Sources {
		s := &g.Sources[i]
		key := s.RepoURL + "\n" + s.TargetRevision
		if s.RepoURL == "" || s.TargetRevision == "" || s.Root == "" || !commitID.MatchString(s.Revision) {
			return fmt.Errorf("source mapping %d: repoURL, targetRevision, root and a full immutable revision are required", i+1)
		}
		if seen[key] {
			return fmt.Errorf("duplicate source mapping for %s@%s", s.RepoURL, s.TargetRevision)
		}
		seen[key] = true
		root, err := filepath.Abs(s.Root)
		if err != nil {
			return err
		}
		s.Root = root
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "HEAD")
		cmd.Dir = root
		head, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("source %s@%s: read checkout HEAD in %s: %w", s.RepoURL, s.TargetRevision, root, err)
		}
		if !strings.EqualFold(strings.TrimSpace(string(head)), s.Revision) {
			return fmt.Errorf("source %s@%s: checkout HEAD %s does not match revision %s", s.RepoURL, s.TargetRevision, strings.TrimSpace(string(head)), s.Revision)
		}
		s.Revision = strings.ToLower(s.Revision)
	}
	return nil
}

func (g *Gate) mapping(src Source) (SourceMapping, bool) {
	for _, s := range g.Sources {
		if s.RepoURL == src.RepoURL && s.TargetRevision == src.TargetRevision {
			return s, true
		}
	}
	return SourceMapping{}, false
}

func (g *Gate) sourceRoot(src Source) (string, error) {
	if s, ok := g.mapping(src); ok {
		return s.Root, nil
	}
	if !g.explicit && g.SelfRepo != nil && g.SelfRepo(src.RepoURL) {
		return g.Root, nil
	}
	return "", fmt.Errorf("unmapped Git source %s@%s (supply its checkout and immutable revision)", src.RepoURL, src.TargetRevision)
}

func (g *Gate) selectedSources(app Application) ([]SourceRevision, error) {
	var selected []SourceRevision
	refs := map[string]bool{}
	for _, src := range app.Sources {
		if src.Ref != "" {
			if refs[src.Ref] {
				return selected, fmt.Errorf("duplicate source ref %q", src.Ref)
			}
			refs[src.Ref] = true
		}
		if s, ok := g.mapping(src); ok {
			selected = append(selected, SourceRevision{s.RepoURL, s.TargetRevision, s.Revision, s.Root, src.Ref})
		} else if src.Ref != "" || (g.explicit && isGit(src.RepoURL) && src.Chart == "") {
			if _, err := g.sourceRoot(src); err != nil {
				return selected, err
			}
		}
	}
	return selected, nil
}

// checkoutPath confines paths to the selected checkout, including symlinks.
// Missing files retain os.ErrNotExist for ignoreMissingValueFiles.
func checkoutPath(root, rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("path %q must be relative to the source", rel)
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	path := filepath.Join(root, rel)
	if !within(root, path) {
		return "", fmt.Errorf("path %q leaves the source checkout", rel)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	if !within(resolvedRoot, resolved) {
		return "", fmt.Errorf("path %q leaves the source checkout via a symlink", rel)
	}
	return resolved, nil
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator))
}
