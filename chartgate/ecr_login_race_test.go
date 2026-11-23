package chartgate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestECRLoginsOfTwoHostsKeepBothCredentials is the race of two private
// registries (the devel and the stable ECR) logging in at once: each
// `helm registry login` reads the one isolated registry config, adds its
// host and writes it back, so two logins that overlap lose the first
// writer's host and its pulls fail with "basic credential not found". The
// logins are serialised: both hosts stay in the file.
func TestECRLoginsOfTwoHostsKeepBothCredentials(t *testing.T) {
	t.Parallel()

	// A read-modify-write of $HELM_REGISTRY_CONFIG with a window between the
	// read and the write, as helm's own credential store has.
	helm := `case "$1" in
registry)
  cat > /dev/null
  f="$HELM_REGISTRY_CONFIG"; mkdir -p "$(dirname "$f")"
  old="$(cat "$f" 2>/dev/null)"
  sleep 0.3
  printf '%s\n%s\n' "$old" "$3" > "$f" ;;
esac`

	g := newECRGate(t, `echo s3cret`, helm, true)

	hosts := []string{
		"1111111.dkr.ecr.eu-central-1.amazonaws.com",
		"2222222.dkr.ecr.eu-central-1.amazonaws.com",
	}

	var wg sync.WaitGroup

	for _, h := range hosts {
		wg.Add(1)

		go func() {
			defer wg.Done()

			if err := g.login(context.Background(), h); err != nil {
				t.Errorf("login %s: %v", h, err)
			}
		}()
	}

	wg.Wait()

	got, err := os.ReadFile(filepath.Join(g.CacheDir, "helm-cache", "registry.json"))
	if err != nil {
		t.Fatal(err)
	}

	for _, h := range hosts {
		if !strings.Contains(string(got), h) {
			t.Errorf("registry config lost %s:\n%s", h, got)
		}
	}
}
