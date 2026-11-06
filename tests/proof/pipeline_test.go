package proof

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/truvity/cd/parity"
	"go.yaml.in/yaml/v3"
)

// renderPipeline renders the cd-pipeline chart with the given values and
// returns its documents grouped by the namespace they live in ("" for
// cluster-scoped ones), each document kept as written.
func renderPipeline(t *testing.T, values map[string]any) map[string][]string {
	t.Helper()
	helm := parity.RequireHelm(t)
	raw, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(file, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.CommandContext(t.Context(), helm, "template", "x", "../../charts/cd-pipeline", "-f", file).CombinedOutput()
	if err != nil {
		t.Fatalf("helm template: %v\n%s", err, out)
	}
	byNamespace := map[string][]string{}
	for _, doc := range strings.Split(string(out), "\n---\n") {
		var obj struct {
			Metadata struct {
				Namespace string `yaml:"namespace"`
			} `yaml:"metadata"`
		}
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
			t.Fatalf("%v\n%s", err, doc)
		}
		byNamespace[obj.Metadata.Namespace] = append(byNamespace[obj.Metadata.Namespace], doc)
	}
	return byNamespace
}

// TestPipelineProjectOverridesLeaveOtherProjectsAlone proves the zero-diff
// slice of per-project git and promotion overrides: stripping every project
// override from the case leaves the objects of the projects that never had
// one byte-identical, and changes the namespaces of the projects that did.
func TestPipelineProjectOverridesLeaveOtherProjectsAlone(t *testing.T) {
	values := companyValues(t, "../cases/cd-pipeline/per-project-git/values.yaml")
	with := renderPipeline(t, values)

	stripped := companyValues(t, "../cases/cd-pipeline/per-project-git/values.yaml")
	overriding := map[string]bool{}
	for _, p := range stripped["projects"].([]any) {
		project := p.(map[string]any)
		if _, ok := project["git"]; ok {
			overriding[project["name"].(string)] = true
		}
		if _, ok := project["promotion"]; ok {
			overriding[project["name"].(string)] = true
		}
		delete(project, "git")
		delete(project, "promotion")
	}
	without := renderPipeline(t, stripped)

	for _, p := range values["projects"].([]any) {
		name := p.(map[string]any)["name"].(string)
		same := strings.Join(with[name], "\n---\n") == strings.Join(without[name], "\n---\n")
		switch {
		case overriding[name] && same:
			t.Errorf("project %q overrides git or promotion, but its objects did not change", name)
		case !overriding[name] && !same:
			t.Errorf("project %q has no override, but its objects differ from the render without overrides", name)
		}
	}
	if len(overriding) != 2 {
		t.Fatalf("the case should have two overriding projects, got %v", overriding)
	}
}
