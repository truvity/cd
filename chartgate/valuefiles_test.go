package chartgate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A remote chart is rendered with the repository's own value files (`$values/`)
// and without the ones the chart ships inside itself; a local chart takes both.
func TestValueFilesOfARemoteChartAreTheRepositorys(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "values.yaml"), []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	g := &Gate{Root: root, work: t.TempDir()}

	var src Source

	src.Helm.ValueFiles = []string{"presets/health.yaml", "$values/values.yaml"}

	remote, err := g.valueFiles(Application{}, src, false)
	if err != nil {
		t.Fatal(err)
	}

	if len(remote) != 1 || !strings.HasSuffix(remote[0], "values.yaml") || !strings.HasPrefix(remote[0], root) {
		t.Errorf("remote value files = %v, want the repository's file alone", remote)
	}

	src.Helm.ValueFiles = []string{"$values/missing.yaml"}

	if _, err := g.valueFiles(Application{}, src, false); err == nil {
		t.Error("a missing repository value file must fail the gate, as it fails Argo CD")
	}
}
