package chartgate

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	opsRepo     = "https://git.example.com/example-org/platform.git"
	companyRepo = "https://git.example.com/example-org/company.git"
	chartRepo   = "oci://registry.example.com/charts/"
)

func writeFixture(t *testing.T, root, path, content string) {
	t.Helper()
	path = filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func command(t *testing.T, root, name string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), name, args...)
	cmd.Dir = root
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v\n%s", name, args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func checkout(t *testing.T, repo, requested string, files map[string]string) SourceMapping {
	t.Helper()
	root := t.TempDir()
	for path, body := range files {
		writeFixture(t, root, path, body)
	}
	command(t, root, "git", "init", "-q")
	command(t, root, "git", "add", ".")
	command(t, root, "git", "-c", "user.name=Example", "-c", "user.email=example@example.com", "commit", "-qm", "fixture")
	return SourceMapping{RepoURL: repo, TargetRevision: requested, Root: root, Revision: command(t, root, "git", "rev-parse", "HEAD")}
}

// Only the registry transport is replaced. Every chart is packaged and templated
// by real Helm, including the vendored cd-company -> cd-delivery composition.
type fixtureRegistry struct {
	gate                *Gate
	archives, helm, log string
}

func registry(t *testing.T) fixtureRegistry {
	t.Helper()
	helm, err := exec.LookPath("helm")
	if err != nil {
		t.Fatal("helm is required (run inside devbox):", err)
	}
	archives, log := t.TempDir(), filepath.Join(t.TempDir(), "pulls")
	script := fmt.Sprintf(`case "$1" in
pull)
  chart="${2##*/}"; shift 2
  while [ "$#" -gt 0 ]; do
    case "$1" in --version) version="$2"; shift 2;; --destination) dest="$2"; shift 2;; *) shift;; esac
  done
  echo "$chart@$version" >> %q
  cp %q/"$chart-$version.tgz" "$dest/" ;;
*) exec %q "$@" ;;
esac`, log, archives, helm)
	return fixtureRegistry{&Gate{Helm: fakeBin(t, "helm", script), CacheDir: t.TempDir(), RequireECR: true}, archives, helm, log}
}

func (r fixtureRegistry) pack(t *testing.T, name, version, template, schema string, extra map[string]string) {
	t.Helper()
	root := t.TempDir()
	writeFixture(t, root, "Chart.yaml", "apiVersion: v2\nname: "+name+"\nversion: "+version+"\n")
	writeFixture(t, root, "templates/main.yaml", template)
	if schema != "" {
		writeFixture(t, root, "values.schema.json", schema)
	}
	for path, body := range extra {
		writeFixture(t, root, path, body)
	}
	r.packPath(t, root)
}

func (r fixtureRegistry) packPath(t *testing.T, root string) {
	t.Helper()
	command(t, ".", r.helm, "package", root, "--destination", r.archives)
}

func assertResults(t *testing.T, results []Result, okCount, failCount int, detail string) {
	t.Helper()
	var report bytes.Buffer
	failures := Report(&report, results, time.Second)
	ok := 0
	for _, res := range results {
		if res.Status == StatusOK {
			ok++
		}
	}
	if failures != failCount || ok != okCount || (detail != "" && !strings.Contains(report.String(), detail)) {
		t.Fatalf("want %d ok, %d failures containing %q; got:\n%s\n%+v", okCount, failCount, detail, report.String(), results)
	}
}

func remoteApp(name, chart, version string, helm Helm, refs ...Source) Application {
	return Application{Name: name, Namespace: "example-service", Cluster: "devel", Sources: append([]Source{{RepoURL: chartRepo + chart, Path: ".", TargetRevision: version, Helm: helm}}, refs...)}
}

func TestExplicitInputsSelectCandidateAndLayerCompleteValues(t *testing.T) {
	r := registry(t)
	r.pack(t, "assert-values", "1.0.0", `{{- if or (ne .Values.winner "object") (ne .Values.marker "candidate") (ne .Release.Name "selected-release") (ne .Release.Namespace "selected-namespace") -}}{{ fail "wrong candidate, precedence or release context" }}{{- end -}}
{{- if not (and .Values.merged.shipped .Values.merged.platform .Values.merged.company) -}}{{ fail "lost merged values" }}{{- end -}}
{{- if hasKey .Values "inline" -}}{{ fail "valuesObject must replace the inline values block" }}{{- end -}}
{{- if or .Values.flag (ne (int .Values.zero) 0) (ne .Values.empty "") -}}{{ fail "lost false, zero or empty override" }}{{- end }}
kind: ConfigMap
apiVersion: v1
metadata: {name: selected}
`, "", map[string]string{"presets/base.yaml": "winner: shipped\nmerged: {shipped: true}\nflag: true\nzero: 2\nempty: nonempty\n"})
	ops := checkout(t, opsRepo, "platform-pin", map[string]string{"platform.yaml": "winner: platform\nmerged: {platform: true}\n"})
	candidate := checkout(t, companyRepo, "deploy", map[string]string{"values.yaml": "winner: candidate\nmarker: candidate\nmerged: {company: true}\n"})
	branch := checkout(t, companyRepo, "deploy", map[string]string{"values.yaml": "winner: branch\nmarker: branch\nmerged: {company: true}\n"})
	r.gate.Sources = []SourceMapping{ops, candidate}
	app := remoteApp("candidate-check", "assert-values", "1.0.0", Helm{
		ReleaseName:  "selected-release",
		ValueFiles:   []string{"presets/base.yaml", "$authority/platform.yaml", "$reviewed/values.yaml"},
		Values:       "winner: inline\ninline: true\n",
		ValuesObject: map[string]any{"winner": "object", "flag": false, "zero": 0, "empty": ""},
	}, Source{RepoURL: opsRepo, TargetRevision: ops.TargetRevision, Ref: "authority"}, Source{RepoURL: companyRepo, TargetRevision: "deploy", Ref: "reviewed"})
	app.Namespace = "selected-namespace"
	results := r.gate.RunApplications(t.Context(), []Application{app})
	assertResults(t, results, 1, 0, candidate.Revision)
	if len(results[0].Sources) != 2 {
		t.Fatalf("source report: %+v", results)
	}
	var report bytes.Buffer
	Report(&report, results, 0)
	if !strings.Contains(report.String(), ops.Revision) || strings.Contains(report.String(), branch.Revision) {
		t.Fatal(report.String())
	}
	// The requested deployment branch stays the same. Only the explicit
	// binding selects its immutable checkout; the gate never fetches a tip.
	r.gate.Sources[1] = branch
	assertResults(t, r.gate.RunApplications(t.Context(), []Application{app}), 0, 1, "wrong candidate")
}

func TestExplicitInputsRefAndMappingFailures(t *testing.T) {
	r := registry(t)
	r.pack(t, "leaf", "1.0.0", "kind: ConfigMap\n", "", nil)
	source := checkout(t, companyRepo, "deploy", map[string]string{"values.yaml": "x: true\n"})
	base := remoteApp("check", "leaf", "1.0.0", Helm{}, Source{RepoURL: companyRepo, TargetRevision: "deploy", Ref: "candidate"})
	tests := []struct {
		name, file, detail string
		ignore             bool
		mappings           []SourceMapping
		refs               []Source
	}{
		{"unknown ref even when optional", "$absent/values.yaml", "unresolved source ref", true, []SourceMapping{source}, nil},
		{"missing file", "$candidate/missing.yaml", "missing.yaml", false, []SourceMapping{source}, nil},
		{"missing shipped file", "presets/missing.yaml", "presets/missing.yaml", false, []SourceMapping{source}, nil},
		{"unmapped ref even when optional", "$candidate/values.yaml", "unmapped Git source", true, nil, nil},
		{"unmapped requested branch", "$candidate/values.yaml", "unmapped Git source", false, []SourceMapping{{RepoURL: source.RepoURL, TargetRevision: "other", Root: source.Root, Revision: source.Revision}}, nil},
		{"moving revision", "$candidate/values.yaml", "full immutable revision", false, []SourceMapping{{RepoURL: source.RepoURL, TargetRevision: "deploy", Root: source.Root, Revision: "deploy"}}, nil},
		{"wrong HEAD", "$candidate/values.yaml", "does not match revision", false, []SourceMapping{{RepoURL: source.RepoURL, TargetRevision: "deploy", Root: source.Root, Revision: strings.Repeat("a", 40)}}, nil},
		{"duplicate mapping", "$candidate/values.yaml", "duplicate source mapping", false, []SourceMapping{source, source}, nil},
		{"duplicate ref", "$candidate/values.yaml", "duplicate source ref", false, []SourceMapping{source}, []Source{base.Sources[1], base.Sources[1]}},
		{"escape checkout", "$candidate/../outside.yaml", "leaves the source", false, []SourceMapping{source}, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := base
			app.Sources = append([]Source(nil), base.Sources...)
			if tt.refs != nil {
				app.Sources = append(app.Sources[:1], tt.refs...)
			}
			app.Sources[0].Helm = Helm{ValueFiles: []string{tt.file}, IgnoreMissing: tt.ignore}
			r.gate.Sources = tt.mappings
			assertResults(t, r.gate.RunApplications(t.Context(), []Application{app}), 0, 1, tt.detail)
		})
	}
	r.gate.Sources = []SourceMapping{source}
	base.Sources[0].Helm = Helm{ValueFiles: []string{"$candidate/missing.yaml", "presets/missing.yaml", "$candidate/values.yaml"}, IgnoreMissing: true}
	assertResults(t, r.gate.RunApplications(t.Context(), []Application{base}), 1, 0, source.Revision)
}

func TestValuesObjectReplacesInlineValuesIncludingEmptyObject(t *testing.T) {
	r := registry(t)
	r.pack(t, "inline-selection", "1.0.0", `{{- if ne .Values.guard "valid" -}}{{ fail "invalid guard from chart defaults" }}{{- end }}
kind: ConfigMap
`, "", map[string]string{"values.yaml": "guard: invalid\n"})
	for _, tt := range []struct {
		name, object string
		ok, fail     int
	}{
		{"absent", "", 1, 0},
		{"empty", "      valuesObject: {}\n", 0, 1},
		{"unrelated", "      valuesObject: {unrelated: true}\n", 0, 1},
		{"override", "      valuesObject: {guard: valid}\n", 1, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			apps, err := ParseApplications([]byte("kind: Application\nmetadata: {name: inline-selection}\nspec:\n  source:\n    repoURL: " + chartRepo + "inline-selection\n    targetRevision: 1.0.0\n    path: .\n    helm:\n      values: 'guard: valid'\n" + tt.object))
			if err != nil {
				t.Fatal(err)
			}
			results := r.gate.RunApplications(t.Context(), apps)
			detail := ""
			if tt.fail != 0 {
				detail = "invalid guard from chart defaults"
			}
			assertResults(t, results, tt.ok, tt.fail, detail)
		})
	}
	inline := remoteApp("same-context", "inline-selection", "1.0.0", Helm{Values: "guard: valid\n"})
	empty := remoteApp("same-context", "inline-selection", "1.0.0", Helm{Values: "guard: valid\n", ValuesObject: map[string]any{}})
	assertResults(t, r.gate.RunApplications(t.Context(), []Application{inline, empty}), 1, 1, "invalid guard from chart defaults")
}

func TestRemoteCompanyTraversesActualPinsAndChildFailures(t *testing.T) {
	for _, failure := range []string{"", "schema", "template", "pull", "auth"} {
		t.Run("child-"+failure, func(t *testing.T) {
			r := registry(t)
			r.packPath(t, "../charts/cd-company")
			appTemplate := `{{- if ne .Values.config.marker "candidate" -}}{{ fail "missing child candidate values" }}{{- end }}
kind: ConfigMap
metadata: {name: application}
`
			schema := ""
			if failure == "schema" {
				schema = `{"type":"object","required":["missingRequiredChildValue"]}`
			}
			if failure == "template" {
				appTemplate = `{{ fail "child template refused values" }}`
			}
			r.pack(t, "example-service", "1.2.3", appTemplate, schema, nil)
			r.pack(t, "example-service-infra", "1.2.3", "kind: ConfigMap\nmetadata: {name: infrastructure}\n", "", nil)
			if failure != "pull" {
				r.pack(t, "example-worker", "2.0.0", "kind: ConfigMap\nmetadata: {name: worker}\n", "", nil)
			}
			r.pack(t, "example-worker-infra", "2.0.0", "kind: ConfigMap\nmetadata: {name: worker-infra}\n", "", nil)
			raw, err := os.ReadFile("../tests/cases/cd-company/minimal/values.yaml")
			if err != nil {
				t.Fatal(err)
			}
			var facts map[string]any
			if err := yaml.Unmarshal(raw, &facts); err != nil {
				t.Fatal(err)
			}
			delete(facts, "pins")
			delete(facts, "values")
			facts["services"] = []string{"example-service", "example-worker", "example-dormant"}
			products := facts["products"].(map[string]any)
			worker := map[string]any{"interface": 15, "hostname": "example-worker.example.com"}
			if failure == "auth" {
				worker["repository"] = "oci://" + testECR + "/charts"
			}
			products["example-worker"] = worker
			body, err := yaml.Marshal(facts)
			if err != nil {
				t.Fatal(err)
			}
			ops := checkout(t, opsRepo, "platform-pin", map[string]string{"facts.yaml": string(body)})
			company := checkout(t, companyRepo, "deploy", map[string]string{
				"pins.yaml":   "pins: {example-service: '1.2.3', example-worker: '2.0.0', example-dormant: ''}\n",
				"values.yaml": "values: {example-service: {app: {config: {marker: candidate}}}}\n",
			})
			r.gate.Sources = []SourceMapping{ops, company}
			r.gate.AWS = fakeBin(t, "aws", "echo 'fixture authentication denied' >&2; exit 1")
			app := remoteApp("company", "cd-company", "0.0.0", Helm{ValueFiles: []string{"$ops/facts.yaml", "$candidate/pins.yaml", "$candidate/values.yaml"}},
				Source{RepoURL: opsRepo, TargetRevision: ops.TargetRevision, Ref: "ops"},
				Source{RepoURL: companyRepo, TargetRevision: "deploy", Ref: "candidate"})
			results := r.gate.RunApplications(t.Context(), []Application{app})
			ok, fail, message := 5, 0, ""
			switch failure {
			case "schema":
				ok, fail, message = 4, 1, "missingRequiredChildValue"
			case "template":
				ok, fail, message = 4, 1, "child template refused values"
			case "pull":
				ok, fail, message = 4, 1, "helm pull:"
			case "auth":
				ok, fail, message = 3, 2, "ECR login required"
			}
			assertResults(t, results, ok, fail, message)
			for _, result := range results {
				if strings.Contains(result.App, "dormant") {
					t.Fatalf("dormant service checked: %+v", result)
				}
				if result.Status == StatusFail && result.App == "company" {
					t.Fatalf("wrapper should succeed: %+v", result)
				}
			}
			pulls, err := os.ReadFile(r.log)
			if err != nil {
				t.Fatal(err)
			}
			for _, pin := range []string{"cd-company@0.0.0", "example-service@1.2.3", "example-service-infra@1.2.3"} {
				if !strings.Contains(string(pulls), pin) {
					t.Fatalf("actual pin %s not pulled:\n%s", pin, pulls)
				}
			}
		})
	}
}

func appYAML(t *testing.T, app Application) string {
	t.Helper()
	doc := map[string]any{"apiVersion": "argoproj.io/v1alpha1", "kind": "Application", "metadata": map[string]any{"name": app.Name}, "spec": map[string]any{
		"destination": map[string]any{"name": app.Cluster, "namespace": app.Namespace}, "sources": app.Sources,
	}}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestTraversalPreservesDistinctContextsAndRepeatedChildFailures(t *testing.T) {
	r := registry(t)
	r.pack(t, "wrapper", "1.0.0", `{{ .Values.manifests }}`, "", nil)
	r.pack(t, "leaf", "1.0.0", `{{ if .Values.refuse }}{{ fail "distinct child context refused" }}{{ end }}kind: ConfigMap`, "", nil)
	good := remoteApp("same-child", "leaf", "1.0.0", Helm{ValuesObject: map[string]any{"refuse": false}})
	bad := remoteApp("same-child", "leaf", "1.0.0", Helm{ValuesObject: map[string]any{"refuse": true}})
	manifests := appYAML(t, good) + "---\n" + appYAML(t, bad) + "---\n" + appYAML(t, bad)
	parent := remoteApp("wrapper", "wrapper", "1.0.0", Helm{ValuesObject: map[string]any{"manifests": manifests}})
	assertResults(t, r.gate.RunApplications(t.Context(), []Application{parent}), 2, 1, "distinct child context refused")
	log, err := os.ReadFile(r.log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(log), "leaf@1.0.0") != 1 {
		t.Fatalf("pulls not deduplicated: %s", log)
	}
	// Repeated roots use the cached render outcome, including its failure.
	assertResults(t, r.gate.RunApplications(t.Context(), []Application{parent, parent}), 4, 2, "distinct child context refused")
}

func TestTraversalCyclesAndBounds(t *testing.T) {
	for _, mode := range []string{"cycle", "depth", "renders"} {
		t.Run(mode, func(t *testing.T) {
			r := registry(t)
			app := remoteApp("loop", "loop", "1.0.0", Helm{})
			child := appYAML(t, app)
			detail := "cycle detected"
			ok := 1
			if mode != "cycle" {
				child = strings.Replace(child, "name: loop\n", `name: {{ .Release.Name }}-next`+"\n", 1)
				ok = 2
				if mode == "depth" {
					r.gate.MaxDepth, detail = 2, "maximum depth 2"
				} else {
					r.gate.MaxRenders, detail = 2, "maximum render contexts 2"
				}
			}
			r.pack(t, "loop", "1.0.0", child, "", nil)
			assertResults(t, r.gate.RunApplications(t.Context(), []Application{app}), ok, 1, detail)
		})
	}
}

func TestBootstrapModeStillFollowsLocalAndRemoteCharts(t *testing.T) {
	r := registry(t)
	r.pack(t, "leaf", "1.0.0", "kind: ConfigMap\n", "", nil)
	child := remoteApp("remote-child", "leaf", "1.0.0", Helm{})
	root := t.TempDir()
	writeFixture(t, root, "charts/root/Chart.yaml", "apiVersion: v2\nname: root\nversion: 1.0.0\n")
	writeFixture(t, root, "charts/root/presets/local.yaml", "shipped: true\n")
	writeFixture(t, root, "values/root.yaml", "external: true\n")
	writeFixture(t, root, "charts/root/templates/main.yaml", `{{- if not (and .Values.shipped .Values.external) -}}{{ fail "missing local values" }}{{- end }}
`+appYAML(t, child))
	app := Application{Name: "bootstrap", Sources: []Source{
		{RepoURL: "https://github.com/example/repo.git", Path: "charts/root", TargetRevision: "master", Helm: Helm{ValueFiles: []string{"presets/local.yaml", "$values/values/root.yaml"}}},
		{RepoURL: "https://github.com/example/repo.git", Ref: "values", TargetRevision: "master"},
	}}
	writeFixture(t, root, "clusters/devel.yaml", appYAML(t, app))
	r.gate.Root, r.gate.SelfRepo = root, self
	assertResults(t, r.gate.Run(context.Background(), []string{"devel"}), 2, 0, "")
	assertResults(t, r.gate.Run(t.Context(), []string{"missing"}), 0, 1, "clusters/missing.yaml")
}

func TestExplicitMappedLocalChartUsesItsOwnCheckout(t *testing.T) {
	company := checkout(t, companyRepo, "deploy", map[string]string{"values.yaml": "candidate: true\n"})
	ops := checkout(t, opsRepo, "platform-pin", map[string]string{
		"charts/local/Chart.yaml":        "apiVersion: v2\nname: local\nversion: 1.0.0\n",
		"charts/local/presets/base.yaml": "shipped: true\n",
		"charts/shared.yaml":             "shared: true\n",
		"authority.yaml":                 "authority: true\n",
		"charts/local/templates/main.yaml": `{{- if not (and .Values.candidate .Values.shipped .Values.shared .Values.authority) -}}{{ fail "wrong checkout or chart-relative values" }}{{- end }}
kind: ConfigMap
`,
	})
	g := &Gate{Sources: []SourceMapping{ops, company}}
	app := Application{Name: "local", Sources: []Source{
		{RepoURL: opsRepo, TargetRevision: ops.TargetRevision, Path: "charts/local", Ref: "authority", Helm: Helm{ValueFiles: []string{"presets/base.yaml", "../shared.yaml", "$authority/authority.yaml", "$candidate/values.yaml"}}},
		{RepoURL: companyRepo, TargetRevision: "deploy", Ref: "candidate"},
	}}
	assertResults(t, g.RunApplications(t.Context(), []Application{app}), 1, 0, company.Revision)
}

func TestRefValueFileRejectsSymlinkEscape(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	writeFixture(t, outside, "values.yaml", "outside: true\n")
	if err := os.Symlink(outside, filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	g := &Gate{Sources: []SourceMapping{{RepoURL: companyRepo, Root: root, TargetRevision: "deploy"}}, work: t.TempDir()}
	src := Source{Helm: Helm{ValueFiles: []string{"$candidate/linked/values.yaml"}, IgnoreMissing: true}}
	app := Application{Sources: []Source{{RepoURL: companyRepo, TargetRevision: "deploy", Ref: "candidate"}}}
	if _, err := g.valueFiles(app, src, false, ""); err == nil || !strings.Contains(err.Error(), "via a symlink") {
		t.Fatalf("symlink escape: %v", err)
	}
}
