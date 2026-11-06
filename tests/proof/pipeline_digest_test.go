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

// renderDigestPipeline renders the cd-pipeline chart with the given values
// and returns its documents grouped by the namespace they live in.
func renderDigestPipeline(t *testing.T, values map[string]any) map[string][]string {
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

// TestPipelineDigestPinEqualsHandSpelledPipeline proves that a project with
// `chart.pin: digest` renders the Warehouse and the Stages a platform used to
// spell out by hand (tests/proof/testdata/static-pipeline.yaml), so a platform
// can switch to the chart without renaming, and so losing the Freight of, an
// object. The promotion's variable is `chartRepo` in the chart and `imageRepo`
// in the fixture; nothing else may differ.
func TestPipelineDigestPinEqualsHandSpelledPipeline(t *testing.T) {
	rendered := renderDigestPipeline(t, companyValues(t, "../cases/cd-pipeline/digest-pin/values.yaml"))["site"]

	raw, err := os.ReadFile("testdata/static-pipeline.yaml")
	if err != nil {
		t.Fatal(err)
	}
	type object struct {
		Kind     string                `yaml:"kind"`
		Metadata struct{ Name string } `yaml:"metadata"`
		Spec     map[string]any        `yaml:"spec"`
	}
	parse := func(docs []string) map[string]map[string]any {
		out := map[string]map[string]any{}
		for _, doc := range docs {
			var obj object
			if err := yaml.Unmarshal([]byte(doc), &obj); err != nil {
				t.Fatal(err)
			}
			if obj.Kind == "Warehouse" || obj.Kind == "Stage" {
				out[obj.Kind+"/"+obj.Metadata.Name] = obj.Spec
			}
		}
		return out
	}
	want := parse(strings.Split(string(raw), "\n---\n"))
	got := parse(rendered)
	if len(want) != 3 {
		t.Fatalf("the fixture should hold a Warehouse and two Stages, got %d objects", len(want))
	}
	for name, spec := range want {
		gotYAML, _ := yaml.Marshal(got[name])
		wantYAML, _ := yaml.Marshal(spec)
		normalized := strings.ReplaceAll(string(gotYAML), "vars.chartRepo", "vars.imageRepo")
		normalized = strings.ReplaceAll(normalized, "name: chartRepo", "name: imageRepo")
		if normalized != string(wantYAML) {
			t.Errorf("%s differs from the hand-spelled one:\n--- chart\n%s\n--- hand-spelled\n%s", name, normalized, wantYAML)
		}
	}
}
