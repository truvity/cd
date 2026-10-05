package parity

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"go.yaml.in/yaml/v3"
)

// RequireHelm is the helm binary, or skips the test where there is none (the
// proofs run inside devbox and in CI).
func RequireHelm(t testing.TB) string {
	t.Helper()

	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Skip("helm not on PATH — parity proof SKIPPED (run inside devbox)")
	}

	return helm
}

type (
	// Chart names a chart a proof renders: vendored, packaged, in the repository
	// (so the proof runs in CI without a network), or live from a checkout.
	Chart struct {
		// EnvDir is the environment variable (e.g. MYREPO_<CHART>_CHART_DIR) that
		// points the proof at a live checkout instead of the vendored archive.
		EnvDir string
		// Archive is the vendored archive's path, relative to the testdata
		// directory of the caller's test package, e.g.
		// "tenancy-chart/tenancy-0.0.0.tgz". Each vendored directory carries a
		// VENDORED_FROM that says where the archive was packaged from.
		Archive string
	}
)

// Path is the live checkout when EnvDir is set, else the vendored archive.
func (c Chart) Path(testdata string) string {
	if dir := os.Getenv(c.EnvDir); c.EnvDir != "" && dir != "" {
		return dir
	}

	return filepath.Join(testdata, c.Archive)
}

// Template renders a chart (an archive or a directory) with values as
// `helm template release chart -f values`, and returns the stream.
func Template(t testing.TB, helm, release, chart string, values map[string]any) []byte {
	t.Helper()

	raw, err := yaml.Marshal(values)
	if err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	return Helm(t, helm, "template", release, chart, "-f", path)
}

// Helm runs helm with args and returns its stdout; a failure fails the test
// with helm's stderr.
func Helm(t testing.TB, helm string, args ...string) []byte {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), helm, args...)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("helm %v: %v\n%s", args, err, stderr.String())
	}

	return stdout.Bytes()
}

// Render is Template decoded into Objects.
func Render(t testing.TB, helm, release, chart, source string, values map[string]any) Objects {
	t.Helper()

	return Decode(t, source, Template(t, helm, release, chart, values))
}
