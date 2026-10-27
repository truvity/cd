package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, root, path, body string) string {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCLIExplicitApplicationsAndRecordedSource(t *testing.T) {
	root := t.TempDir()
	write(t, root, "checkout/Chart.yaml", "apiVersion: v2\nname: local\nversion: 1.0.0\n")
	write(t, root, "checkout/templates/main.yaml", "{{ if .Values.refuse }}{{ fail \"CLI child refused\" }}{{ end }}kind: ConfigMap\n")
	checkout := filepath.Join(root, "checkout")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir = checkout
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q")
	git("add", ".")
	git("-c", "user.name=Example", "-c", "user.email=example@example.com", "commit", "-qm", "fixture")
	revision := git("rev-parse", "HEAD")
	mappings := write(t, root, "sources.yaml", "- repoURL: https://git.example.com/example-org/platform.git\n  targetRevision: deploy\n  root: checkout\n  revision: "+revision+"\n")
	appBody := "apiVersion: argoproj.io/v1alpha1\nkind: Application\nmetadata: {name: local}\nspec:\n  source:\n    repoURL: https://git.example.com/example-org/platform.git\n    targetRevision: deploy\n    path: .\n"
	app := write(t, root, "root.yaml", appBody)
	args := []string{"-application", app, "-application", app, "-sources", mappings, "-cache", t.TempDir()}
	var report bytes.Buffer
	if err := run(args, &report); err != nil {
		t.Fatalf("CLI: %v\n%s", err, report.String())
	}
	if !strings.Contains(report.String(), revision) || !strings.Contains(report.String(), "2 rendered ok") {
		t.Fatal(report.String())
	}
	write(t, root, "root.yaml", appBody+"    helm:\n      valuesObject: {refuse: true}\n")
	report.Reset()
	if err := run(args, &report); !errors.Is(err, errFailures) || !strings.Contains(report.String(), "CLI child refused") {
		t.Fatalf("CLI failed render: %v\n%s", err, report.String())
	}
}

func TestCLIRefusesIncompleteOrAmbiguousInputs(t *testing.T) {
	for _, args := range [][]string{
		{}, {"-application", "root.yaml"}, {"-sources", "sources.yaml"},
		{"-application", "root.yaml", "-sources", "sources.yaml", "-cluster", "devel"},
		{"-self-repo", "example-org/repo", "-jobs", "0"},
		{"-self-repo", "example-org/repo", "unexpected"},
	} {
		if err := run(args, io.Discard); err == nil {
			t.Errorf("accepted incomplete CLI inputs: %v", args)
		}
	}
	root := t.TempDir()
	for _, body := range []string{"- repoURL: example\n  typo: x\n", "[]\n---\n[]\n"} {
		path := write(t, root, "sources.yaml", body)
		if _, err := readSourceMappings(path); err == nil {
			t.Errorf("accepted invalid mappings: %s", body)
		}
	}
	path := write(t, root, "root.yaml", "kind: ConfigMap\n")
	if _, err := readApplications([]string{path}); err == nil {
		t.Fatal("accepted no Applications")
	}
}
