// Package proof holds the repository's zero-diff gate: every case under
// tests/cases of a chart that wraps an upstream chart must render the same
// objects through the wrapper as through the upstream chart alone.
package proof

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/truvity/cd/parity"
)

// upstreamKey maps a wrapper chart to the key its upstream chart's values live
// under. A chart with no upstream (cd-delivery, cd-pipeline) has nothing to be
// at parity with: its goldens and negative fixtures are its whole proof.
var upstreamKey = map[string]string{
	"cd-argocd":   "argo-cd",
	"cd-kargo":    "kargo",
	"cd-rollouts": "argo-rollouts",
}

func TestWrapperRendersTheUpstreamObjects(t *testing.T) {
	helm := parity.RequireHelm(t)

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}

	cases, err := filepath.Glob(filepath.Join(root, "tests", "cases", "*", "*", "values.yaml"))
	if err != nil {
		t.Fatal(err)
	}

	n := 0

	for _, values := range cases {
		caseDir := filepath.Dir(values)
		chart := filepath.Base(filepath.Dir(caseDir))

		key, ok := upstreamKey[chart]
		if !ok {
			continue
		}

		n++

		t.Run(chart+"/"+filepath.Base(caseDir), func(t *testing.T) {
			archives, err := filepath.Glob(filepath.Join(root, "charts", chart, "charts", key+"-*.tgz"))
			if err != nil || len(archives) == 0 {
				t.Fatalf("no vendored %s archive for %s", key, chart)
			}

			layers := presets(t, root, chart, caseDir)

			parity.Wrapper{
				Chart:        filepath.Join(root, "charts", chart),
				Upstream:     archives[0],
				Key:          key,
				Release:      chart,
				Namespace:    namespace(caseDir),
				Layers:       append(layers, values),
				OwnTemplates: chart + "/templates/",
			}.AssertZeroDiff(t, helm)
		})
	}

	if n == 0 {
		t.Fatal("no wrapper cases found")
	}
}

// presets are the values files a case names (tests/cases/<chart>/<case>/presets,
// one per line), layered before the case's own values the way an adopter does.
func presets(t *testing.T, root, chart, caseDir string) []string {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(caseDir, "presets")) //nolint:gosec // fixture path
	if os.IsNotExist(err) {
		return nil
	}

	if err != nil {
		t.Fatal(err)
	}

	var out []string

	for _, name := range strings.Fields(string(raw)) {
		out = append(out, filepath.Join(root, "charts", chart, "presets", name+".yaml"))
	}

	return out
}

func namespace(caseDir string) string {
	raw, err := os.ReadFile(filepath.Join(caseDir, "namespace")) //nolint:gosec // fixture path
	if err != nil || strings.TrimSpace(string(raw)) == "" {
		return "default"
	}

	return strings.TrimSpace(string(raw))
}
