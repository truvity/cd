package proof

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/truvity/cd/parity"
	"go.yaml.in/yaml/v3"
)

// projectKinds is everything cd-project may render: the platform renders the
// Kargo Project, the write-back credentials, verification and access.
var projectKinds = []string{
	"argoproj.io/v1alpha1 Application",
	"kargo.akuity.io/v1alpha1 Warehouse",
	"kargo.akuity.io/v1alpha1 Stage",
	"kargo.akuity.io/v1alpha1 ProjectConfig",
}

func projectCases(t *testing.T) []string {
	t.Helper()
	cases, err := filepath.Glob("../cases/cd-project/*/values.yaml")
	if err != nil || len(cases) == 0 {
		t.Fatalf("project cases: %v", err)
	}
	return cases
}

// documents splits a helm template stream into its documents, keyed like
// parity.Key, each kept as written less helm's own `# Source:` line, so two
// renders compare byte for byte.
func documents(t *testing.T, raw []byte) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, doc := range strings.Split("\n"+string(raw), "\n---\n") {
		var lines []string
		for _, line := range strings.Split(doc, "\n") {
			if !strings.HasPrefix(line, "# Source: ") {
				lines = append(lines, line)
			}
		}
		text := strings.TrimSpace(strings.Join(lines, "\n"))
		if text == "" {
			continue
		}
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(text), &obj); err != nil {
			t.Fatalf("%v\n%s", err, text)
		}
		key := parity.Key(obj)
		if _, dup := out[key]; dup {
			t.Fatalf("%s rendered twice", key)
		}
		out[key] = text
	}
	return out
}

// projectCustomers is a project's customers (project.customers) as name and
// chart, the chart project.source.chart unless the customer names its own.
func projectCustomers(project map[string]any) [][2]string {
	var out [][2]string
	for _, entry := range parity.List(project["customers"]) {
		chart := project["source"].(map[string]any)["chart"].(string)
		if name, ok := entry.(string); ok {
			out = append(out, [2]string{name, chart})
			continue
		}
		c := parity.Map(entry)
		if own, ok := c["chart"].(string); ok {
			chart = own
		}
		out = append(out, [2]string{c["name"].(string), chart})
	}
	return out
}

// stageNameTemplate is the platform's pipeline.graph.stageName, default
// {stage}-{customer}.
func stageNameTemplate(values map[string]any) string {
	if name, ok := parity.Map(parity.Map(values["pipeline"])["graph"])["stageName"].(string); ok {
		return name
	}
	return "{stage}-{customer}"
}

// companySlice is what cd-company is given for one cluster of a project: the
// cluster's platform block, the project as its one service, and the
// cluster's pin, payload and product facts under the project's name, the
// facts with the project's chart source and kind in them, as cd-project
// injects them. With project.customers each customer is a service of its
// own, <project>-<customer>, whose pin, payload and facts are keyed by its
// Stage's name, with the customer and the project's name as the middle of
// its Application names in its facts.
func companySlice(values map[string]any, cluster string) map[string]any {
	out := installsSlice(values, cluster)
	addCompanions(values, cluster, out)
	return out
}

// addCompanions puts each companion of project.companions beside each
// install of the slice: the service <service>-<companion>, keyed
// <key>-<companion> in pins, values and products, a static site of the
// company's artifact in the install's namespace (unless the platform names
// one), with the customer and <project>-<companion> as its Application
// names' middle where the install is a customer's.
func addCompanions(values map[string]any, cluster string, out map[string]any) {
	project := values["project"].(map[string]any)
	name := project["name"].(string)
	companions := parity.Map(project["companions"])
	if len(companions) == 0 {
		return
	}
	type install struct{ key, service, customer string }
	var installs []install
	if customers := projectCustomers(project); len(customers) > 0 {
		for _, c := range customers {
			key := strings.NewReplacer("{stage}", cluster, "{customer}", c[0]).Replace(stageNameTemplate(values))
			installs = append(installs, install{key, name + "-" + c[0], c[0]})
		}
	} else {
		installs = append(installs, install{cluster, name, ""})
	}
	names := slices.Sorted(maps.Keys(companions))
	for _, in := range installs {
		for _, cn := range names {
			key, service := in.key+"-"+cn, in.service+"-"+cn
			out["services"] = append(out["services"].([]any), service)
			if pin, ok := parity.Map(values["pins"])[key]; ok {
				out["pins"].(map[string]any)[service] = pin
			}
			if payload, ok := parity.Map(values["values"])[key]; ok {
				out["values"].(map[string]any)[service] = payload
			}
			if product, ok := parity.Map(values["products"])[key]; ok {
				facts := maps.Clone(product.(map[string]any))
				facts["kind"] = "static-site"
				facts["artifact"] = map[string]any{"ref": strings.TrimPrefix(parity.Map(companions[cn])["artifact"].(string), "oci://")}
				if _, ok := facts["namespace"]; !ok {
					facts["namespace"] = in.service
				}
				if in.customer != "" {
					facts["customer"] = in.customer
					facts["applicationName"] = name + "-" + cn
				}
				out["products"].(map[string]any)[service] = facts
			}
		}
	}
}

// installsSlice is companySlice without the companions.
func installsSlice(values map[string]any, cluster string) map[string]any {
	project := values["project"].(map[string]any)
	name := project["name"].(string)
	if customers := projectCustomers(project); len(customers) > 0 {
		out := map[string]any{
			"platform": values["platform"].(map[string]any)[cluster],
			"services": []any{},
			"pins":     map[string]any{},
			"values":   map[string]any{},
			"products": map[string]any{},
		}
		for _, c := range customers {
			service := name + "-" + c[0]
			key := strings.NewReplacer("{stage}", cluster, "{customer}", c[0]).Replace(stageNameTemplate(values))
			out["services"] = append(out["services"].([]any), service)
			if pin, ok := parity.Map(values["pins"])[key]; ok {
				out["pins"].(map[string]any)[service] = pin
			}
			if payload, ok := parity.Map(values["values"])[key]; ok {
				out["values"].(map[string]any)[service] = payload
			}
			if product, ok := parity.Map(values["products"])[key]; ok {
				cut := strings.LastIndex(c[1], "/")
				facts := maps.Clone(product.(map[string]any))
				facts["repository"] = c[1][:cut]
				facts["chart"] = c[1][cut+1:]
				facts["kind"] = "product"
				if kind, ok := project["source"].(map[string]any)["kind"]; ok {
					facts["kind"] = kind
				}
				facts["customer"] = c[0]
				facts["applicationName"] = name
				out["products"].(map[string]any)[service] = facts
			}
		}
		return out
	}
	out := map[string]any{
		"platform": values["platform"].(map[string]any)[cluster],
		"services": []any{name},
		"pins":     map[string]any{},
		"values":   map[string]any{},
		"products": map[string]any{},
	}
	if pin, ok := parity.Map(values["pins"])[cluster]; ok {
		out["pins"] = map[string]any{name: pin}
	}
	if payload, ok := parity.Map(values["values"])[cluster]; ok {
		out["values"] = map[string]any{name: payload}
	}
	if product, ok := parity.Map(values["products"])[cluster]; ok {
		// The chart source is the company's: oci://<repository>/<chart>, from
		// the source the cluster's Stage takes (project.pipeline.stageSources),
		// else project.source.
		chart := project["source"].(map[string]any)["chart"].(string)
		if src, ok := parity.Map(parity.Map(project["pipeline"])["stageSources"])[cluster]; ok {
			chart = parity.Map(parity.Map(project["sources"])[src.(string)])["chart"].(string)
		}
		cut := strings.LastIndex(chart, "/")
		facts := maps.Clone(product.(map[string]any))
		facts["repository"] = chart[:cut]
		facts["chart"] = chart[cut+1:]
		// So is the kind of product (project.source.kind, default product).
		facts["kind"] = "product"
		if kind, ok := project["source"].(map[string]any)["kind"]; ok {
			facts["kind"] = kind
		}
		out["products"] = map[string]any{name: facts}
	}
	return out
}

// TestProjectChildrenAreCompanyRenders proves there is no second renderer:
// the Applications of every cd-project case are, document for document and
// byte for byte, what cd-company renders for each cluster of the project
// given that cluster's slice of the inputs.
func TestProjectChildrenAreCompanyRenders(t *testing.T) {
	helm := parity.RequireHelm(t)
	for _, path := range projectCases(t) {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			values := companyValues(t, path)
			want := map[string]string{}
			for _, cluster := range values["project"].(map[string]any)["clusters"].([]any) {
				rendered := parity.Template(t, helm, "company", "../../charts/cd-company", companySlice(values, cluster.(string)))
				for key, doc := range documents(t, rendered) {
					if _, dup := want[key]; dup {
						t.Fatalf("%s rendered for two clusters", key)
					}
					want[key] = doc
				}
			}
			got := map[string]string{}
			for key, doc := range documents(t, parity.Template(t, helm, "project", "../../charts/cd-project", values)) {
				if strings.HasPrefix(key, "Application ") {
					got[key] = doc
				}
			}
			if len(want) == 0 {
				t.Fatal("cd-company rendered nothing to compare with")
			}
			for key, doc := range want {
				if got[key] != doc {
					t.Errorf("%s differs from cd-company's render\n  %s", key, parity.FirstDifference(doc, got[key]))
				}
			}
			for key := range got {
				if _, ok := want[key]; !ok {
					t.Errorf("%s is not rendered by cd-company", key)
				}
			}
		})
	}
}

// withPlatformRange is a company's range within the platform's: both ANDed
// (a space), an OR on either side distributed; the company's alone where the
// platform gives none.
func withPlatformRange(own, platform string) string {
	if platform == "" {
		return own
	}

	var out []string

	for _, a := range strings.Split(own, "||") {
		for _, b := range strings.Split(platform, "||") {
			out = append(out, strings.TrimSpace(a)+" "+strings.TrimSpace(b))
		}
	}

	return strings.Join(out, " || ")
}

// TestProjectPipelineIsCdPipelines proves the Kargo objects are cd-pipeline's
// own: the chart renders, byte for byte, the objects cd-pipeline renders for
// the same project with createProject false and the platform's settings.
func TestProjectPipelineIsCdPipelines(t *testing.T) {
	helm := parity.RequireHelm(t)
	for _, path := range projectCases(t) {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			values := companyValues(t, path)
			project := values["project"].(map[string]any)
			source := project["source"].(map[string]any)
			pipeline := project["pipeline"].(map[string]any)
			chart := map[string]any{"repoURL": source["chart"], "semver": source["semver"]}
			for _, key := range []string{"versionPrefix", "subscribeAs"} {
				if v, ok := source[key]; ok {
					chart[key] = v
				}
			}
			// The platform's range (pipeline.project.semver), ANDed with
			// every range the company gives its Warehouses.
			platformRange, _ := parity.Map(parity.Map(values["pipeline"])["project"])["semver"].(string)
			chart["semver"] = withPlatformRange(chart["semver"].(string), platformRange)
			p := map[string]any{"name": project["name"], "createProject": false, "chart": chart}
			for _, key := range []string{"stageOrder", "autoPromote", "upstreams", "stageSources"} {
				if v, ok := pipeline[key]; ok {
					p[key] = v
				}
			}
			// What the platform says about the Kargo Project (pipeline.project)
			// is the project map's own keys: the filled name with the project's
			// name as the slug, the vendor and the customer, and the Stages
			// either side holds back.
			kargo := parity.Map(parity.Map(values["pipeline"])["project"])
			held := parity.List(pipeline["noAutoPromote"])
			for _, stage := range parity.List(kargo["noAutoPromote"]) {
				if !slices.Contains(held, stage) {
					held = append(held, stage)
				}
			}
			if len(held) > 0 {
				p["noAutoPromote"] = held
			}
			fill := []string{"{project}", project["name"].(string), "{product}", project["name"].(string)}
			for _, key := range []string{"vendor", "customer"} {
				if v, ok := kargo[key]; ok {
					p[key] = v
					fill = append(fill, "{"+key+"}", v.(string))
				}
			}
			if name, ok := kargo["name"]; ok {
				p["name"] = strings.NewReplacer(fill...).Replace(name.(string))
				p["slug"] = project["name"]
				// The Warehouse keeps the project's own name.
				p["warehouse"] = map[string]any{"name": project["name"]}
			}
			// Each customer is a source of its own: its chart, at the
			// project's semver.
			if customers := projectCustomers(project); len(customers) > 0 {
				var list []any
				for i, c := range customers {
					entry := map[string]any{"name": c[0], "chart": map[string]any{"repoURL": c[1], "semver": withPlatformRange(source["semver"].(string), platformRange)}}
					if own, ok := parity.Map(parity.List(project["customers"])[i])["companions"]; ok {
						entry["companions"] = own
					}
					list = append(list, entry)
				}
				p["customers"] = list
			}
			// The companions: image subscriptions pinned by digest, and their
			// Applications watched beside the project's.
			if companions := parity.Map(project["companions"]); len(companions) > 0 {
				subs := map[string]any{}
				apps := []any{project["name"]}
				for _, cn := range slices.Sorted(maps.Keys(companions)) {
					co := parity.Map(companions[cn])
					subs[cn] = map[string]any{"repoURL": strings.TrimPrefix(co["artifact"].(string), "oci://"), "semver": co["semver"], "subscribeAs": "image", "pin": "digest"}
					apps = append(apps, project["name"].(string)+"-"+cn)
				}
				p["companions"] = subs
				p["applications"] = apps
			}
			if named, ok := project["sources"]; ok {
				sources := map[string]any{}
				for sn, sv := range parity.Map(named) {
					s := parity.Map(sv)
					sc := map[string]any{"repoURL": s["chart"], "semver": withPlatformRange(s["semver"].(string), platformRange)}
					for _, key := range []string{"versionPrefix", "subscribeAs"} {
						if v, ok := s[key]; ok {
							sc[key] = v
						}
					}
					sources[sn] = sc
				}
				p["sources"] = sources
			}
			settings := map[string]any{}
			for k, v := range parity.Map(values["pipeline"]) {
				if k != "project" { // read by cd-project, not a cd-pipeline setting
					settings[k] = v
				}
			}
			promotion := map[string]any{}
			for k, v := range parity.Map(settings["promotion"]) {
				promotion[k] = v
			}
			if _, ok := promotion["pinKey"]; !ok {
				promotion["pinKey"] = "pins.{stage}"
				// With customers a pin is keyed by its Stage's name.
				if len(projectCustomers(project)) > 0 {
					promotion["pinKey"] = "pins." + stageNameTemplate(values)
				}
			}
			settings["promotion"] = promotion
			// cd-project names the promoted-version check and never renders
			// its objects: the same as cd-pipeline with objects false.
			settings["promotedVersion"] = map[string]any{"objects": false}
			settings["projects"] = []any{p}
			want := documents(t, parity.Template(t, helm, "pipeline", "../../charts/cd-pipeline", settings))
			got := map[string]string{}
			for key, doc := range documents(t, parity.Template(t, helm, "project", "../../charts/cd-project", values)) {
				if !strings.HasPrefix(key, "Application ") {
					got[key] = doc
				}
			}
			if len(want) == 0 {
				t.Fatal("cd-pipeline rendered nothing to compare with")
			}
			for key, doc := range want {
				if got[key] != doc {
					t.Errorf("%s differs from cd-pipeline's render\n  %s", key, parity.FirstDifference(doc, got[key]))
				}
			}
			for key := range got {
				if _, ok := want[key]; !ok {
					t.Errorf("%s is not rendered by cd-pipeline", key)
				}
			}
		})
	}
}

// TestProjectRendersOnlyItsKinds holds the whole render of every case to
// Applications, Warehouses, Stages and ProjectConfigs.
func TestProjectRendersOnlyItsKinds(t *testing.T) {
	helm := parity.RequireHelm(t)
	for _, path := range projectCases(t) {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			objects := parity.Render(t, helm, "project", "../../charts/cd-project", "cd-project", companyValues(t, path))
			if len(objects) == 0 {
				t.Fatal("rendered nothing")
			}
			for key, doc := range objects {
				kind := doc["apiVersion"].(string) + " " + doc["kind"].(string)
				if !slices.Contains(projectKinds, kind) {
					t.Errorf("%s: kind %s is not one cd-project may render", key, kind)
				}
			}
		})
	}
}

// The packaged dependencies of cd-project are its sibling charts as they are:
// cd-company with cd-delivery inside it, and cd-pipeline.
func TestProjectDependenciesMatchTheirSources(t *testing.T) {
	assertPackaged(t, "../../charts/cd-project/charts/cd-company-*.tgz", "just vendor cd-project", map[string]string{
		"cd-company/":                    "../../charts/cd-company",
		"cd-company/charts/cd-delivery/": "../../charts/cd-delivery",
	})
	assertPackaged(t, "../../charts/cd-project/charts/cd-pipeline-*.tgz", "just vendor cd-project", map[string]string{
		"cd-pipeline/": "../../charts/cd-pipeline",
	})
}

// TestProjectGuardRefusesOtherKinds proves the render-time guard behind the
// schema: with schema validation skipped, platform settings that would make
// cd-pipeline render credentials, sealed as the platform's, still fail the
// render instead of rendering those objects. (Verification cannot reach the
// guard: TestProjectVerificationIsNamedNotRendered.)
func TestProjectGuardRefusesOtherKinds(t *testing.T) {
	helm := parity.RequireHelm(t)
	digest := regexp.MustCompile(`received sha256 ([0-9a-f]{64})`)
	for name, set := range map[string]func(pipeline map[string]any){
		"credentials": func(pipeline map[string]any) {
			git := parity.Map(pipeline["git"])
			git["credentials"] = map[string]any{
				"secretStoreRef": map[string]any{"kind": "ClusterSecretStore", "name": "secrets"},
				"remoteKey":      "pipeline/github-app",
				"properties":     map[string]any{"appID": "app-id", "installationID": "installation-id", "privateKey": "private-key"},
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			values := companyValues(t, "../cases/cd-project/minimal/values.yaml")
			set(parity.Map(values["pipeline"]))
			render := func() (string, error) {
				raw, err := yaml.Marshal(values)
				if err != nil {
					t.Fatal(err)
				}
				file := filepath.Join(t.TempDir(), "values.yaml")
				if err := os.WriteFile(file, raw, 0o600); err != nil {
					t.Fatal(err)
				}
				out, err := exec.CommandContext(t.Context(), helm, "template", "x", "../../charts/cd-project", "--skip-schema-validation", "-f", file).CombinedOutput()
				return string(out), err
			}
			out, _ := render()
			m := digest.FindStringSubmatch(out)
			if m == nil {
				t.Fatalf("no digest to seal with:\n%s", out)
			}
			values["platformDigest"] = m[1]
			out, err := render()
			if err == nil || !strings.Contains(out, "cd-project: the render holds an object of kind") {
				t.Fatalf("the guard let the render through: %v\n%s", err, out)
			}
		})
	}
}

// TestProjectVerificationIsNamedNotRendered proves that the promoted-version
// check reaches a company's render only by name: with the platform's graph
// turning it on, and even with platform settings that ask cd-pipeline to
// render its objects (schema validation skipped), every Stage names
// `<project>-<stage>-promoted-version` and the render holds none of its
// ServiceAccount, Role, RoleBinding, ConfigMap or AnalysisTemplate.
func TestProjectVerificationIsNamedNotRendered(t *testing.T) {
	helm := parity.RequireHelm(t)
	values := companyValues(t, "../cases/cd-project/full/values.yaml")
	pipeline := parity.Map(values["pipeline"])
	graph := parity.Map(pipeline["graph"])
	if graph == nil {
		graph = map[string]any{}
		pipeline["graph"] = graph
	}
	graph["promotedVersion"] = true
	pipeline["promotedVersion"] = map[string]any{"objects": true, "image": "registry.example.com/tools/curl:8", "jqImage": "registry.example.com/tools/jq:1"}

	digest := regexp.MustCompile(`received sha256 ([0-9a-f]{64})`)
	render := func() (string, error) {
		raw, err := yaml.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(t.TempDir(), "values.yaml")
		if err := os.WriteFile(file, raw, 0o600); err != nil {
			t.Fatal(err)
		}
		out, err := exec.CommandContext(t.Context(), helm, "template", "x", "../../charts/cd-project", "--skip-schema-validation", "-f", file).CombinedOutput()
		return string(out), err
	}
	out, _ := render()
	m := digest.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("no digest to seal with:\n%s", out)
	}
	values["platformDigest"] = m[1]
	out, err := render()
	if err != nil {
		t.Fatalf("render: %v\n%s", err, out)
	}

	project := parity.Map(values["project"])
	name, _ := project["name"].(string)
	stages := 0
	for _, doc := range strings.Split(out, "\n---") {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil || obj == nil {
			continue
		}
		switch kind, _ := obj["kind"].(string); kind {
		case "ServiceAccount", "Role", "RoleBinding", "ConfigMap", "AnalysisTemplate":
			t.Errorf("the render holds a %s of the check: %v", kind, parity.Map(obj["metadata"])["name"])
		case "Stage":
			stages++
			stage, _ := parity.Map(obj["metadata"])["name"].(string)
			verification := parity.Map(parity.Map(obj["spec"])["verification"])
			want := name + "-" + stage + "-promoted-version"
			templates := parity.List(verification["analysisTemplates"])
			if len(templates) != 1 || parity.Map(templates[0])["name"] != want {
				t.Errorf("Stage %s names %v, want only %s", stage, templates, want)
			}
		}
	}
	if stages == 0 {
		t.Fatal("no Stage rendered: the proof checked nothing")
	}
}

// TestProjectDigestIsPlainJSON proves the platformDigest recipe of
// docs/reference.md#cd-project from outside Helm: sha256, in hex, of Go's
// encoding/json of the four platform keys (map keys sorted, compact) equals
// the digest every case is sealed with, so a platform written in Go computes
// what the chart checks.
func TestProjectDigestIsPlainJSON(t *testing.T) {
	for _, path := range projectCases(t) {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			values := companyValues(t, path)
			sealed := map[string]any{}
			for _, key := range []string{"clusters", "pipeline", "platform", "products"} {
				sealed[key] = values[key]
			}
			raw, err := json.Marshal(sealed)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(raw)
			if got := hex.EncodeToString(sum[:]); got != values["platformDigest"] {
				t.Fatalf("sha256 of %s\n= %s, sealed %v", raw, got, values["platformDigest"])
			}
		})
	}
}
