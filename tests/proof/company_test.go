package proof

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/truvity/cd/chartgate"
	"github.com/truvity/cd/parity"
	"go.yaml.in/yaml/v3"
)

func companyValues(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var values map[string]any
	if err := yaml.Unmarshal(raw, &values); err != nil {
		t.Fatal(err)
	}
	return values
}

func TestCompanyAdapterEqualsDeliveryComposition(t *testing.T) {
	helm := parity.RequireHelm(t)
	cases, err := filepath.Glob("../cases/cd-company/*/values.yaml")
	if err != nil || len(cases) == 0 {
		t.Fatalf("company cases: %v", err)
	}
	for _, path := range cases {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			values := companyValues(t, path)
			products := map[string]any{}
			facts, _ := values["products"].(map[string]any)
			payloads, _ := values["values"].(map[string]any)
			for service, pin := range values["pins"].(map[string]any) {
				if pin == "" {
					continue
				}
				product := maps.Clone(facts[service].(map[string]any))
				product["pin"] = pin
				if payload, ok := payloads[service]; ok {
					product["values"] = payload
				}
				products[service] = product
			}
			delivery := map[string]any{"products": products}
			if platform, ok := values["platform"]; ok {
				delivery["platform"] = platform
			}
			want := parity.Render(t, helm, "delivery", "../../charts/cd-delivery", "delivery", delivery)
			got := parity.Render(t, helm, "company", "../../charts/cd-company", "company", values)
			parity.AssertSame(t, want, got)
			// Decode refuses duplicates as well: the packaged dependency must not
			// render a second copy of any of the Applications.
			if strings.Contains(path, "/dormant/") && len(got) != 0 {
				t.Fatalf("dormant rendered %d objects", len(got))
			}
		})
	}
}

// The chart's development version remains 0.0.0, so dependency metadata alone
// cannot catch a stale archive after delivery templates or schemas change.
// Check the actual packaged application chart against its single source tree.
func TestCompanyDependencyMatchesDeliverySource(t *testing.T) {
	assertPackaged(t, "../../charts/cd-company/charts/cd-delivery-*.tgz", "just vendor cd-company", map[string]string{
		"cd-delivery/": "../../charts/cd-delivery",
	})
}

// assertPackaged holds the one archive matching pattern to its source trees:
// every regular file under an archive prefix (the longest that matches) is
// byte-identical to the file at the same path of that prefix's source tree,
// Chart.yaml equal as metadata, and every source file is packaged, except a
// source's own vendored charts/ (Helm packages a dependency as a directory,
// which is a prefix of its own).
func assertPackaged(t *testing.T, pattern, hint string, roots map[string]string) {
	t.Helper()
	archives, err := filepath.Glob(pattern)
	if err != nil || len(archives) != 1 {
		t.Fatalf("want one packaged dependency matching %s, got %v: %v", pattern, archives, err)
	}
	f, err := os.Open(archives[0])
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	zr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = zr.Close() }()
	tr := tar.NewReader(zr)
	seen := map[string]map[string]bool{}
	for prefix := range roots {
		seen[prefix] = map[string]bool{}
	}
	for {
		header, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if header.Typeflag != tar.TypeReg {
			t.Fatalf("unexpected archive entry %s", header.Name)
		}
		prefix := ""
		for candidate := range roots {
			if strings.HasPrefix(header.Name, candidate) && len(candidate) > len(prefix) {
				prefix = candidate
			}
		}
		path, ok := strings.CutPrefix(header.Name, prefix)
		if prefix == "" || !ok || !filepath.IsLocal(path) || seen[prefix][path] {
			t.Fatalf("unexpected or duplicate archive path %q", header.Name)
		}
		seen[prefix][path] = true
		source := roots[prefix]
		want, err := os.ReadFile(filepath.Join(source, path))
		if err != nil {
			t.Fatalf("%s: %v; run %s", header.Name, err, hint)
		}
		got, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if path == "Chart.yaml" || path == "Chart.lock" {
			// Helm normalizes chart metadata when packaging it, and stamps a
			// packaged lock file's `generated` with a fixed time.
			var wantMetadata, gotMetadata map[string]any
			if err := yaml.Unmarshal(want, &wantMetadata); err != nil {
				t.Fatal(err)
			}
			if err := yaml.Unmarshal(got, &gotMetadata); err != nil {
				t.Fatal(err)
			}
			if path == "Chart.lock" {
				delete(wantMetadata, "generated")
				delete(gotMetadata, "generated")
			}
			if !parity.Equal(wantMetadata, gotMetadata) {
				t.Fatalf("packaged %s metadata differs from %s; run %s", header.Name, source, hint)
			}
		} else if !bytes.Equal(want, got) {
			t.Errorf("packaged %s differs from %s; run %s", header.Name, source, hint)
		}
	}
	for prefix, source := range roots {
		if err := filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(source, path)
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if rel == "charts" {
					return filepath.SkipDir
				}
				return nil
			}
			if !seen[prefix][filepath.ToSlash(rel)] {
				t.Errorf("%s file %s missing from the packaged dependency; run %s", source, rel, hint)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestCompanyPayloadMergesOnlyBusinessConfiguration(t *testing.T) {
	helm := parity.RequireHelm(t)
	values := companyValues(t, "../cases/cd-company/merge/values.yaml")
	apps, err := chartgate.ParseApplications(parity.Template(t, helm, "company", "../../charts/cd-company", values))
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		if app.Name != "devel-example-service" {
			continue
		}
		payload := app.Sources[0].Helm.ValuesObject
		product := payload["product"].(map[string]any)
		nested := product["nested"].(map[string]any)
		if product["greeting"] != "company" || product["preserved"] != "platform" || nested["first"] != "platform" || nested["second"] != "company" || nested["shared"] != "company" {
			t.Fatalf("business configuration merge: %+v", product)
		}
		if payload["route"].(map[string]any)["hostname"] != "example-service.example.com" || payload["tier"] != "primary" {
			t.Fatalf("lost platform facts: %+v", payload)
		}
		return
	}
	t.Fatal("application ring missing")
}

func TestCompanyRefusesPlatformOwnedPathsEvenForDormantServices(t *testing.T) {
	helm := parity.RequireHelm(t)
	paths := []string{
		"cloud", "events", "identity", "route.enabled", "route.parentRef", "route.parentRefs", "route.sectionName", "route.surfaces", "route.faro", "serviceAccount", "tier", "pin", "chart", "repository", "interface", "appProject", "installName", "availability", "database", "tls", "archive", "otel", "access", "identityProviders", "alerts",
		"postgres.platformOwned", "postgres.instances", "postgres.storage", "postgres.runtimeRole", "postgres.runtimePasswordSecret", "postgres.runtimePassword", "postgres.labels", "postgres.scheduling", "postgres.backup", "postgres.serverTLS",
		"web.faro", "observability.dashboards",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), helm, "template", "company", "../../charts/cd-company", "--set", "services[0]=example-service", "--set-string", "pins.example-service=", "--set", fmt.Sprintf("values.example-service.app.%s=override", path))
			out, err := cmd.CombinedOutput()
			if err == nil || !strings.Contains(string(out), "values.example-service.app."+path+" is platform-owned") {
				t.Fatalf("ownership refusal: %v\n%s", err, out)
			}
		})
	}
}

func TestCompanyMayRefineItsRouteButNotItsAttachment(t *testing.T) {
	helm := parity.RequireHelm(t)
	for _, path := range []string{"route.hostname", "route.hostnames", "route.paths", "route.rules"} {
		t.Run(path, func(t *testing.T) {
			cmd := exec.CommandContext(t.Context(), helm, "template", "company", "../../charts/cd-company", "--set", "services[0]=example-service", "--set-string", "pins.example-service=", "--set", fmt.Sprintf("values.example-service.app.%s=ok", path))
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("an unreserved route key was refused: %v\n%s", err, out)
			}
		})
	}
	values := companyValues(t, "../cases/cd-company/route/values.yaml")
	apps, err := chartgate.ParseApplications(parity.Template(t, helm, "company", "../../charts/cd-company", values))
	if err != nil {
		t.Fatal(err)
	}
	for _, app := range apps {
		if app.Name != "devel-example-service" {
			continue
		}
		route := app.Sources[0].Helm.ValuesObject["route"].(map[string]any)
		parent, _ := route["parentRef"].(map[string]any)
		if route["hostname"] != "shop.example.com" || route["enabled"] != true || parent["name"] != "example" || route["paths"] == nil {
			t.Fatalf("route merge: %+v", route)
		}
		return
	}
	t.Fatal("application ring missing")
}
