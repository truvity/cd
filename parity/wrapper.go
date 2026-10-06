package parity

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// The zero-diff proof of a wrapper chart.
//
// A wrapper chart nests an upstream chart as a subchart and adds only what an
// adopter opts into. The claim an adopter relies on is that moving an
// installation from the upstream chart to the wrapper, with its values nested
// one level, changes nothing the cluster sees. Wrapper holds one case to it:
// the wrapper rendered with the case's layered values, and the upstream chart
// rendered alone with the same values flattened to its own shape (the
// subchart key's contents, the root `global` merged over the nested one, which
// is Helm's precedence), must be the same objects.
//
// "The same objects" is the render with its `# Source:` comment lines removed
// (they name the template's path, which differs between the two charts, and
// Helm emits them as comments no cluster ever sees) and with trailing blank
// lines of each document removed. Everything else is compared byte for byte.

type (
	// Wrapper is one case of the zero-diff proof.
	Wrapper struct {
		// Chart is the wrapper chart's directory.
		Chart string
		// Upstream is the vendored upstream chart archive (or directory).
		Upstream string
		// Key is the values key the upstream chart's values live under in the
		// wrapper (the subchart's name or alias).
		Key string
		// Release and Namespace are passed to both renders.
		Release   string
		Namespace string
		// Layers are values files in the order an adopter layers them (presets
		// first, then the case's own values). Maps are merged, lists replaced.
		Layers []string
		// Overlay is deep-merged over the layers, for a case that varies one
		// value (a switch turned off to isolate an opt-in object).
		Overlay map[string]any
		// OwnTemplates, when set, is the path prefix of the wrapper's own
		// templates as Helm prints it after `# Source: ` (for example
		// "wrapper/templates/"): the objects the
		// upstream chart does not render at all. They are held out of the
		// comparison and pinned by goldens instead.
		OwnTemplates string
	}
)

// Merged returns the layers deep-merged, then the overlay: what Helm hands the
// wrapper chart.
func (w Wrapper) Merged(t testing.TB) map[string]any {
	t.Helper()

	out := map[string]any{}

	for _, path := range w.Layers {
		raw, err := os.ReadFile(path) //nolint:gosec // a test reads its own fixtures
		if err != nil {
			t.Fatal(err)
		}

		var layer map[string]any
		if err := yaml.Unmarshal(raw, &layer); err != nil {
			t.Fatalf("%s: %v", path, err)
		}

		out = DeepMerge(out, layer)
	}

	return DeepMerge(out, w.Overlay)
}

// Flatten returns the values the upstream chart is given: the contents of its
// key with the root `global` merged over the nested one.
func (w Wrapper) Flatten(merged map[string]any) map[string]any {
	nested := Map(merged[w.Key])
	flat := DeepMerge(map[string]any{}, nested)
	flat["global"] = DeepMerge(Map(nested["global"]), Map(merged["global"]))

	return flat
}

// Render returns the normalized wrapper render and the normalized upstream
// render for the case.
func (w Wrapper) Render(t testing.TB, helm string) (wrapped, upstream string) {
	t.Helper()

	merged := w.Merged(t)

	dir := t.TempDir()
	write := func(name string, values map[string]any) string {
		raw, err := yaml.Marshal(values)
		if err != nil {
			t.Fatal(err)
		}

		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, raw, 0o600); err != nil {
			t.Fatal(err)
		}

		return path
	}

	wrapped = NormalizeRender(string(Helm(t, helm, "template", w.Release, w.Chart,
		"--namespace", w.Namespace, "-f", write("merged.yaml", merged))), w.OwnTemplates)
	upstream = NormalizeRender(string(Helm(t, helm, "template", w.Release, w.Upstream,
		"--namespace", w.Namespace, "-f", write("flat.yaml", w.Flatten(merged)))), "")

	return wrapped, upstream
}

// AssertZeroDiff fails the test when the wrapper renders anything the upstream
// chart does not, naming the first difference.
func (w Wrapper) AssertZeroDiff(t testing.TB, helm string) {
	t.Helper()

	wrapped, upstream := w.Render(t, helm)
	if wrapped != upstream {
		t.Fatalf("the wrapper renders something the upstream chart does not.\n%s", FirstDifference(upstream, wrapped))
	}
}

// DeepMerge returns a copy of base with over merged in: maps are merged
// recursively, any other value (a list included) is replaced. Neither input
// is modified.
func DeepMerge(base, over map[string]any) map[string]any {
	out := make(map[string]any, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}

	for k, v := range over {
		if next, ok := v.(map[string]any); ok {
			out[k] = DeepMerge(Map(out[k]), next)

			continue
		}

		out[k] = v
	}

	return out
}

var trailingBlank = regexp.MustCompile(`\n+$`)

// NormalizeRender splits a helm render into documents, drops the documents
// whose `# Source: ` line starts with ownPrefix (none when it is empty), drops
// every `# Source:` comment line, and drops blank lines at the end of a
// document (a template that begins with a comment leaves one; no cluster sees
// it).
func NormalizeRender(render, ownPrefix string) string {
	var (
		out  strings.Builder
		doc  strings.Builder
		skip bool
	)

	flush := func() {
		if doc.Len() > 0 && !skip {
			out.WriteString(trailingBlank.ReplaceAllString(doc.String(), "\n"))
		}
	}

	for _, line := range strings.SplitAfter(render, "\n") {
		trimmed := strings.TrimRight(line, "\n")

		switch {
		case trimmed == "---":
			flush()
			doc.Reset()
			doc.WriteString(line)

			skip = false
		case ownPrefix != "" && strings.HasPrefix(trimmed, "# Source: "+ownPrefix):
			skip = true
		case strings.HasPrefix(trimmed, "# Source:"):
		default:
			doc.WriteString(line)
		}
	}

	flush()

	return out.String()
}
