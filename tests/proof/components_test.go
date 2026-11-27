package proof

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/truvity/cd/parity"
	"go.yaml.in/yaml/v3"
)

// kargoObjects is every Warehouse and Stage of a cd-pipeline render: the
// Warehouse a Stage requests Freight from, and the Stages it takes Freight
// from ("" for straight from the Warehouse).
func kargoObjects(t *testing.T, values map[string]any) (warehouses []string, origin map[string]string, upstream map[string][]string) {
	t.Helper()
	origin, upstream = map[string]string{}, map[string][]string{}
	for _, docs := range renderPipeline(t, values) {
		for _, doc := range docs {
			var obj map[string]any
			if err := yaml.Unmarshal([]byte(doc), &obj); err != nil || obj == nil {
				continue
			}
			name, _ := parity.Map(obj["metadata"])["name"].(string)
			switch obj["kind"] {
			case "Warehouse":
				warehouses = append(warehouses, name)
			case "Stage":
				req := parity.Map(parity.List(parity.Map(obj["spec"])["requestedFreight"])[0])
				origin[name], _ = parity.Map(req["origin"])["name"].(string)
				upstream[name] = parity.Strings(parity.Map(req["sources"])["stages"])
			}
		}
	}
	sort.Strings(warehouses)
	return warehouses, origin, upstream
}

// TestComponentsShareOneWarehouseEach proves the shape of a project of
// components (cd-pipeline/components): one Warehouse per component, and per
// further source of a component, shared by every customer that takes it and
// none per customer; each customer's chain of each of its components along
// stageOrder, from that component's Warehouse and from its own Stages only;
// a customer that takes one component has no Stage of another; and a lane
// customer is one Stage on the first Stage of stageOrder, on its component's
// Warehouse of the lane's source, from which no Stage promotes.
func TestComponentsShareOneWarehouseEach(t *testing.T) {
	values := companyValues(t, "../cases/cd-pipeline/components/values.yaml")
	warehouses, origin, upstream := kargoObjects(t, values)

	if want := []string{"sso-demo", "sso-keycloak", "sso-keycloak-master", "sso-portal"}; !slices.Equal(warehouses, want) {
		t.Fatalf("Warehouses %v, want %v", warehouses, want)
	}
	order := []string{"devel", "stage", "prod"}
	takes := map[string][]string{
		"generic": {"portal", "keycloak"}, "globex": {"portal", "keycloak"}, "initech": {"portal", "keycloak"},
		"umbrella": {"portal", "keycloak"}, "hooli": {"portal", "keycloak"}, "internal": {"demo"},
	}
	want := map[string]bool{}
	for customer, components := range takes {
		for _, component := range []string{"demo", "portal", "keycloak"} {
			for i, stage := range order {
				name := stage + "-" + customer + "-" + component
				if !slices.Contains(components, component) {
					if _, ok := origin[name]; ok {
						t.Errorf("Stage %s: %s does not take %s", name, customer, component)
					}
					continue
				}
				want[name] = true
				if got := origin[name]; got != "sso-"+component {
					t.Errorf("Stage %s requests Freight from %s, want the shared sso-%s", name, got, component)
				}
				var from []string
				if i > 0 {
					from = []string{order[i-1] + "-" + customer + "-" + component}
				}
				if !slices.Equal(upstream[name], from) {
					t.Errorf("Stage %s takes Freight from %v, want %v", name, upstream[name], from)
				}
			}
		}
	}
	lane := "devel-globex-master-keycloak"
	want[lane] = true
	if origin[lane] != "sso-keycloak-master" || len(upstream[lane]) != 0 {
		t.Errorf("lane Stage %s requests %s from %v, want sso-keycloak-master straight from the Warehouse", lane, origin[lane], upstream[lane])
	}
	for name, from := range upstream {
		if !want[name] {
			t.Errorf("Stage %s is not one of the project's chains", name)
		}
		if slices.Contains(from, lane) {
			t.Errorf("Stage %s promotes from the lane %s: a lane never promotes onward", name, lane)
		}
	}
	if len(origin) != len(want) {
		t.Errorf("%d Stages, want %d", len(origin), len(want))
	}
}

// TestWithoutComponentsNothingMoves proves the components axis is off for
// every project that does not set it: each cd-pipeline and cd-project case
// without `components` renders, byte for byte, the golden it had before the
// axis existed (no golden of such a case moved when it was added), and its
// render names no {component} token.
func TestWithoutComponentsNothingMoves(t *testing.T) {
	helm := parity.RequireHelm(t)
	for _, chart := range []string{"cd-pipeline", "cd-project"} {
		cases, err := filepath.Glob(filepath.Join("..", "cases", chart, "*", "values.yaml"))
		if err != nil || len(cases) == 0 {
			t.Fatalf("no %s cases: %v", chart, err)
		}
		checked := 0
		for _, path := range cases {
			if setsComponents(companyValues(t, path)) {
				continue
			}
			name := filepath.Base(filepath.Dir(path))
			t.Run(chart+"/"+name, func(t *testing.T) {
				render := exec.CommandContext(t.Context(), helm, "template", chart, filepath.Join("..", "..", "charts", chart), "--namespace", namespace(filepath.Dir(path)), "-f", path)
				normalize := exec.CommandContext(t.Context(), filepath.Join("..", "..", "hack", "golden-normalize.py"))
				pipe, err := render.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				normalize.Stdin = pipe
				var stderr strings.Builder
				render.Stderr = &stderr
				if err := render.Start(); err != nil {
					t.Fatal(err)
				}
				out, err := normalize.Output()
				if werr := render.Wait(); werr != nil {
					t.Fatalf("helm template: %v\n%s", werr, stderr.String())
				}
				if err != nil {
					t.Fatalf("normalize: %v", err)
				}
				golden, err := os.ReadFile(filepath.Join("..", "golden", chart, name+".yaml"))
				if err != nil {
					t.Fatal(err)
				}
				if got := strings.TrimRight(string(out), "\n"); got != strings.TrimRight(string(golden), "\n") {
					t.Errorf("render moved from its golden\n  %s", parity.FirstDifference(string(golden), got))
				}
				if strings.Contains(string(out), "{component}") {
					t.Error("the render names a {component} token")
				}
			})
			checked++
		}
		if checked == 0 {
			t.Fatalf("no %s case without components: the proof checked nothing", chart)
		}
	}
}

// setsComponents is whether a case's values give any project components:
// cd-pipeline's projects[].components, cd-project's project.components.
func setsComponents(values map[string]any) bool {
	if len(parity.Map(parity.Map(values["project"])["components"])) > 0 {
		return true
	}
	for _, p := range parity.List(values["projects"]) {
		if len(parity.Map(parity.Map(p)["components"])) > 0 {
			return true
		}
	}
	return false
}

// pinTargets is, for each Stage of a render, the pin file its promotion
// writes and the keys it writes there.
func pinTargets(t *testing.T, render string) map[string][2]string {
	t.Helper()
	out := map[string][2]string{}
	for _, doc := range strings.Split(render, "\n---") {
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(doc), &obj); err != nil || obj == nil || obj["kind"] != "Stage" {
			continue
		}
		name, _ := parity.Map(obj["metadata"])["name"].(string)
		for _, step := range parity.List(parity.Map(parity.Map(parity.Map(obj["spec"])["promotionTemplate"])["spec"])["steps"]) {
			st := parity.Map(step)
			if st["uses"] != "yaml-update" {
				continue
			}
			cfg := parity.Map(st["config"])
			key, _ := parity.Map(parity.List(cfg["updates"])[0])["key"].(string)
			path, _ := cfg["path"].(string)
			out[name] = [2]string{path, key}
		}
	}
	return out
}

// TestLanePinsLiveBelowTheirCustomer proves the lane's folder: a lane
// customer <customer>-<lane> writes its pin below its customer's folder,
// <customer>/<lane>/<component>/, while its Stage and pin key keep the lane
// customer's name. cd-pipeline's default pin file for components is
// versions/{customerBase}/{lanePath}{component}/{stage}.yaml; cd-project's
// case spells the platform's rule out with the same tokens.
func TestLanePinsLiveBelowTheirCustomer(t *testing.T) {
	helm := parity.RequireHelm(t)
	for _, tc := range []struct {
		chart string
		want  map[string][2]string
	}{
		{"cd-pipeline", map[string][2]string{
			"devel-globex-master-keycloak": {"./repo/versions/globex/master/keycloak/devel.yaml", "pins.devel-globex-master-keycloak"},
			"devel-globex-keycloak":        {"./repo/versions/globex/keycloak/devel.yaml", "pins.devel-globex-keycloak"},
			"prod-internal-demo":           {"./repo/versions/internal/demo/prod.yaml", "pins.prod-internal-demo"},
		}},
		{"cd-project", map[string][2]string{
			"devel-globex-master-keycloak": {"./repo/projects/sso/globex/master/keycloak/devel/pin.yaml", "pins.devel-globex-master-keycloak"},
			"devel-globex-keycloak":        {"./repo/projects/sso/globex/keycloak/devel/pin.yaml", "pins.devel-globex-keycloak"},
			"prod-internal-demo":           {"./repo/projects/sso/internal/demo/prod/pin.yaml", "pins.prod-internal-demo"},
		}},
	} {
		t.Run(tc.chart, func(t *testing.T) {
			values := companyValues(t, filepath.Join("..", "cases", tc.chart, "components", "values.yaml"))
			got := pinTargets(t, string(parity.Template(t, helm, tc.chart, filepath.Join("..", "..", "charts", tc.chart), values)))
			for stage, want := range tc.want {
				if got[stage] != want {
					t.Errorf("Stage %s writes %v, want %v", stage, got[stage], want)
				}
			}
			for stage, target := range got {
				if strings.Contains(target[0], "globex-master") {
					t.Errorf("Stage %s writes %s: a lane's folder is below its customer's", stage, target[0])
				}
			}
		})
	}
}
