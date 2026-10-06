package parity

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

const (
	stream = `---
apiVersion: v1
kind: ConfigMap
metadata: {name: a, namespace: ns}
data: {k: v}
---
# comment only
---
apiVersion: v1
kind: Namespace
metadata:
  name: n
  annotations:
    argocd.argoproj.io/sync-options: Prune=false,Delete=false
`
)

// recorder runs a check against a throwaway T so a test can assert that the
// check FAILS, which is what a parity helper is for.
func fails(t *testing.T, check func(t testing.TB)) (failed bool, msg string) {
	t.Helper()

	r := &recorder{TB: t}
	func() {
		defer func() {
			if v := recover(); v != nil {
				if _, ok := v.(fatalStop); !ok {
					panic(v)
				}
			}
		}()

		check(r)
	}()

	return r.failed, r.msg.String()
}

type (
	fatalStop struct{}

	recorder struct {
		testing.TB
		failed bool
		msg    strings.Builder
	}
)

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.failed = true
	fmt.Fprintf(&r.msg, format, args...)
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	panic(fatalStop{})
}

func (r *recorder) Fatal(args ...any) { r.Fatalf("%v", args) }

func TestDecodeKeysAndSkipsEmpty(t *testing.T) {
	t.Parallel()

	got := Decode(t, "stream", []byte(stream))
	if len(got) != 2 || got["ConfigMap ns/a"] == nil || got["Namespace /n"] == nil {
		t.Fatalf("keys %v", got.Keys())
	}
}

func TestDecodeFailsOnDuplicate(t *testing.T) {
	t.Parallel()

	failed, msg := fails(t, func(t testing.TB) { Decode(t, "s", []byte(stream+"---\n"+stream)) })
	if !failed || !strings.Contains(msg, "twice") {
		t.Fatalf("a duplicate must fail: %q", msg)
	}
}

func TestAssertSameFailsOnEveryKindOfDifference(t *testing.T) {
	t.Parallel()

	want := Decode(t, "want", []byte(stream))

	AssertSame(t, want, Decode(t, "same", []byte(stream)))

	for name, mutated := range map[string]string{
		"changed": strings.Replace(stream, "k: v", "k: w", 1),
		"missing": strings.Split(stream, "---\n# comment only")[0],
		"added":   stream + "---\napiVersion: v1\nkind: Secret\nmetadata: {name: x}\n",
	} {
		failed, msg := fails(t, func(t testing.TB) { AssertSame(t, want, Decode(t, name, []byte(mutated))) })
		if !failed {
			t.Errorf("%s: AssertSame passed", name)
		}

		_ = msg
	}
}

func TestAssertSwapNeedsDisjointAndUnionEqual(t *testing.T) {
	t.Parallel()

	frozen := Decode(t, "frozen", []byte(stream))
	a := Objects{"ConfigMap ns/a": frozen["ConfigMap ns/a"]}
	b := Objects{"Namespace /n": frozen["Namespace /n"]}

	AssertSwap(t, frozen, Source{"stack", a}, Source{"chart", b})

	if failed, _ := fails(t, func(t testing.TB) { AssertSwap(t, frozen, Source{"stack", frozen}, Source{"chart", b}) }); !failed {
		t.Error("an object in both sources passed")
	}

	if failed, _ := fails(t, func(t testing.TB) { AssertSwap(t, frozen, Source{"stack", a}) }); !failed {
		t.Error("an object in neither source passed")
	}
}

func TestUnionFailsOnOverlap(t *testing.T) {
	t.Parallel()

	o := Decode(t, "o", []byte(stream))
	if failed, _ := fails(t, func(t testing.TB) { Union(t, "u", o, o) }); !failed {
		t.Fatal("overlap passed")
	}

	if got := Union(t, "u", Objects{"a": nil}, Objects{"b": nil}); len(got) != 2 {
		t.Fatal(got)
	}
}

func TestGuarded(t *testing.T) {
	t.Parallel()

	o := Decode(t, "o", []byte(stream))
	if Guarded(o["ConfigMap ns/a"]) || !Guarded(o["Namespace /n"]) {
		t.Fatal("guard detection")
	}
}

func TestApplicationValues(t *testing.T) {
	t.Parallel()

	const apps = `
kind: Application
metadata: {name: c-foundation}
spec:
  sources:
    - repoURL: https://x/stack
    - repoURL: oci://r/charts/cluster-foundation
      helm: {valuesObject: {a: 1}}
---
kind: Application
metadata: {name: c-pools}
spec: {sources: [{repoURL: https://x/stack}]}
`

	v, src, app := ApplicationValues(t, []byte(apps), "x", SourceMatch{AppSuffix: "foundation", ChartSuffix: "/cluster-foundation"})
	if !src || !app || v["a"] != 1 {
		t.Fatal(v, src, app)
	}

	if v, src, app := ApplicationValues(t, []byte(apps), "x", SourceMatch{AppSuffix: "pools", ChartSuffix: "/eks"}); v != nil || src || !app {
		t.Fatal("not moved")
	}

	if _, _, app := ApplicationValues(t, []byte(apps), "x", SourceMatch{AppSuffix: "none", ChartSuffix: "/"}); app {
		t.Fatal("no app")
	}
}

func TestChartPathPrefersTheLiveCheckout(t *testing.T) {
	c := Chart{EnvDir: "PARITY_TEST_CHART_DIR", Archive: "x/y.tgz"}
	if got := c.Path("td"); got != filepath.Join("td", "x", "y.tgz") {
		t.Fatal(got)
	}

	t.Setenv("PARITY_TEST_CHART_DIR", "/live")

	if got := c.Path("td"); got != "/live" {
		t.Fatal(got)
	}
}

func TestNormalizeRenderAndDeepMerge(t *testing.T) {
	t.Parallel()

	render := "---\n# Source: w/templates/own.yaml\nkind: Own\n---\n# Source: up/templates/a.yaml\n\nkind: A\n\n\n---\n# Source: up/templates/b.yaml\nkind: B\n"

	got := NormalizeRender(render, "w/templates/")
	if want := "---\n\nkind: A\n---\nkind: B\n"; got != want {
		t.Errorf("NormalizeRender = %q, want %q", got, want)
	}

	merged := DeepMerge(
		map[string]any{"a": map[string]any{"x": 1, "l": []any{1, 2}}, "b": 1},
		map[string]any{"a": map[string]any{"y": 2, "l": []any{3}}},
	)
	if !Equal(merged, map[string]any{"a": map[string]any{"x": 1, "y": 2, "l": []any{3}}, "b": 1}) {
		t.Errorf("DeepMerge = %v", merged)
	}

	w := Wrapper{Key: "up"}

	flat := w.Flatten(map[string]any{"up": map[string]any{"v": 1, "global": map[string]any{"g": "nested", "h": 1}}, "global": map[string]any{"g": "root"}})
	if !Equal(flat, map[string]any{"v": 1, "global": map[string]any{"g": "root", "h": 1}}) {
		t.Errorf("Flatten = %v", flat)
	}
}
