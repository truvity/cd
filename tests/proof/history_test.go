package proof

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/truvity/cd/parity"
)

// applicationCharts are the charts that render Argo CD Applications: the
// Applications themselves come from cd-delivery, which cd-company and
// cd-project package.
var applicationCharts = []string{"cd-delivery", "cd-company", "cd-project"}

// revisionHistoryLimits is spec.revisionHistoryLimit of every Application an
// Objects holds, by key; an Application without one is listed as nil.
func revisionHistoryLimits(objects parity.Objects) map[string]any {
	out := map[string]any{}
	for key, doc := range objects {
		if doc["apiVersion"] != "argoproj.io/v1alpha1" || doc["kind"] != "Application" {
			continue
		}
		spec, _ := doc["spec"].(map[string]any)
		out[key] = spec["revisionHistoryLimit"]
	}
	return out
}

// TestEveryApplicationKeepsThreeRevisions holds every Application of every
// case to three history entries. Each entry of status.history keeps
// the sources' valuesObject inline, and with Argo CD's default of 10 one
// Application grew to 1.63 MB, past what etcd accepts in one write.
func TestEveryApplicationKeepsThreeRevisions(t *testing.T) {
	helm := parity.RequireHelm(t)
	seen := 0
	for _, chart := range applicationCharts {
		cases, err := filepath.Glob(fmt.Sprintf("../cases/%s/*/values.yaml", chart))
		if err != nil || len(cases) == 0 {
			t.Fatalf("%s cases: %v", chart, err)
		}
		for _, path := range cases {
			t.Run(chart+"/"+filepath.Base(filepath.Dir(path)), func(t *testing.T) {
				objects := parity.Render(t, helm, chart, "../../charts/"+chart, chart, companyValues(t, path))
				for key, limit := range revisionHistoryLimits(objects) {
					seen++
					if !parity.Equal(limit, 3) {
						t.Errorf("%s: spec.revisionHistoryLimit is %v, want 3", key, limit)
					}
				}
			})
		}
	}
	if seen == 0 {
		t.Fatal("no case rendered an Application")
	}
}

// TestPlatformSetsRevisionHistoryLimit: platform.revisionHistoryLimit
// replaces the default on every ring's Application.
func TestPlatformSetsRevisionHistoryLimit(t *testing.T) {
	helm := parity.RequireHelm(t)
	values := companyValues(t, "../cases/cd-delivery/everything/values.yaml")
	values["platform"].(map[string]any)["revisionHistoryLimit"] = 5
	limits := revisionHistoryLimits(parity.Render(t, helm, "delivery", "../../charts/cd-delivery", "delivery", values))
	if len(limits) == 0 {
		t.Fatal("rendered no Application")
	}
	for key, limit := range limits {
		if !parity.Equal(limit, 5) {
			t.Errorf("%s: spec.revisionHistoryLimit is %v, want 5", key, limit)
		}
	}
}
