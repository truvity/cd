// Package genesis is the imperative bootstrap of an Argo CD installation:
// the one moment before Argo CD exists to deliver itself. It seeds the
// repository-credentials Secret, installs the cd-argocd chart with Helm, and
// applies the cluster's root Application, so that from the first sync on Argo CD
// delivers everything else, itself included.
//
// Every step is idempotent: it checks the current state first and skips what
// is already done, so a failed run is repeated, never repaired by hand.
//
// What this package does not know is where an installation keeps its
// particulars: which cluster, which repository, where the GitHub App
// credentials are stored, how the break-glass kubeconfig is built. They are
// the fields of Config and the arguments of Run; the caller is the wiring.
package genesis

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type (
	// Config is one installation's genesis.
	Config struct {
		// Kubeconfig is the file every kubectl and helm call is pinned to
		// (KUBECONFIG), so genesis never depends on, or changes, the
		// operator's ambient context.
		Kubeconfig string
		// Namespace is the Argo CD namespace (created when absent).
		Namespace string
		// ReleaseName is the Helm release of Argo CD.
		ReleaseName string
		// Chart is the OCI reference of the cd-argocd chart, ChartVersion its version.
		Chart        string
		ChartVersion string
		// ValuesFile is the values file layered after the chart's own health
		// preset, the same one the self-management Application uses.
		ValuesFile string
		// RepoURL is the git repository the credentials are for, and
		// RepoCredsSecret the name of the Secret that holds them.
		RepoURL         string
		RepoCredsSecret string
		// ClusterName is the name the local cluster is registered under in
		// Argo CD (destination.name), RootApplication the root Application
		// whose presence ends genesis, and RootManifest the file that
		// applies it.
		ClusterName     string
		RootApplication string
		RootManifest    string
	}

	// RepoCreds are the GitHub App credentials Argo CD reads the repository with.
	RepoCreds struct {
		AppID          string
		InstallationID string
		PrivateKey     string
	}
)

const setFlag = "--set"

// Run seeds the repository credentials, installs Argo CD and applies the root
// Application, in that order.
func Run(ctx context.Context, logger *slog.Logger, c Config, creds RepoCreds) error {
	if err := ensureRepoCreds(ctx, logger, c, creds); err != nil {
		return fmt.Errorf("seed repo-creds secret: %w", err)
	}

	logger.InfoContext(ctx, "argocd genesis: repo-creds secret ensured")

	if err := ensureHelm(ctx, logger, c); err != nil {
		return fmt.Errorf("install argocd helm: %w", err)
	}

	logger.InfoContext(ctx, "argocd genesis: helm release ensured")

	if err := ensureRootApp(ctx, logger, c); err != nil {
		return fmt.Errorf("apply root application: %w", err)
	}

	logger.InfoContext(ctx, "argocd genesis: complete")

	return nil
}

func ensureRepoCreds(ctx context.Context, logger *slog.Logger, c Config, creds RepoCreds) error {
	// Check if the secret already exists.
	checkCmd := c.kubeCmd(ctx, "kubectl", "get", "secret", c.RepoCredsSecret,
		"-n", c.Namespace,
		"--ignore-not-found", "-o", "name",
	)

	checkOut, err := checkCmd.Output()
	if err == nil && strings.TrimSpace(string(checkOut)) != "" {
		logger.InfoContext(ctx, "repo-creds secret already exists, skipping")
		return nil
	}

	// Create namespace first (idempotent).
	nsCmd := c.kubeCmd(ctx, "kubectl", "create", "namespace", c.Namespace,
		"--dry-run=client", "-o", "yaml",
	)

	var nsYAML bytes.Buffer

	nsCmd.Stdout = &nsYAML
	nsCmd.Stderr = os.Stderr

	if err := nsCmd.Run(); err != nil {
		return fmt.Errorf("generate namespace YAML: %w", err)
	}

	applyNS := c.kubeCmd(ctx, "kubectl", "apply", "-f", "-")
	applyNS.Stdin = &nsYAML
	applyNS.Stdout = os.Stdout
	applyNS.Stderr = os.Stderr

	if err := applyNS.Run(); err != nil {
		return fmt.Errorf("apply argocd namespace: %w", err)
	}

	applyCmd := c.kubeCmd(ctx, "kubectl", "apply", "-f", "-")
	applyCmd.Stdin = strings.NewReader(RepoCredsSecretYAML(c, creds))
	applyCmd.Stdout = os.Stdout
	applyCmd.Stderr = os.Stderr

	if err := applyCmd.Run(); err != nil {
		return fmt.Errorf("kubectl apply repo-creds secret: %w", err)
	}

	return nil
}

// RepoCredsSecretYAML is the repository-credentials Secret genesis applies.
func RepoCredsSecretYAML(c Config, creds RepoCreds) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: %s
  namespace: %s
  labels:
    argocd.argoproj.io/secret-type: repo-creds
type: Opaque
stringData:
  type: git
  url: %s
  githubAppID: "%s"
  githubAppInstallationID: "%s"
  githubAppPrivateKey: |
%s
`, c.RepoCredsSecret, c.Namespace, c.RepoURL, creds.AppID, creds.InstallationID, indentMultiline(creds.PrivateKey, 4))
}

func ensureHelm(ctx context.Context, logger *slog.Logger, c Config) error {
	// Check if already installed.
	statusCmd := c.kubeCmd(ctx, "helm", "status", c.ReleaseName,
		"-n", c.Namespace,
	)
	statusCmd.Stdout = nil
	statusCmd.Stderr = nil

	if err := statusCmd.Run(); err == nil {
		// Already installed — upgrade to ensure version matches.
		logger.InfoContext(ctx, "argocd helm release exists, upgrading to ensure version match",
			slog.String("version", c.ChartVersion),
		)
	}

	// helm upgrade --install (idempotent: installs or upgrades).
	// The chart's presets are files inside it, so the genesis install works
	// from the pulled chart rather than the OCI reference.
	chartDir, cleanup, err := PullChart(ctx, c.Chart, c.ChartVersion)
	if err != nil {
		return err
	}
	defer cleanup()

	args := append([]string{"upgrade", "--install", c.ReleaseName}, ChartArgs(c.Namespace, c.ValuesFile, chartDir)...)
	args = append(args, "--create-namespace")

	helmCmd := c.kubeCmd(ctx, "helm", args...)
	helmCmd.Stdout = os.Stdout
	helmCmd.Stderr = os.Stderr

	logger.InfoContext(ctx, "running helm upgrade --install",
		slog.String("chart", c.Chart),
		slog.String("version", c.ChartVersion),
	)

	if err := helmCmd.Run(); err != nil {
		return fmt.Errorf("helm upgrade --install argocd: %w", err)
	}

	return nil
}

// PullChart pulls and unpacks the cd-argocd chart at version into a
// temporary directory and returns the chart's own directory, plus the cleanup.
func PullChart(ctx context.Context, chart, version string) (chartDir string, cleanup func(), err error) {
	tmp, err := os.MkdirTemp("", "argocd-genesis-chart-")
	if err != nil {
		return "", nil, fmt.Errorf("temp dir for the genesis chart: %w", err)
	}

	cleanup = func() { _ = os.RemoveAll(tmp) }

	//nolint:gosec // fixed binary, caller-derived args
	cmd := exec.CommandContext(ctx, "helm", "pull", chart, "--version", version, "--untar", "--untardir", tmp)
	if out, runErr := cmd.CombinedOutput(); runErr != nil {
		cleanup()

		return "", nil, fmt.Errorf("helm pull %s %s: %w\n%s", chart, version, runErr, out)
	}

	return filepath.Join(tmp, "cd-argocd"), cleanup, nil
}

// ChartArgs is everything helm takes after `upgrade --install <release>`
// (or `template <release>`, which a caller's test runs) for the genesis install.
// chartDir is the unpacked cd-argocd chart (PullChart).
//
// It installs the SAME chart the Argo CD self-management Application syncs,
// with the SAME value layers, in the same order: the chart's health preset
// (presets/health.yaml), then the values file, so the first sync starts with
// identical config, critically the resource health customizations that gate
// the sync order, which live in the preset and are not in the values file.
// The chart nests the upstream argo-cd values one level under `argo-cd:`, so
// the genesis-only override is prefixed too:
//   - HTTPRoute off: the Gateway API CRDs do not exist yet.
//   - OIDC config is unresolved until the OIDC application runs; admin login
//     (enabled in the values file) covers the gap.
func ChartArgs(namespace, valuesFile, chartDir string) []string {
	return []string{
		chartDir,
		"-n", namespace,
		"-f", filepath.Join(chartDir, "presets", "health.yaml"),
		"-f", valuesFile,
		setFlag, "argo-cd.server.httproute.enabled=false",
	}
}

func ensureRootApp(ctx context.Context, logger *slog.Logger, c Config) error {
	// Check if the root Application already exists.
	checkCmd := c.kubeCmd(ctx, "kubectl", "get", "application", c.RootApplication,
		"-n", c.Namespace,
		"--ignore-not-found", "-o", "name",
	)

	checkOut, err := checkCmd.Output()
	if err == nil && strings.TrimSpace(string(checkOut)) != "" {
		logger.InfoContext(ctx, "root Application already exists, skipping",
			slog.String("name", c.RootApplication),
		)

		return nil
	}

	// Create the cluster Secret (name alias for Argo CD destinations).
	// Must exist before any Application references destination.name.
	logger.InfoContext(ctx, "creating the cluster Secret (name alias)",
		slog.String("cluster", c.ClusterName),
	)

	secretCmd := c.kubeCmd(ctx, "kubectl", "apply", "-f", "-")
	secretCmd.Stdin = strings.NewReader(ClusterSecretYAML(c))
	secretCmd.Stdout = os.Stdout
	secretCmd.Stderr = os.Stderr

	if err := secretCmd.Run(); err != nil {
		return fmt.Errorf("create cluster secret: %w", err)
	}

	// Apply the cluster's root Application (installs the platform from here).
	logger.InfoContext(ctx, "applying the root manifest",
		slog.String("path", c.RootManifest),
	)

	clusterCmd := c.kubeCmd(ctx, "kubectl", "apply", "-f", c.RootManifest)
	clusterCmd.Stdout = os.Stdout
	clusterCmd.Stderr = os.Stderr

	if err := clusterCmd.Run(); err != nil {
		return fmt.Errorf("apply root manifest: %w", err)
	}

	logger.InfoContext(ctx, "genesis complete: the root Application rolls out the rest from here")

	return nil
}

// ClusterSecretYAML is the Argo CD cluster Secret that registers the local
// cluster under c.ClusterName.
func ClusterSecretYAML(c Config) string {
	return fmt.Sprintf(`apiVersion: v1
kind: Secret
metadata:
  name: cluster-%[1]s
  namespace: %[2]s
  labels:
    argocd.argoproj.io/secret-type: cluster
type: Opaque
stringData:
  name: %[1]s
  server: https://kubernetes.default.svc
  config: |
    {
      "tlsClientConfig": {
        "insecure": false
      }
    }
`, c.ClusterName, c.Namespace)
}

// kubeCmd builds an exec.Cmd with KUBECONFIG pinned to c.Kubeconfig.
func (c Config) kubeCmd(ctx context.Context, name string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, name, args...) //nolint:gosec // fixed binaries (kubectl/helm), caller-derived args
	cmd.Env = append(os.Environ(), "KUBECONFIG="+c.Kubeconfig)

	return cmd
}

func indentMultiline(s string, spaces int) string {
	prefix := strings.Repeat(" ", spaces)
	lines := strings.Split(s, "\n")

	var result []string

	for _, line := range lines {
		result = append(result, prefix+line)
	}

	return strings.Join(result, "\n")
}
