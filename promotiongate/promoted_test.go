package promotiongate_test

// The promoted-version check against a fake API server over real TLS: the
// check's default client trusts the CA file alone and sends the token file's
// content, both read at every request, as the script's curl did.

import (
	"encoding/pem"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/truvity/cd/promotiongate"
)

type fakeAPI struct {
	mu sync.Mutex
	// apps answers per poll: apps[name][i] is the body (or "!<status>") of
	// the i-th read, the last one repeated.
	apps  map[string][]string
	reads map[string]int
	auth  []string
	paths []string
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.auth = append(f.auth, r.Header.Get("Authorization"))
	f.paths = append(f.paths, r.URL.Path)

	name := r.URL.Path[strings.LastIndexByte(r.URL.Path, '/')+1:]

	answers, ok := f.apps[name]
	if !ok {
		w.WriteHeader(http.StatusNotFound)

		return
	}

	i := min(f.reads[name], len(answers)-1)
	f.reads[name]++

	body := answers[i]
	if strings.HasPrefix(body, "!") {
		var code int
		_, _ = fmt.Sscanf(body[1:], "%d", &code)
		w.WriteHeader(code)

		return
	}

	_, _ = w.Write([]byte(body))
}

type pvRun struct {
	code        int
	err         error
	out, stderr string
	api         *fakeAPI
}

// application renders an Application with one chart source at specRev,
// compared at cmpRev ("" = never compared), in the given sync and health.
func application(repo, chart, specRev, cmpRev, sync, health, phase string) string {
	src := func(rev string) string {
		s := fmt.Sprintf(`{"repoURL":%q,"targetRevision":%q`, repo, rev)
		if chart != "" {
			s += fmt.Sprintf(`,"chart":%q`, chart)
		}

		return s + "}"
	}

	cmp := ""
	if cmpRev != "" {
		cmp = fmt.Sprintf(`"comparedTo":{"source":%s},`, src(cmpRev))
	}

	op := ""
	if phase != "" {
		op = fmt.Sprintf(`"operationState":{"phase":%q},`, phase)
	}

	return fmt.Sprintf(`{"spec":{"source":%s},"status":{%s"sync":{%s"status":%q},"health":{"status":%q}}}`, src(specRev), op, cmp, sync, health)
}

const (
	ociRepo = "oci://registry.example.com/charts/shop"
	want    = "v1.2.3"
)

func runPromotedVersion(t *testing.T, apps map[string][]string, env map[string]string) pvRun {
	t.Helper()

	api := &fakeAPI{apps: apps, reads: map[string]int{}}
	srv := publicCA.tlsServer(t, api)

	dir := t.TempDir()

	caFile := filepath.Join(dir, "ca.crt")
	if err := os.WriteFile(caFile, publicCA.pem, 0o600); err != nil {
		t.Fatal(err)
	}

	tokenFile := filepath.Join(dir, "token")
	if err := os.WriteFile(tokenFile, []byte("sa-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(apps))
	for n := range apps {
		names = append(names, n)
	}

	base := map[string]string{
		"APPS":         strings.Join(names, " "),
		"CHART_REPO":   ociRepo,
		"CHART_NAME":   "",
		"EXPECTED":     want,
		"WAIT_SECONDS": "30",
		"POLL_SECONDS": "10",
		"JQ":           "/opt/jq/jq",
	}
	for k, v := range env {
		base[k] = v
	}

	cfg, err := promotiongate.PromotedVersionConfigFromEnv(func(k string) (string, bool) {
		v, ok := base[k]

		return v, ok
	})
	if err != nil {
		t.Fatalf("config: %v", err)
	}

	clock := newFakeClock()

	var out, log syncBuffer

	cfg.APIBase, cfg.TokenFile, cfg.CAFile = srv.URL, tokenFile, caFile
	cfg.Now, cfg.Sleep, cfg.Out, cfg.Log = clock.Now, clock.Sleep, &out, &log

	err = promotiongate.PromotedVersion(t.Context(), cfg)

	return pvRun{code: promotiongate.ExitCode(err), err: err, out: out.String(), stderr: log.String(), api: api}
}

func wantPV(t *testing.T, r pvRun, code int, subs ...string) {
	t.Helper()

	if r.code != code {
		t.Fatalf("exit code = %d (%v), want %d\nstdout:\n%s\nstderr:\n%s", r.code, r.err, code, r.out, r.stderr)
	}

	for _, s := range subs {
		if !strings.Contains(r.out, s) {
			t.Errorf("stdout does not contain %q:\n%s", s, r.out)
		}
	}
}

func TestPromotedVersionPassesWhenRenderedSyncedAndHealthy(t *testing.T) {
	t.Parallel()

	r := runPromotedVersion(t, map[string][]string{
		"shop": {application(ociRepo, "", want, want, "Synced", "Healthy", "Succeeded")},
	}, nil)
	wantPV(t, r, 0, "promoted version v1.2.3 is rendered and Synced/Healthy:\n  shop: at v1.2.3\n")

	for _, a := range r.api.auth {
		if a != "Bearer sa-token" {
			t.Errorf("Authorization = %q, want the token file's content", a)
		}
	}

	if r.api.paths[0] != "/apis/argoproj.io/v1alpha1/namespaces/argocd/applications/shop" {
		t.Errorf("path = %s", r.api.paths[0])
	}
}

// Every way an Application is not there yet, in the script's order, each
// waiting until it is, with one report line per change.
func TestPromotedVersionWaitsThroughEachStage(t *testing.T) {
	t.Parallel()

	r := runPromotedVersion(t, map[string][]string{
		"shop": {
			application(ociRepo, "", "v1.2.2", "v1.2.2", "Synced", "Healthy", ""),
			application(ociRepo, "", want, "", "OutOfSync", "Healthy", ""),
			application(ociRepo, "", want, "v1.2.2", "OutOfSync", "Healthy", ""),
			application(ociRepo, "", want, want, "OutOfSync", "Healthy", "Running"),
			application(ociRepo, "", want, want, "OutOfSync", "Healthy", ""),
			application(ociRepo, "", want, want, "Synced", "Progressing", ""),
			application(ociRepo, "", want, want, "Synced", "Healthy", ""),
		},
	}, map[string]string{"WAIT_SECONDS": "100", "POLL_SECONDS": "1"})
	wantPV(t, r, 0,
		"waiting (0s of 100s):\n  shop: spec still targets v1.2.2, want v1.2.3 (parent app-of-apps not synced?)\n",
		"waiting (1s of 100s):\n  shop: spec is at v1.2.3 but last comparison was at nothing\n",
		"waiting (2s of 100s):\n  shop: spec is at v1.2.3 but last comparison was at v1.2.2\n",
		"waiting (3s of 100s):\n  shop: a sync operation is still Running\n",
		"waiting (4s of 100s):\n  shop: sync status OutOfSync\n",
		"waiting (5s of 100s):\n  shop: health Progressing\n",
		"promoted version v1.2.3 is rendered and Synced/Healthy:\n  shop: at v1.2.3\n",
	)
}

// The same report is written once, not at every poll.
func TestPromotedVersionReportsAChangeOnce(t *testing.T) {
	t.Parallel()

	stale := application(ociRepo, "", "v1.2.2", "v1.2.2", "Synced", "Healthy", "")
	r := runPromotedVersion(t, map[string][]string{
		"shop": {stale, stale, stale, application(ociRepo, "", want, want, "Synced", "Healthy", "")},
	}, map[string]string{"POLL_SECONDS": "5"})
	wantPV(t, r, 0)

	if n := strings.Count(r.out, "waiting ("); n != 1 {
		t.Errorf("%d waiting lines, want 1:\n%s", n, r.out)
	}
}

func TestPromotedVersionFailsWhenTheWaitRunsOut(t *testing.T) {
	t.Parallel()

	r := runPromotedVersion(t, map[string][]string{
		"shop": {application(ociRepo, "", "v1.2.2", "v1.2.2", "Synced", "Healthy", "")},
	}, map[string]string{"WAIT_SECONDS": "30", "POLL_SECONDS": "10"})
	wantPV(t, r, 1,
		"FAIL: after 30s the promoted version v1.2.3 is not live. Not ready:\n  shop: spec still targets v1.2.2, want v1.2.3 (parent app-of-apps not synced?)\n",
		"If an Application still targets the previous version, the parent that renders its pin has not synced: look for a sync operation still Running on it.\n",
	)

	if n := len(r.api.paths); n != 4 {
		t.Errorf("%d reads, want 4 (at 0, 10, 20 and 30s)", n)
	}
}

// No Application renders the chart at all: fail at once, there is nothing to
// wait for.
func TestPromotedVersionFailsWhenNoneRendersTheChart(t *testing.T) {
	t.Parallel()

	r := runPromotedVersion(t, map[string][]string{
		"other": {application("oci://registry.example.com/charts/other", "", "v9", "v9", "Synced", "Healthy", "")},
	}, nil)
	wantPV(t, r, 1, "FAIL: none of the Applications (other) renders a source from "+ociRepo+" -- check the Stage's applications:\n  other: renders no source from "+ociRepo+"\n")

	if len(r.api.paths) != 1 {
		t.Errorf("%d reads, want 1", len(r.api.paths))
	}
}

// An Application that renders none of the chart is skipped; the others
// decide.
func TestPromotedVersionSkipsApplicationsOfOtherCharts(t *testing.T) {
	t.Parallel()

	r := runPromotedVersion(t, map[string][]string{
		"shop":  {application(ociRepo, "", want, want, "Synced", "Healthy", "")},
		"other": {application("https://git.example.com/acme/other.git", "", "main", "main", "OutOfSync", "Degraded", "")},
	}, map[string]string{"APPS": "shop other"})
	wantPV(t, r, 0, "  shop: at v1.2.3\n  other: renders no source from "+ociRepo+"\n")
}

// A multi-source Application: only the sources of the chart are judged, the
// values repository beside it never is.
func TestPromotedVersionJudgesOnlyTheChartsSources(t *testing.T) {
	t.Parallel()

	app := `{"spec":{"sources":[{"repoURL":"https://git.example.com/acme/env.git","targetRevision":"main","ref":"values"},{"repoURL":"oci://registry.example.com/charts/shop/","targetRevision":"v1.2.3"}]},
	"status":{"sync":{"status":"Synced","comparedTo":{"sources":[{"repoURL":"https://git.example.com/acme/env.git","targetRevision":"main"},{"repoURL":"oci://registry.example.com/charts/shop","targetRevision":"v1.2.3"}]}},"health":{"status":"Healthy"}}}`
	r := runPromotedVersion(t, map[string][]string{"shop": {app}}, nil)
	wantPV(t, r, 0, "  shop: at v1.2.3\n")
}

// A chart source's identity: the repository without scheme or trailing
// slash, plus /chart where one is named. An Application that says repoURL
// registry/charts + chart X is the same chart as Kargo's oci://.../X.
func TestPromotedVersionChartIdentity(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                  string
		appRepo, appChart     string
		checkRepo, checkChart string
		match                 bool
	}{
		{"same oci path", "oci://registry.example.com/charts/shop", "", ociRepo, "", true},
		{"repo plus chart vs whole path", "registry.example.com/charts", "shop", ociRepo, "", true},
		{"whole path vs repo plus chart", ociRepo, "", "registry.example.com/charts", "shop", true},
		{"trailing slashes", "oci://registry.example.com/charts//", "shop", "oci://registry.example.com/charts/shop/", "", true},
		{"another chart", "registry.example.com/charts", "cart", ociRepo, "", false},
		{"http helm repo plus chart", "https://charts.example.com", "shop", "https://charts.example.com/", "shop", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			r := runPromotedVersion(t, map[string][]string{
				"shop": {application(tc.appRepo, tc.appChart, want, want, "Synced", "Healthy", "")},
			}, map[string]string{"CHART_REPO": tc.checkRepo, "CHART_NAME": tc.checkChart})

			if tc.match {
				wantPV(t, r, 0, "  shop: at v1.2.3\n")
			} else {
				wantPV(t, r, 1, "  shop: renders no source from "+tc.checkRepo+"\n")
			}
		})
	}
}

// An Application that cannot be read waits (it may not exist yet) and says
// with what status.
func TestPromotedVersionWaitsOnAnUnreadableApplication(t *testing.T) {
	t.Parallel()

	r := runPromotedVersion(t, map[string][]string{
		"shop": {"!403", application(ociRepo, "", want, want, "Synced", "Healthy", "")},
	}, nil)
	wantPV(t, r, 0, "waiting (0s of 30s):\n  shop: cannot read Application (HTTP 403)\n", "  shop: at v1.2.3\n")
}

// A response jq could not judge (a source with no repoURL) is an empty
// verdict: it waits, with jq's complaint on stderr.
func TestPromotedVersionWaitsOnAnApplicationItCannotJudge(t *testing.T) {
	t.Parallel()

	r := runPromotedVersion(t, map[string][]string{
		"shop": {`{"spec":{"source":{"chart":"shop"}}}`},
	}, map[string]string{"WAIT_SECONDS": "0"})
	wantPV(t, r, 1, "Not ready:\n  shop: \n")

	if !strings.Contains(r.stderr, "jq: error") || !strings.Contains(r.stderr, "cannot be matched, as it is not a string") {
		t.Errorf("stderr does not carry jq's complaint:\n%s", r.stderr)
	}
}

// ARGOCD_NAMESPACE names where the Applications live.
func TestPromotedVersionReadsTheArgoCDNamespace(t *testing.T) {
	t.Parallel()

	r := runPromotedVersion(t, map[string][]string{
		"shop": {application(ociRepo, "", want, want, "Synced", "Healthy", "")},
	}, map[string]string{"ARGOCD_NAMESPACE": "cd-system"})
	wantPV(t, r, 0)

	if r.api.paths[0] != "/apis/argoproj.io/v1alpha1/namespaces/cd-system/applications/shop" {
		t.Errorf("path = %s", r.api.paths[0])
	}
}

// The CA file is the only trust: a server it did not sign is HTTP 000, as
// curl --cacert reported it.
func TestPromotedVersionTrustsTheCAFileAlone(t *testing.T) {
	t.Parallel()

	other, err := newTestCA("another root")
	if err != nil {
		t.Fatal(err)
	}

	api := &fakeAPI{apps: map[string][]string{"shop": {application(ociRepo, "", want, want, "Synced", "Healthy", "")}}, reads: map[string]int{}}
	srv := other.tlsServer(t, api)

	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.crt")

	// The system store (which holds publicCA here) is not consulted either.
	if err := os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: publicCA.cert.Raw}), 0o600); err != nil {
		t.Fatal(err)
	}

	var out syncBuffer

	clock := newFakeClock()
	err = promotiongate.PromotedVersion(t.Context(), promotiongate.PromotedVersionConfig{
		Apps: "shop", ChartRepo: ociRepo, Expected: want, WaitSeconds: 0, Poll: time.Second,
		APIBase: srv.URL, TokenFile: filepath.Join(dir, "missing-token"), CAFile: caFile,
		Now: clock.Now, Sleep: clock.Sleep, Out: &out, Log: &syncBuffer{},
	})
	if promotiongate.ExitCode(err) != 1 || !strings.Contains(out.String(), "  shop: cannot read Application (HTTP 000)") {
		t.Errorf("err %v, stdout:\n%s", err, out.String())
	}
}

func TestPromotedVersionConfigFromEnv(t *testing.T) {
	t.Parallel()

	env := map[string]string{
		"APPS": "a b", "CHART_REPO": ociRepo, "CHART_NAME": "", "EXPECTED": want,
		"WAIT_SECONDS": "1500", "POLL_SECONDS": "15",
	}
	lookup := func(k string) (string, bool) { v, ok := env[k]; return v, ok }

	cfg, err := promotiongate.PromotedVersionConfigFromEnv(lookup)
	if err != nil {
		t.Fatal(err)
	}

	if cfg.ArgoCDNamespace != "" || cfg.WaitSeconds != 1500 || cfg.Poll != 15*time.Second || cfg.ChartName != "" {
		t.Errorf("config: %+v", cfg)
	}

	env["POLL_SECONDS"] = "0.5"
	if cfg, err = promotiongate.PromotedVersionConfigFromEnv(lookup); err != nil || cfg.Poll != 500*time.Millisecond {
		t.Errorf("fractional poll: %v %v", cfg.Poll, err)
	}

	// set -u: unset is an error, empty is a value.
	delete(env, "CHART_NAME")

	_, err = promotiongate.PromotedVersionConfigFromEnv(lookup)
	if promotiongate.ExitCode(err) != promotiongate.ExitUsage || !strings.Contains(err.Error(), "CHART_NAME: parameter not set") {
		t.Errorf("unset CHART_NAME: %v", err)
	}
}
