package parity

import (
	"bufio"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

type (
	// Move is one extraction: objects that one source of an Application renders
	// until a per-cluster gate flips, and another source (a chart in a public
	// repository) renders after. The table of Moves is the single declaration
	// that GuardMoves and the parity tests consume; the next extraction adds a
	// row and a vendored chart.
	//
	// A move reaches a cluster in two steps minutes apart: the stack side first,
	// then the Application's spec. In between NOTHING renders the moved objects,
	// and Argo CD prunes every moved object that lacks Prune=false in
	// argocd.argoproj.io/sync-options (and skips, PruneSkipped, the guarded
	// ones). See GuardMoves.
	Move struct {
		// Name is the move's name in the allow-list and in test names.
		Name string
		// Gate is the cluster values key (overrides.<Gate>) of the gate; empty
		// once the move is settled and the gate deleted: the chart is then the
		// only source, and only its guards are checked.
		Gate string
		// Pins are the versions keys the gate's predicate requires.
		Pins []string
		// Stack renders what the repository side of the move renders for a cluster
		// today; with forceOn, as it would render with the gate turned on. Nil for
		// a settled move.
		Stack func(t *testing.T, cluster string, forceOn bool) Objects
		// Chart renders what the chart source of the Application renders for a
		// cluster whose gate is on; nil Objects where the cluster has not moved.
		Chart func(t *testing.T, cluster string) Objects
	}

	// GuardConfig is the environment of GuardMoves.
	GuardConfig struct {
		Root     string
		Clusters []string
		// AllowList is the path of the allow-list file.
		AllowList string
		Moves     []Move
	}
)

// MoveByName is the named move of the table; a missing name fails the test.
func MoveByName(t testing.TB, moves []Move, name string) Move {
	t.Helper()

	for _, m := range moves {
		if m.Name == name {
			return m
		}
	}

	t.Fatalf("no move named %q", name)

	return Move{}
}

// Guarded reports whether the object carries Prune=false and Delete=false in
// argocd.argoproj.io/sync-options.
func Guarded(doc map[string]any) bool {
	meta, _ := doc["metadata"].(map[string]any)
	annotations, _ := meta["annotations"].(map[string]any)
	opts, _ := annotations["argocd.argoproj.io/sync-options"].(string)

	var prune, del bool

	for _, o := range strings.Split(opts, ",") {
		switch strings.TrimSpace(o) {
		case "Prune=false":
			prune = true
		case "Delete=false":
			del = true
		}
	}

	return prune && del
}

// AllowList reads an allow-list file: `<move> <cluster> <Kind ns/name>` per
// line, `#` comments. The returned map is keyed by the whole line.
func AllowList(t testing.TB, path string) map[string]bool {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}

	defer func() { _ = f.Close() }()

	out := map[string]bool{}

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		out[line] = false
	}

	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}

	return out
}

// GateOn reports whether the cluster's gate for the move is on: always for a
// settled move, else overrides.<Gate>.enabled of values/clusters/<cluster>.yaml.
func GateOn(t testing.TB, root, cluster string, m Move) bool {
	t.Helper()

	if m.Gate == "" {
		return true
	}

	raw, err := os.ReadFile(filepath.Join(root, "values", "clusters", cluster+".yaml"))
	if err != nil {
		t.Fatal(err)
	}

	var live map[string]any
	if err := yaml.Unmarshal(raw, &live); err != nil {
		t.Fatal(err)
	}

	return Truthy(Map(Map(live["overrides"])[m.Gate])["enabled"])
}

// MovedObjects is what the move moves on a cluster: with the gate on, what the
// chart renders; with it off, what leaves the current stack render when the
// gate is turned on.
func MovedObjects(t *testing.T, m Move, cluster string, on bool) Objects {
	t.Helper()

	if on {
		return m.Chart(t, cluster)
	}

	current := m.Stack(t, cluster, false)
	after := m.Stack(t, cluster, true)
	moved := Objects{}

	for key, doc := range current {
		if _, stays := after[key]; !stays {
			moved[key] = doc
		}
	}

	return moved
}

// GuardMoves makes the prune window of a move impossible to merge. For every
// move and cluster:
//
//   - gate off (pending): the objects that would move if the gate were turned
//     on carry Prune=false,Delete=false in the CURRENT render;
//   - gate on (done): the objects the chart source renders still carry it.
//
// Trimming a guard after a hand-over is explicit and reviewed: the test fails
// until the object has an entry (with its reason) in the allow-list, and an
// entry that matches nothing unguarded fails too, so the list cannot rot.
func GuardMoves(t *testing.T, cfg GuardConfig) {
	t.Helper()

	allow := AllowList(t, cfg.AllowList)
	// used records, per allow-list entry, that the render still has it
	// unguarded; written by the subtests, read after they all ran.
	used := make(chan string, 1<<16)

	t.Run("swaps", func(t *testing.T) {
		for _, m := range cfg.Moves {
			for _, cluster := range cfg.Clusters {
				t.Run(m.Name+"/"+cluster, func(t *testing.T) {
					t.Parallel()

					on := GateOn(t, cfg.Root, cluster, m)
					moved := MovedObjects(t, m, cluster, on)

					t.Logf("%s %s: gate on=%v, %d moved objects", m.Name, cluster, on, len(moved))

					for _, key := range slices.Sorted(maps.Keys(moved)) {
						if Guarded(moved[key]) {
							continue
						}

						entry := m.Name + " " + cluster + " " + key
						if _, ok := allow[entry]; ok {
							used <- entry

							continue
						}

						state := "pending (gate off)"
						if on {
							state = "done (gate on)"
						}

						t.Errorf("%s: %s is moved by the %s swap, %s, and lacks Prune=false,Delete=false in argocd.argoproj.io/sync-options.\n"+
							"Argo CD prunes an unguarded moved object in the window between the stack and the Application. Guard it; "+
							"to trim a guard after a hand-over, add %q with a reason to the swap-guard allowlist", cluster, key, m.Name, state, entry)
					}
				})
			}
		}
	})

	close(used)

	for entry := range used {
		allow[entry] = true
	}

	for entry, hit := range allow {
		if !hit {
			t.Errorf("allow-list entry %q matches no unguarded moved object any more (gone or guarded now): remove it", entry)
		}
	}
}
