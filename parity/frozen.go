package parity

import (
	"os"
	"path/filepath"
	"testing"
)

type (
	// Fixtures is a directory of frozen fixtures.
	Fixtures struct{ Dir string }
)

// Bytes reads one fixture.
func (f Fixtures) Bytes(t testing.TB, name string) []byte {
	t.Helper()

	raw, err := os.ReadFile(filepath.Join(f.Dir, name))
	if err != nil {
		t.Fatal(err)
	}

	return raw
}

// Objects reads one fixture as a keyed render.
func (f Fixtures) Objects(t testing.TB, name string) Objects {
	t.Helper()

	return Decode(t, "frozen "+name, f.Bytes(t, name))
}
