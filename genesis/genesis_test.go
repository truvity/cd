package genesis

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestChartArgs pins the genesis helm invocation: the cd-argocd chart, the
// nested override. An unprefixed `server.httproute.enabled=false` would be
// silently ignored by the wrapper (its subchart key is `argo-cd`), leaving the
// HTTPRoute on before the Gateway API CRDs exist.
func TestChartArgs(t *testing.T) {
	t.Parallel()

	args := ChartArgs("argocd", "/repo/values-argocd.yaml", "/chart/cd-argocd")

	if args[0] != "/chart/cd-argocd" {
		t.Errorf("chart = %q, want the unpacked cd-argocd chart", args[0])
	}

	if !slices.Contains(args, "argo-cd.server.httproute.enabled=false") {
		t.Errorf("args %v lack the nested override argo-cd.server.httproute.enabled=false", args)
	}

	if slices.Contains(args, "server.httproute.enabled=false") {
		t.Errorf("args %v carry the un-nested override, which the wrapper chart ignores", args)
	}

	preset := filepath.Join("/chart/cd-argocd", "presets", "health.yaml")

	// The Application layers the preset first and the values file after it
	// (which wins), and genesis must do the same.
	if p, v := slices.Index(args, preset), slices.Index(args, "/repo/values-argocd.yaml"); p < 0 || v < 0 || p > v {
		t.Errorf("args %v must pass the health preset %s before the values file", args, preset)
	}

	if n := slices.Index(args, "-n"); n < 0 || args[n+1] != "argocd" {
		t.Errorf("args %v do not select the namespace", args)
	}
}

func TestRepoCredsSecretYAML(t *testing.T) {
	t.Parallel()

	c := Config{Namespace: "argocd", RepoCredsSecret: "repo-creds-x", RepoURL: "https://example.test/o/r.git"}
	got := RepoCredsSecretYAML(c, RepoCreds{AppID: "1", InstallationID: "2", PrivateKey: "line1\nline2"})

	for _, want := range []string{
		"name: repo-creds-x\n", "namespace: argocd\n", "argocd.argoproj.io/secret-type: repo-creds",
		"url: https://example.test/o/r.git\n", `githubAppID: "1"`, `githubAppInstallationID: "2"`,
		"githubAppPrivateKey: |\n    line1\n    line2\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("secret lacks %q:\n%s", want, got)
		}
	}
}

func TestClusterSecretYAML(t *testing.T) {
	t.Parallel()

	got := ClusterSecretYAML(Config{Namespace: "argocd", ClusterName: "mgmt"})

	for _, want := range []string{"name: cluster-mgmt\n", "namespace: argocd\n", "secret-type: cluster", "  name: mgmt\n", "server: https://kubernetes.default.svc"} {
		if !strings.Contains(got, want) {
			t.Errorf("cluster secret lacks %q:\n%s", want, got)
		}
	}
}

// TestChartArgsLayersThePresetsInOrder: named presets replace the default
// health-only layer, in the given order, all before the values file.
func TestChartArgsLayersThePresetsInOrder(t *testing.T) {
	t.Parallel()

	args := ChartArgs("argocd", "/repo/v.yaml", "/c", "health", "ha", "sso-only")

	var files []string

	for i, a := range args {
		if a == "-f" {
			files = append(files, args[i+1])
		}
	}

	want := []string{"/c/presets/health.yaml", "/c/presets/ha.yaml", "/c/presets/sso-only.yaml", "/repo/v.yaml"}
	if !slices.Equal(files, want) {
		t.Errorf("value layers = %v, want %v", files, want)
	}
}
