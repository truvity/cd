package promotiongate

// The handful of jq semantics the scripts relied on, kept exact where a
// decision depends on them: an input is a STREAM of JSON values, each run on
// its own (an error on one is reported and the next still runs, the exit
// status is 5); `.a` on null is null and on anything but an object an error;
// `x // y` takes y when x is null or false (and does NOT swallow an error in
// x); `-r` prints a string raw and anything else as JSON.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type jqError struct{ msg string }

func (e *jqError) Error() string { return e.msg }

func jqErrorf(format string, a ...any) error { return &jqError{msg: fmt.Sprintf(format, a...)} }

// jqRaw runs f over every JSON value of body like `jq -r`: each result is one
// line of the returned text. An error is written to stderr in jq's own shape
// and the run goes on with the next value; a parse error ends the stream.
// ok is false when jq would have exited 5: the last value failed, or the
// input did not parse.
func jqRaw(body string, stderr io.Writer, f func(v any) ([]string, error)) (out string, ok bool) {
	var b strings.Builder

	ok = true
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()

	for {
		var v any

		err := dec.Decode(&v)
		if errors.Is(err, io.EOF) {
			return b.String(), ok
		}

		if err != nil {
			_, _ = fmt.Fprintf(stderr, "jq: error (at <stdin>:0): Cannot parse input: %v\n", err)

			return b.String(), false
		}

		lines, err := f(v)
		for _, l := range lines {
			b.WriteString(l)
			b.WriteByte('\n')
		}

		// jq's exit status is the LAST input's: an error on an earlier
		// one is reported and forgotten.
		ok = err == nil
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "jq: error (at <stdin>:0): %v\n", err)
		}
	}
}

// substitution is what `$(...)` keeps of a command's output: every trailing
// newline is dropped.
func substitution(s string) string { return strings.TrimRight(s, "\n") }

func jqType(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case json.Number:
		return "number"
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return "unknown"
	}
}

// field is jq's `.k`.
func field(v any, k string) (any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case map[string]any:
		return t[k], nil
	default:
		return nil, jqErrorf("Cannot index %s with %q", jqType(v), k)
	}
}

// path is a chain of `.a.b.c`.
func path(v any, keys ...string) (any, error) {
	var err error
	for _, k := range keys {
		if v, err = field(v, k); err != nil {
			return nil, err
		}
	}

	return v, nil
}

// index is jq's `.[i]` for a non-negative i.
func index(v any, i int) (any, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case []any:
		if i < len(t) {
			return t[i], nil
		}

		return nil, nil
	default:
		return nil, jqErrorf("Cannot index %s with number", jqType(v))
	}
}

// iterate is jq's `.[]`.
func iterate(v any) ([]any, error) {
	switch t := v.(type) {
	case []any:
		return t, nil
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}

		sort.Strings(keys)

		out := make([]any, 0, len(keys))
		for _, k := range keys {
			out = append(out, t[k])
		}

		return out, nil
	default:
		return nil, jqErrorf("Cannot iterate over %s", jqDescribe(v))
	}
}

// truthy is jq's truth: everything but null and false.
func truthy(v any) bool { return v != nil && v != false }

// toJSON is jq's tojson: compact, no HTML escaping, numbers as written.
func toJSON(v any) string {
	var b bytes.Buffer

	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)

	return strings.TrimSuffix(b.String(), "\n")
}

// rawOut is `-r`'s rendering of one result: a string raw, anything else as
// jq prints JSON (indented by two).
func rawOut(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	switch v.(type) {
	case map[string]any, []any:
		var b bytes.Buffer

		enc := json.NewEncoder(&b)
		enc.SetEscapeHTML(false)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)

		return strings.TrimSuffix(b.String(), "\n")
	}

	return toJSON(v)
}

// interp is a value inside a jq string interpolation `\(...)`: a string as is,
// anything else as tojson.
func interp(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	return toJSON(v)
}

// jqDescribe is how jq names a value in an error: its type and (short) JSON.
func jqDescribe(v any) string { return fmt.Sprintf("%s (%s)", jqType(v), toJSON(v)) }

// addString is jq's `a + b` for a string a: null adds nothing, a string
// concatenates, anything else is an error.
func addString(a string, b any) (string, error) {
	switch t := b.(type) {
	case nil:
		return a, nil
	case string:
		return a + t, nil
	default:
		return "", jqErrorf("string (%s) and %s cannot be added", toJSON(a), jqDescribe(b))
	}
}

// joinComma is jq 1.7's `join(",")`: null is empty, numbers and booleans are
// printed, a string is itself, an array or object is an error.
func joinComma(vs []any) (string, error) {
	parts := make([]string, 0, len(vs))

	for _, v := range vs {
		switch t := v.(type) {
		case nil:
			parts = append(parts, "")
		case string:
			parts = append(parts, t)
		case json.Number, bool:
			parts = append(parts, toJSON(t))
		default:
			return "", jqErrorf("string (%s) and %s cannot be added", toJSON(strings.Join(parts, ",")), jqDescribe(v))
		}
	}

	return strings.Join(parts, ","), nil
}

// ---------------------------------------------------------------------------
// awk, the way the scripts' busybox awk did it.

// awkNumber reports whether s is a numeric string to awk (strtod accepts the
// whole of it, surrounding blanks allowed), and its value.
func awkNumber(s string) (float64, bool) {
	t := strings.TrimSpace(s)
	if t == "" {
		return 0, false
	}

	f, err := strconv.ParseFloat(t, 64)
	if err != nil {
		var ne *strconv.NumError
		if errors.As(err, &ne) && errors.Is(ne.Err, strconv.ErrRange) {
			return f, true
		}

		return 0, false
	}

	return f, true
}

// awkCompare is awk's `a OP b` for two -v values: numeric when both are
// numeric strings, otherwise a string comparison. It returns -1, 0 or 1, and
// unordered as ordered=false, which no comparison satisfies.
func awkCompare(a, b string) (cmp int, ordered bool) {
	fa, okA := awkNumber(a)
	fb, okB := awkNumber(b)

	if okA && okB {
		// busybox awk compares by the sign of the difference, so a NaN
		// either side, and Inf against the same Inf, satisfy nothing.
		switch d := fa - fb; {
		case math.IsNaN(d):
			return 0, false
		case d < 0:
			return -1, true
		case d > 0:
			return 1, true
		default:
			return 0, true
		}
	}

	return strings.Compare(a, b), true
}

// numGE is the scripts' num_ge: awk's a >= b.
func numGE(a, b string) bool {
	c, ok := awkCompare(a, b)

	return ok && c >= 0
}

// numGT is the scripts' num_gt: awk's a > b.
func numGT(a, b string) bool {
	c, ok := awkCompare(a, b)

	return ok && c > 0
}

// awkValue is the number awk makes of a string in arithmetic: the longest
// leading prefix strtod accepts, 0 when there is none.
func awkValue(s string) float64 {
	t := strings.TrimLeftFunc(s, unicode.IsSpace)
	for end := len(t); end > 0; end-- {
		if f, ok := awkNumber(t[:end]); ok {
			return f
		}
	}

	return 0
}

// toInt is the scripts' to_int: awk's printf "%d", a + 0.5 (round half up,
// then truncate toward zero). A NaN or infinite value prints as busybox awk
// prints it, INT_MIN.
func toInt(s string) int64 {
	if s == "" {
		s = "0"
	}

	f := awkValue(s) + 0.5
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return math.MinInt32
	}

	return int64(math.Trunc(f))
}

// ratio4 is the scripts' failure ratio: 0 when there are no journeys,
// otherwise f/t with four decimals.
func ratio4(failures, total int64) string {
	if total == 0 {
		return "0"
	}

	return fmt.Sprintf("%.4f", float64(failures)/float64(total))
}

// shellInt is a POSIX shell arithmetic operand: decimal, 0x hexadecimal or
// 0 octal, with an optional sign.
func shellInt(s string) (int64, error) {
	t := strings.TrimSpace(s)

	neg := false
	if strings.HasPrefix(t, "-") || strings.HasPrefix(t, "+") {
		neg = t[0] == '-'
		t = t[1:]
	}

	base := 10

	switch {
	case strings.HasPrefix(t, "0x") || strings.HasPrefix(t, "0X"):
		base, t = 16, t[2:]
	case len(t) > 1 && t[0] == '0':
		base, t = 8, t[1:]
	}

	n, err := strconv.ParseInt(t, base, 64)
	if err != nil || strings.ContainsAny(t, "+-_") {
		return 0, fmt.Errorf("arithmetic expression: expecting primary: %q", s)
	}

	if neg {
		n = -n
	}

	return n, nil
}
