package chartgate

import (
	"archive/tar"
	"compress/gzip"
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

	remote, err := g.valueFiles(Application{}, src, false, "")
	if err != nil {
		t.Fatal(err)
	}

	if len(remote) != 1 || !strings.HasSuffix(remote[0], "values.yaml") || !strings.HasPrefix(remote[0], root) {
		t.Errorf("remote value files = %v, want the repository's file alone", remote)
	}

	src.Helm.ValueFiles = []string{"$values/missing.yaml"}

	if _, err := g.valueFiles(Application{}, src, false, ""); err == nil {
		t.Error("a missing repository value file must fail the gate, as it fails Argo CD")
	}
}

// With the pulled archive, a remote chart also takes the files it ships inside
// itself, layered in the order listed; a missing one fails unless the
// Application ignores missing value files.
func TestValueFilesOfARemoteChartIncludeTheShippedOnes(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "values.yaml"), []byte("a: 1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	tgz := filepath.Join(t.TempDir(), "demo-1.0.0.tgz")
	out, err := os.Create(tgz)
	if err != nil {
		t.Fatal(err)
	}

	zw := gzip.NewWriter(out)
	tw := tar.NewWriter(zw)

	body := []byte("preset: true\n")
	if err := tw.WriteHeader(&tar.Header{Name: "demo/presets/p.yaml", Mode: 0o600, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatal(err)
	}

	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}

	_ = tw.Close()
	_ = zw.Close()
	_ = out.Close()

	g := &Gate{Root: root, work: t.TempDir()}

	var src Source

	src.Helm.ValueFiles = []string{"presets/p.yaml", "$values/values.yaml"}

	files, err := g.valueFiles(Application{}, src, false, tgz)
	if err != nil {
		t.Fatal(err)
	}

	if len(files) != 2 || !strings.HasSuffix(files[0], "p.yaml") || !strings.HasSuffix(files[1], "values.yaml") {
		t.Fatalf("value files = %v, want the shipped preset then the repository's file", files)
	}

	got, err := os.ReadFile(files[0])
	if err != nil || string(got) != string(body) {
		t.Errorf("extracted preset = %q, %v", got, err)
	}

	src.Helm.ValueFiles = []string{"presets/missing.yaml"}
	if _, err := g.valueFiles(Application{}, src, false, tgz); err == nil {
		t.Error("a missing shipped value file must fail the gate, as it fails Argo CD")
	}

	src.Helm.IgnoreMissing = true
	if files, err := g.valueFiles(Application{}, src, false, tgz); err != nil || len(files) != 0 {
		t.Errorf("ignoreMissingValueFiles: files = %v, err = %v", files, err)
	}
}
