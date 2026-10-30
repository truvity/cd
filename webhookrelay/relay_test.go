package webhookrelay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const targetSecret = "target-secret"

type captured struct {
	body    []byte
	header  http.Header
	calls   int
	status  int
	handled sync.Mutex
}

type fixture struct {
	t      *testing.T
	dir    string
	relay  *httptest.Server
	target *captured
	r      *Relay
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func newFixture(t *testing.T, mutate func(*Config)) *fixture {
	t.Helper()

	dir := t.TempDir()
	rec := &captured{status: http.StatusOK}

	tgt := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.handled.Lock()
		defer rec.handled.Unlock()

		rec.calls++
		rec.body, _ = io.ReadAll(r.Body)
		rec.header = r.Header.Clone()
		w.WriteHeader(rec.status)
	}))
	t.Cleanup(tgt.Close)

	writeFile(t, filepath.Join(dir, "alpha"), "alpha-secret\n")
	writeFile(t, filepath.Join(dir, "beta"), "beta-secret")
	writeFile(t, filepath.Join(dir, "target"), targetSecret)

	cfg := &Config{
		Senders: []SenderConfig{
			{Name: "alpha", SecretFile: filepath.Join(dir, "alpha"), Owners: []string{"Alpha-Org"}},
			{Name: "beta", SecretFile: filepath.Join(dir, "beta"), Owners: []string{"beta-org", "beta-user"}},
		},
		Target: TargetConfig{URL: tgt.URL + "/api/webhook", SecretFile: filepath.Join(dir, "target")},
	}
	if mutate != nil {
		mutate(cfg)
	}

	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	r, err := New(cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	srv := httptest.NewServer(r.Handler())
	t.Cleanup(srv.Close)

	return &fixture{t: t, dir: dir, relay: srv, target: rec, r: r}
}

func sig256(secret string, body []byte) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)

	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func pushBody(owner string) []byte {
	return []byte(`{"ref":"refs/heads/main","repository":{"full_name":"` + owner + `/app","owner":{"login":"` + owner + `"},` +
		`"html_url":"https://github.com/` + owner + `/app","clone_url":"https://github.com/` + owner + `/app.git","ssh_url":"git@github.com:` + owner + `/app.git"}}`)
}

func (f *fixture) post(event, signature string, body []byte) int {
	f.t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, f.relay.URL+"/api/webhook", bytes.NewReader(body))
	if err != nil {
		f.t.Fatal(err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "GitHub-Hookshot/abc")
	req.Header.Set("X-GitHub-Delivery", "d-1")
	req.Header.Set("X-GitHub-Hook-ID", "42")

	if event != "" {
		req.Header.Set("X-GitHub-Event", event)
	}

	if signature != "" {
		req.Header.Set("X-Hub-Signature-256", signature)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}

	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, resp.Body)

	return resp.StatusCode
}

func (f *fixture) calls() int {
	f.target.handled.Lock()
	defer f.target.handled.Unlock()

	return f.target.calls
}

func TestSignature(t *testing.T) {
	body := pushBody("alpha-org")

	cases := []struct {
		name, sig string
		want      int
	}{
		{"good", sig256("alpha-secret", body), http.StatusOK},
		{"bad", sig256("wrong", body), http.StatusUnauthorized},
		{"missing", "", http.StatusUnauthorized},
		{"no prefix", strings.TrimPrefix(sig256("alpha-secret", body), "sha256="), http.StatusUnauthorized},
		{"not hex", "sha256=zz", http.StatusUnauthorized},
		{"target secret is not a sender", sig256(targetSecret, body), http.StatusUnauthorized},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t, nil)
			if got := f.post("push", c.sig, body); got != c.want {
				t.Fatalf("status %d, want %d", got, c.want)
			}

			if c.want != http.StatusOK && f.calls() != 0 {
				t.Fatal("an unverified delivery was forwarded")
			}
		})
	}
}

func TestSenderSelectionAndOwner(t *testing.T) {
	f := newFixture(t, nil)

	// Each sender delivers for its own owner (case-insensitively).
	for _, c := range []struct{ secret, owner string }{
		{"alpha-secret", "alpha-org"}, {"alpha-secret", "ALPHA-ORG"}, {"beta-secret", "beta-org"}, {"beta-secret", "beta-user"},
	} {
		b := pushBody(c.owner)
		if got := f.post("push", sig256(c.secret, b), b); got != http.StatusOK {
			t.Fatalf("%s for %s: %d", c.secret, c.owner, got)
		}
	}

	// One organisation's secret cannot trigger another's.
	b := pushBody("beta-org")
	if got := f.post("push", sig256("alpha-secret", b), b); got != http.StatusForbidden {
		t.Fatalf("cross-owner: %d", got)
	}

	// Nor can it smuggle another owner's URL under its own repository.owner.
	mixed := []byte(`{"repository":{"owner":{"login":"alpha-org"},"full_name":"alpha-org/x","html_url":"https://github.com/beta-org/x"}}`)
	if got := f.post("push", sig256("alpha-secret", mixed), mixed); got != http.StatusForbidden {
		t.Fatalf("mixed owner: %d", got)
	}

	// Nor a package event for another namespace.
	pkg := []byte(`{"package":{"namespace":"beta-org"},"repository":{"owner":{"login":"alpha-org"}}}`)
	if got := f.post("package", sig256("alpha-secret", pkg), pkg); got != http.StatusForbidden {
		t.Fatalf("package: %d", got)
	}

	// An event naming no owner has nothing to bind to.
	none := []byte(`{"zen":"x"}`)
	if got := f.post("push", sig256("alpha-secret", none), none); got != http.StatusForbidden {
		t.Fatalf("ownerless: %d", got)
	}

	// organization.login alone is enough to bind (org-level events).
	org := []byte(`{"organization":{"login":"alpha-org"}}`)
	if got := f.post("push", sig256("alpha-secret", org), org); got != http.StatusOK {
		t.Fatalf("org only: %d", got)
	}
}

func TestPingNotForwarded(t *testing.T) {
	f := newFixture(t, nil)
	b := []byte(`{"zen":"Keep it logically awesome."}`)

	if got := f.post("ping", sig256("beta-secret", b), b); got != http.StatusOK {
		t.Fatalf("ping: %d", got)
	}

	if f.calls() != 0 {
		t.Fatal("ping was forwarded")
	}

	// A bad signature on a ping still fails: a rotation check must see it.
	if got := f.post("ping", sig256("old", b), b); got != http.StatusUnauthorized {
		t.Fatalf("bad ping: %d", got)
	}
}

func TestResign(t *testing.T) {
	f := newFixture(t, nil)
	b := pushBody("alpha-org")

	if got := f.post("push", sig256("alpha-secret", b), b); got != http.StatusOK {
		t.Fatal(got)
	}

	h := f.target.header
	if !bytes.Equal(f.target.body, b) {
		t.Fatal("body changed")
	}

	if got, want := h.Get("X-Hub-Signature-256"), sig256(targetSecret, b); got != want {
		t.Fatalf("sha256 signature %q, want %q", got, want)
	}

	if h.Get("X-Hub-Signature") != "" {
		t.Fatal("the legacy sha1 header must not be sent: Argo CD does not verify it")
	}

	for k, v := range map[string]string{
		"X-GitHub-Event": "push", "X-GitHub-Delivery": "d-1", "X-GitHub-Hook-ID": "42",
		"Content-Type": "application/json", "User-Agent": "GitHub-Hookshot/abc",
	} {
		if h.Get(k) != v {
			t.Fatalf("%s = %q, want %q", k, h.Get(k), v)
		}
	}

	if strings.Contains(h.Get("X-Hub-Signature-256"), "alpha") || h.Get("X-Hub-Signature-256") == sig256("alpha-secret", b) {
		t.Fatal("the sender's signature leaked through")
	}
}

func TestRotationWithoutRestart(t *testing.T) {
	f := newFixture(t, nil)
	b := pushBody("alpha-org")

	if got := f.post("push", sig256("alpha-secret", b), b); got != http.StatusOK {
		t.Fatal(got)
	}

	// Replace the file the way a Kubernetes Secret volume does: a new file
	// renamed over the old path. The size stays equal, so only identity or
	// mtime can reveal the change.
	tmp := filepath.Join(f.dir, "alpha.new")
	writeFile(t, tmp, "alpha-secrex")

	if err := os.Rename(tmp, filepath.Join(f.dir, "alpha")); err != nil {
		t.Fatal(err)
	}

	if got := f.post("push", sig256("alpha-secrex", b), b); got != http.StatusOK {
		t.Fatalf("new secret: %d", got)
	}

	if got := f.post("push", sig256("alpha-secret", b), b); got != http.StatusUnauthorized {
		t.Fatalf("old secret still verifies: %d", got)
	}

	// In-place rewrite with a different mtime.
	p := filepath.Join(f.dir, "target")
	writeFile(t, p, "target-rotated")

	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(p, later, later); err != nil {
		t.Fatal(err)
	}

	f.post("push", sig256("alpha-secrex", b), b)

	if got, want := f.target.header.Get("X-Hub-Signature-256"), sig256("target-rotated", b); got != want {
		t.Fatalf("target secret not reloaded: %q", got)
	}
}

func TestBodyLimit(t *testing.T) {
	f := newFixture(t, func(c *Config) { c.MaxBodyBytes = 64 })
	b := append(pushBody("alpha-org"), bytes.Repeat([]byte(" "), 200)...)

	if got := f.post("push", sig256("alpha-secret", b), b); got != http.StatusRequestEntityTooLarge {
		t.Fatalf("status %d", got)
	}

	if f.calls() != 0 {
		t.Fatal("forwarded")
	}
}

func TestTargetFailures(t *testing.T) {
	f := newFixture(t, nil)
	b := pushBody("alpha-org")

	f.target.status = http.StatusInternalServerError
	if got := f.post("push", sig256("alpha-secret", b), b); got != http.StatusBadGateway {
		t.Fatalf("5xx: %d", got)
	}

	f.target.status = http.StatusBadRequest
	if got := f.post("push", sig256("alpha-secret", b), b); got != http.StatusBadRequest {
		t.Fatalf("4xx: %d", got)
	}

	f.target.status = http.StatusOK

	// Unreachable target.
	g := newFixture(t, func(c *Config) { c.Target.URL = "http://127.0.0.1:1/api/webhook" })
	if got := g.post("push", sig256("alpha-secret", b), b); got != http.StatusBadGateway {
		t.Fatalf("unreachable: %d", got)
	}
}

func TestBadRequests(t *testing.T) {
	f := newFixture(t, nil)

	if got := f.post("", sig256("alpha-secret", pushBody("alpha-org")), pushBody("alpha-org")); got != http.StatusBadRequest {
		t.Fatalf("no event: %d", got)
	}

	nj := []byte("not json")
	if got := f.post("push", sig256("alpha-secret", nj), nj); got != http.StatusBadRequest {
		t.Fatalf("not json: %d", got)
	}
}

func httpGet(t *testing.T, url string) (int, string) {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	out, _ := io.ReadAll(resp.Body)

	return resp.StatusCode, string(out)
}

func TestEndpoints(t *testing.T) {
	f := newFixture(t, nil)
	b := pushBody("alpha-org")
	f.post("push", sig256("alpha-secret", b), b)
	f.post("push", sig256("nope", b), b)

	get := func(p string) (int, string) {
		return httpGet(t, f.relay.URL+p)
	}

	if c, _ := get("/healthz"); c != http.StatusOK {
		t.Fatal(c)
	}

	if c, _ := get("/readyz"); c != http.StatusOK {
		t.Fatal(c)
	}

	_, m := get("/metrics")
	for _, w := range []string{
		`webhook_relay_deliveries_total{sender="alpha",event="push",result="forwarded"} 1`,
		`webhook_relay_deliveries_total{sender="unknown",event="unknown",result="unauthorized"} 1`,
		`webhook_relay_forward_duration_seconds_count 1`,
	} {
		if !strings.Contains(m, w) {
			t.Fatalf("metrics lack %q:\n%s", w, m)
		}
	}

	if strings.Contains(m, "alpha-secret") {
		t.Fatal("secret in metrics")
	}

	// GET on the webhook path is refused.
	if c, _ := get("/api/webhook"); c != http.StatusMethodNotAllowed {
		t.Fatal(c)
	}

	// Readiness drops when a secret file vanishes and the last value is gone.
	g := newFixture(t, nil)
	if err := os.Remove(filepath.Join(g.dir, "beta")); err != nil {
		t.Fatal(err)
	}

	if c, _ := httpGet(t, g.relay.URL+"/readyz"); c != http.StatusServiceUnavailable {
		t.Fatalf("readyz with a missing secret: %d", c)
	}
}

func TestConfigValidation(t *testing.T) {
	dir := t.TempDir()
	ok := filepath.Join(dir, "s")
	writeFile(t, ok, "x")

	empty := filepath.Join(dir, "empty")
	writeFile(t, empty, "\n")

	good := func() *Config {
		return &Config{
			Senders: []SenderConfig{{Name: "a", SecretFile: ok, Owners: []string{"o"}}},
			Target:  TargetConfig{URL: "http://argocd-server.argocd/api/webhook", SecretFile: ok},
		}
	}

	if err := good().Validate(); err != nil {
		t.Fatal(err)
	}

	bad := map[string]func(*Config){
		"duplicate":     func(c *Config) { c.Senders = append(c.Senders, c.Senders[0]) },
		"no owners":     func(c *Config) { c.Senders[0].Owners = nil },
		"slash owner":   func(c *Config) { c.Senders[0].Owners = []string{"o/r"} },
		"missing file":  func(c *Config) { c.Senders[0].SecretFile = filepath.Join(dir, "nope") },
		"empty secret":  func(c *Config) { c.Senders[0].SecretFile = empty },
		"no target":     func(c *Config) { c.Target.URL = "" },
		"bad target":    func(c *Config) { c.Target.URL = "ftp://x" },
		"target secret": func(c *Config) { c.Target.SecretFile = "" },
		"no senders":    func(c *Config) { c.Senders = nil },
	}
	for name, m := range bad {
		c := good()
		m(c)

		if c.Validate() == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	s := filepath.Join(dir, "s")
	writeFile(t, s, "x")

	p := filepath.Join(dir, "c.yaml")
	writeFile(t, p, "senders:\n- {name: a, secretFile: "+s+", owners: [o]}\ntarget: {url: 'http://x/api/webhook', secretFile: "+s+"}\ntargetTimeout: 5s\n")

	c, err := LoadConfig(p)
	if err != nil {
		t.Fatal(err)
	}

	if c.Listen != ":8080" || c.MaxBodyBytes != 25<<20 || c.TargetTimeout != 5*time.Second {
		t.Fatalf("%+v", c)
	}

	writeFile(t, p, "bogus: 1\n")

	if _, err := LoadConfig(p); err == nil {
		t.Fatal("unknown key accepted")
	}
}
