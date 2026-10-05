package parity

import (
	"testing"
)

type (
	// Source is one renderer of the objects of a swap: a name for the failure
	// messages and what it renders.
	Source struct {
		Name    string
		Objects Objects
	}
)

// AssertSwap is the claim of a swap: the sources are pairwise DISJOINT (an
// object rendered twice is a duplicate Argo CD cannot resolve; one rendered
// by none is a deletion) and together exactly the frozen render, each object
// equal to its frozen self, annotations included.
func AssertSwap(t testing.TB, frozen Objects, sources ...Source) {
	t.Helper()

	union := Objects{}

	for i, s := range sources {
		for _, other := range sources[:i] {
			AssertDisjoint(t, other.Name, other.Objects, s.Name, s.Objects)
		}

		for key, doc := range s.Objects {
			union[key] = doc
		}
	}

	AssertSame(t, frozen, union)
}
