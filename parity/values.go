package parity

import (
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"go.yaml.in/yaml/v3"
)

// Equal is a strict deep equality of two decoded values.
func Equal(a, b any) bool { return reflect.DeepEqual(a, b) }

// Map is v as a map, nil when it is not one.
func Map(v any) map[string]any {
	m, _ := v.(map[string]any)

	return m
}

// List is v as a list, nil when it is not one.
func List(v any) []any {
	l, _ := v.([]any)

	return l
}

// Truthy is the template notion of a set value: nil, false, the empty string,
// the empty list and the empty map are not.
func Truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}

	return true
}

// JSONNormalize round-trips a value through JSON so the numbers of the two
// sides of a comparison have one type.
func JSONNormalize(t testing.TB, v any) any {
	t.Helper()

	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}

	return out
}

// YAMLNormalize round-trips a computed value through YAML, the way an
// Application carries it, so numbers and lists compare as the chart sees them.
func YAMLNormalize(t testing.TB, v any) map[string]any {
	t.Helper()

	raw, err := yaml.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	var out map[string]any
	if err := yaml.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}

	return out
}

// AssertValuesEqual is the "Application carries the estate's half" claim:
// got (the Application's valuesObject, computed by Helm) deep-equals want (an
// independent Go computation), both normalised through JSON. what names the
// values in the failure.
func AssertValuesEqual(t testing.TB, what string, want, got any) {
	t.Helper()

	if reflect.DeepEqual(JSONNormalize(t, want), JSONNormalize(t, got)) {
		return
	}

	wy, _ := yaml.Marshal(want)
	gy, _ := yaml.Marshal(got)
	t.Fatalf("%s:\n%s", what, FirstDifference(string(wy), string(gy)))
}

// Strings is the elements of a list rendered with Sprint.
func Strings(v any) []string {
	var out []string

	for _, x := range List(v) {
		out = append(out, fmt.Sprint(x))
	}

	return out
}
