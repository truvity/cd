package parity

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type (
	// SourceMatch selects a source of an Argo CD Application: the Application
	// whose metadata.name has suffix AppSuffix, and in it the source whose repoURL
	// has suffix ChartSuffix.
	SourceMatch struct {
		AppSuffix   string
		ChartSuffix string
		// FirstAppOnly stops at the first Application with the suffix even when
		// it has no such source (the Application is unique); otherwise the scan
		// goes on to the next Application with the suffix.
		FirstAppOnly bool
	}
)

// ApplicationValues is the valuesObject of the matched source of an
// Application in a roles-cluster render (what Argo CD hands the chart), read
// in document order. sourceFound is whether a matching source exists (the cluster has
// moved); appFound whether an Application with the suffix exists at all.
func ApplicationValues(t testing.TB, rolesCluster []byte, label string, m SourceMatch) (values map[string]any, sourceFound, appFound bool) {
	t.Helper()

	dec := yaml.NewDecoder(bytes.NewReader(rolesCluster))

	for index := 0; ; index++ {
		var app map[string]any

		err := dec.Decode(&app)
		if errors.Is(err, io.EOF) {
			return nil, false, appFound
		}

		if err != nil {
			t.Fatalf("%s: YAML document %d does not decode: %v", label, index, err)
		}

		if app["kind"] != "Application" {
			continue
		}

		if name, _ := Map(app["metadata"])["name"].(string); !strings.HasSuffix(name, m.AppSuffix) {
			continue
		}

		appFound = true

		for _, src := range List(Map(app["spec"])["sources"]) {
			source := Map(src)
			if repo, _ := source["repoURL"].(string); strings.HasSuffix(repo, m.ChartSuffix) {
				return Map(Map(source["helm"])["valuesObject"]), true, true
			}
		}

		if m.FirstAppOnly {
			return nil, false, true
		}
	}
}
