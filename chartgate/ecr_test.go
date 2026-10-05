package chartgate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeBin writes an executable shell script and returns its path.
func fakeBin(t *testing.T, name, body string) string {
	t.Helper()

	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	return p
}

func ecrTarget() target {
	return target{
		cluster: "devel",
		app:     Application{Name: "devel-dms", Namespace: "dms"},
		chart:   Chart{Kind: KindOCI, Ref: "oci://" + testECR + "/dms/charts/dms", Version: "0.40.0"},
	}
}

func newECRGate(t *testing.T, awsBody, helmBody string, require bool) *Gate {
	t.Helper()

	return &Gate{
		Root: t.TempDir(), CacheDir: t.TempDir(), Jobs: 1,
		Helm: fakeBin(t, "helm", helmBody), AWS: fakeBin(t, "aws", awsBody),
		RequireECR: require, work: t.TempDir(),
		pulls: map[string]*pullOnce{}, logins: map[string]*pullOnce{},
	}
}

const (
	testECR = "1234567.dkr.ecr.eu-central-1.amazonaws.com"

	// helm fake: `registry login` must receive the password on stdin; `pull`
	// writes an archive; `template` succeeds.
	fakeHelm = `case "$1" in
registry) [ "$(cat)" = "s3cret" ] || { echo "bad password" >&2; exit 1; } ;;
pull) while [ $# -gt 0 ]; do [ "$1" = "--destination" ] && d="$2"; shift; done; : > "$d/dms-0.40.0.tgz" ;;
template) echo "kind: ConfigMap" ;;
esac`
)

func TestECRLoginThenRender(t *testing.T) {
	t.Parallel()

	g := newECRGate(t, `echo s3cret`, fakeHelm, true)

	res := g.check(context.Background(), ecrTarget())
	if res.Status != StatusOK {
		t.Fatalf("status = %s (%s), want ok", res.Status, res.Detail)
	}
}

func TestECRLoginUsesRegionFromHostAndProfile(t *testing.T) {
	t.Parallel()

	log := filepath.Join(t.TempDir(), "args")
	g := newECRGate(t, `echo "$@" > `+log+`; echo s3cret`, fakeHelm, true)
	g.AWSProfile = "ci-profile"

	if res := g.check(context.Background(), ecrTarget()); res.Status != StatusOK {
		t.Fatalf("status = %s (%s)", res.Status, res.Detail)
	}

	got, _ := os.ReadFile(log)
	if want := "ecr get-login-password --region eu-central-1 --profile ci-profile"; strings.TrimSpace(string(got)) != want {
		t.Errorf("aws args = %q, want %q", got, want)
	}
}

func TestECRLoginFailureSkipsLocally(t *testing.T) {
	t.Parallel()

	g := newECRGate(t, `echo "Unable to locate credentials" >&2; exit 255`, fakeHelm, false)

	res := g.check(context.Background(), ecrTarget())
	if res.Status != StatusSkip || !strings.Contains(res.Detail, "needs auth") {
		t.Fatalf("status = %s (%s), want a needs-auth skip", res.Status, res.Detail)
	}
}

func TestECRLoginFailureFailsInCI(t *testing.T) {
	t.Parallel()

	g := newECRGate(t, `echo "Unable to locate credentials" >&2; exit 255`, fakeHelm, true)

	res := g.check(context.Background(), ecrTarget())
	if res.Status != StatusFail || !strings.Contains(res.Detail, "Unable to locate credentials") {
		t.Fatalf("status = %s (%s), want FAIL carrying aws's error", res.Status, res.Detail)
	}
}

func TestECRLoginIsOncePerHost(t *testing.T) {
	t.Parallel()

	count := filepath.Join(t.TempDir(), "n")
	g := newECRGate(t, `echo x >> `+count+`; echo s3cret`, fakeHelm, true)

	for range 3 {
		if res := g.check(context.Background(), ecrTarget()); res.Status != StatusOK {
			t.Fatalf("status = %s (%s)", res.Status, res.Detail)
		}
	}

	got, _ := os.ReadFile(count)
	if n := strings.Count(string(got), "x"); n != 1 {
		t.Errorf("aws called %d times, want 1", n)
	}
}
