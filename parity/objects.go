package parity

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type (
	// Objects is a set of rendered objects keyed by Key.
	Objects map[string]map[string]any
)

// Key identifies one object across the sides of a comparison: its kind,
// namespace and name.
func Key(doc map[string]any) string {
	meta, _ := doc["metadata"].(map[string]any)
	ns, _ := meta["namespace"].(string)
	name, _ := meta["name"].(string)

	return fmt.Sprintf("%v %s/%s", doc["kind"], ns, name)
}

// Decode decodes a multi-document YAML stream into Objects. A document that
// does not decode, and an object rendered twice, fail the test; empty
// documents are skipped. source names where the stream came from.
func Decode(t testing.TB, source string, raw []byte) Objects {
	t.Helper()

	out := Objects{}

	dec := yaml.NewDecoder(bytes.NewReader(raw))

	for index := 0; ; index++ {
		var doc map[string]any

		err := dec.Decode(&doc)
		if errors.Is(err, io.EOF) {
			return out
		}

		if err != nil {
			t.Fatalf("%s: YAML document %d does not decode: %v", source, index, err)
		}

		if doc == nil {
			continue
		}

		key := Key(doc)
		if _, dup := out[key]; dup {
			t.Fatalf("%s renders %s twice", source, key)
		}

		out[key] = doc
	}
}

// Keys is the sorted keys of the set.
func (o Objects) Keys() []string {
	return slices.Sorted(maps.Keys(o))
}

// Filter is the objects of the set for which keep is true.
func (o Objects) Filter(keep func(doc map[string]any) bool) Objects {
	out := Objects{}

	for key, doc := range o {
		if keep(doc) {
			out[key] = doc
		}
	}

	return out
}

// Union merges the sets; an object in two of them fails the test, because an
// object rendered twice is a duplicate Argo CD cannot resolve. label names the
// merge in the failure.
func Union(t testing.TB, label string, sets ...Objects) Objects {
	t.Helper()

	out := Objects{}

	for _, set := range sets {
		for key, doc := range set {
			if _, dup := out[key]; dup {
				t.Fatalf("%s: %s is in more than one set", label, key)
			}

			out[key] = doc
		}
	}

	return out
}

// AssertDisjoint fails for every object that is in both sets, naming the
// sets.
func AssertDisjoint(t testing.TB, aName string, a Objects, bName string, b Objects) {
	t.Helper()

	for _, key := range a.Keys() {
		if _, dup := b[key]; dup {
			t.Errorf("%s is rendered by BOTH %s and %s", key, aName, bName)
		}
	}
}

// AssertSame holds got to want, object by object, and reports up to 25
// problems: missing and added objects, and for a differing object the first
// differing line.
func AssertSame(t testing.TB, want, got Objects) {
	t.Helper()

	var problems []string

	for key, w := range want {
		g, ok := got[key]
		if !ok {
			problems = append(problems, "missing: "+key)

			continue
		}

		if !Equal(w, g) {
			wy, _ := yaml.Marshal(w)
			gy, _ := yaml.Marshal(g)
			problems = append(problems, fmt.Sprintf("differs: %s\n  %s", key, FirstDifference(string(wy), string(gy))))
		}
	}

	for key := range got {
		if _, ok := want[key]; !ok {
			problems = append(problems, "added: "+key)
		}
	}

	slices.Sort(problems)

	if len(problems) > 0 {
		shown := problems
		if len(shown) > 25 {
			shown = shown[:25]
		}

		t.Fatalf("%d problems against %d frozen objects, first:\n%s", len(problems), len(want), strings.Join(shown, "\n"))
	}
}

// FirstDifference names the first differing line of two renders, so a parity
// failure says where instead of dumping thousands of lines.
func FirstDifference(want, got string) string {
	w, g := strings.Split(want, "\n"), strings.Split(got, "\n")

	for i := 0; i < len(w) || i < len(g); i++ {
		var wl, gl string
		if i < len(w) {
			wl = w[i]
		}

		if i < len(g) {
			gl = g[i]
		}

		if wl != gl {
			return fmt.Sprintf("line %d (of %d want / %d got):\n  want: %q\n  got:  %q", i+1, len(w), len(g), wl, gl)
		}
	}

	return "no differing line"
}
