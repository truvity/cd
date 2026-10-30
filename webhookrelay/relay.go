package webhookrelay

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// signatureHeader is the one signature Argo CD verifies. Checked against the
// source of Argo CD v3.5.5: util/webhook/webhook.go builds the GitHub parser
// from go-playground/webhooks/v6 v6.4.0, whose github.Parse reads ONLY
// X-Hub-Signature-256 (HMAC-SHA256, "sha256=<hex>") and fails with
// ErrMissingHubSignatureHeader without it; Argo CD's own GHCR package-event
// parser (util/webhook/ghcr.go) reads the same header. The legacy SHA-1
// X-Hub-Signature is not consulted by either, so the relay does not send it.
const signatureHeader = "X-Hub-Signature-256"

// forwardedHeaders are copied from the inbound delivery to the forwarded one.
var forwardedHeaders = []string{"Content-Type", "User-Agent", "X-GitHub-Event", "X-GitHub-Delivery", "X-GitHub-Hook-ID"}

var eventName = regexp.MustCompile(`^[a-z_]{1,40}$`)

const unknown = "unknown"

type sender struct {
	name   string
	secret *secretFile
	owners map[string]bool
}

// Relay is the webhook fan-in.
type Relay struct {
	cfg     *Config
	senders []*sender
	target  *secretFile
	client  *http.Client
	log     *slog.Logger
	m       *metrics
}

// New builds a Relay from a validated configuration. client may be nil.
func New(cfg *Config, client *http.Client, log *slog.Logger) (*Relay, error) {
	if client == nil {
		client = &http.Client{
			Timeout: cfg.TargetTimeout,
			// A redirect would resend a signed body somewhere unconfigured.
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
	}

	if log == nil {
		log = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	r := &Relay{cfg: cfg, client: client, log: log, m: newMetrics()}

	var err error
	if r.target, err = newSecretFile(cfg.Target.SecretFile); err != nil {
		return nil, fmt.Errorf("target: %w", err)
	}

	for _, sc := range cfg.Senders {
		sf, err := newSecretFile(sc.SecretFile)
		if err != nil {
			return nil, fmt.Errorf("sender %s: %w", sc.Name, err)
		}

		s := &sender{name: sc.Name, secret: sf, owners: map[string]bool{}}
		for _, o := range sc.Owners {
			s.owners[strings.ToLower(o)] = true
		}

		r.senders = append(r.senders, s)
	}

	return r, nil
}

// Handler serves the relay's endpoints.
func (r *Relay) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/webhook", r.handleWebhook)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	mux.HandleFunc("GET /readyz", r.handleReady)
	mux.HandleFunc("GET /metrics", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		r.m.write(w)
	})

	return mux
}

func (r *Relay) handleReady(w http.ResponseWriter, _ *http.Request) {
	errs := []error{r.target.ready()}
	for _, s := range r.senders {
		errs = append(errs, s.secret.ready())
	}

	if err := errors.Join(errs...); err != nil {
		r.log.Warn("not ready", "error", err.Error())
		http.Error(w, "a secret file is not readable", http.StatusServiceUnavailable)

		return
	}

	_, _ = io.WriteString(w, "ok\n")
}

// delivery is one inbound request, for logging and metrics.
type delivery struct {
	sender, event, id string
}

func (r *Relay) finish(w http.ResponseWriter, d delivery, status int, result, msg string, extra ...any) {
	r.m.delivery(d.sender, d.event, result)

	args := append([]any{"sender", d.sender, "event", d.event, "delivery", d.id, "result", result, "status", status}, extra...)
	level := slog.LevelInfo

	if result != ResultForwarded && result != ResultPing {
		level = slog.LevelWarn
	}

	r.log.Log(context.Background(), level, "delivery", args...)
	http.Error(w, msg, status)
}

func (r *Relay) handleWebhook(w http.ResponseWriter, req *http.Request) {
	d := delivery{sender: unknown, event: unknown, id: clip(req.Header.Get("X-GitHub-Delivery"), 64)}

	body, err := io.ReadAll(http.MaxBytesReader(w, req.Body, r.cfg.MaxBodyBytes))
	if err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			r.finish(w, d, http.StatusRequestEntityTooLarge, ResultTooLarge, "payload too large")
		} else {
			r.finish(w, d, http.StatusBadRequest, ResultBadRequest, "cannot read body")
		}

		return
	}

	s := r.match(body, req.Header.Get(signatureHeader))
	if s == nil {
		r.finish(w, d, http.StatusUnauthorized, ResultUnauthorized, "signature does not verify")

		return
	}

	d.sender = s.name

	event := req.Header.Get("X-GitHub-Event")
	if !eventName.MatchString(event) {
		d.event = "other"
		r.finish(w, d, http.StatusBadRequest, ResultBadRequest, "missing or malformed X-GitHub-Event")

		return
	}

	d.event = event

	owners, err := payloadOwners(body)
	if err != nil {
		r.finish(w, d, http.StatusBadRequest, ResultBadRequest, "payload is not JSON")

		return
	}

	if bad := s.refuse(owners, event == "ping"); bad != "" {
		r.finish(w, d, http.StatusForbidden, ResultForbiddenOwner, "sender is not entitled to this owner", "owner", bad)

		return
	}

	if event == "ping" {
		// A rotation check pings the consumer with the new secret and needs a 2xx.
		r.finish(w, d, http.StatusOK, ResultPing, "pong")

		return
	}

	r.forward(w, req, d, body)
}

// match returns the sender whose secret signed body, or nil. Every sender is
// tried, whatever the outcome of the earlier ones, with a constant-time compare.
func (r *Relay) match(body []byte, header string) *sender {
	hexSig, ok := strings.CutPrefix(header, "sha256=")
	if !ok {
		return nil
	}

	got, err := hex.DecodeString(hexSig)
	if err != nil {
		return nil
	}

	var found *sender

	for _, s := range r.senders {
		key, err := s.secret.get()
		if err != nil {
			r.log.Error("sender secret", "sender", s.name, "error", err.Error())
		}

		if key == nil {
			continue
		}

		if hmac.Equal(got, sign(key, body)) && found == nil {
			found = s
		}
	}

	return found
}

func sign(key, body []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(body)

	return m.Sum(nil)
}

// refuse returns the first owner the sender may not act for, or "". A payload
// naming no owner at all is refused unless it is a ping: Argo CD acts only on
// events that carry a repository or a package, so an owner-less event has
// nothing to deliver and nothing to bind to.
func (s *sender) refuse(owners []string, ping bool) string {
	if len(owners) == 0 && !ping {
		return "(none)"
	}

	for _, o := range owners {
		if !s.owners[strings.ToLower(o)] {
			return o
		}
	}

	return ""
}

// payloadOwners returns every owner the payload names: the repository's
// owner.login, the owner segment of its full_name and of each of its URLs, the
// organization.login, and a package's namespace/owner. ALL of them are bound to
// the sender, because Argo CD matches Applications by the repository URL and a
// holder of one organisation's secret must not be able to pair that
// organisation's repository.owner with another organisation's html_url.
func payloadOwners(body []byte) ([]string, error) {
	var p struct {
		Repository *struct {
			Owner    *struct{ Login string } `json:"owner"`
			FullName string                  `json:"full_name"`
			HTMLURL  string                  `json:"html_url"`
			CloneURL string                  `json:"clone_url"`
			SSHURL   string                  `json:"ssh_url"`
			GitURL   string                  `json:"git_url"`
			SVNURL   string                  `json:"svn_url"`
		} `json:"repository"`
		Organization *struct{ Login string } `json:"organization"`
		Package      *struct {
			Namespace string                  `json:"namespace"`
			Owner     *struct{ Login string } `json:"owner"`
		} `json:"package"`
	}

	if err := json.Unmarshal(body, &p); err != nil {
		return nil, err
	}

	var out []string

	add := func(o string) {
		if o != "" {
			out = append(out, o)
		}
	}

	if rp := p.Repository; rp != nil {
		if rp.Owner != nil {
			add(rp.Owner.Login)
		}

		add(firstSegment(rp.FullName))

		for _, u := range []string{rp.HTMLURL, rp.CloneURL, rp.SSHURL, rp.GitURL, rp.SVNURL} {
			add(urlOwner(u))
		}
	}

	if p.Organization != nil {
		add(p.Organization.Login)
	}

	if p.Package != nil {
		add(p.Package.Namespace)

		if p.Package.Owner != nil {
			add(p.Package.Owner.Login)
		}
	}

	return out, nil
}

func firstSegment(path string) string {
	path = strings.TrimLeft(path, "/")
	seg, _, _ := strings.Cut(path, "/")

	return seg
}

// urlOwner returns the first path segment of a repository URL, in https://,
// ssh:// and scp-like (git@host:owner/repo) form.
func urlOwner(raw string) string {
	if raw == "" {
		return ""
	}

	if !strings.Contains(raw, "://") {
		if _, rest, ok := strings.Cut(raw, ":"); ok {
			return firstSegment(rest)
		}

		return ""
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "(unparsable)"
	}

	return firstSegment(u.Path)
}

func (r *Relay) forward(w http.ResponseWriter, in *http.Request, d delivery, body []byte) {
	key, err := r.target.get()
	if key == nil {
		r.finish(w, d, http.StatusBadGateway, ResultTargetError, "target secret unavailable", "error", err.Error())

		return
	}

	out, err := http.NewRequestWithContext(in.Context(), http.MethodPost, r.cfg.Target.URL, bytes.NewReader(body))
	if err != nil {
		r.finish(w, d, http.StatusBadGateway, ResultTargetError, "cannot build target request", "error", err.Error())

		return
	}

	for _, h := range forwardedHeaders {
		if v := in.Header.Get(h); v != "" {
			out.Header.Set(h, v)
		}
	}

	out.Header.Set(signatureHeader, "sha256="+hex.EncodeToString(sign(key, body)))

	start := time.Now()
	resp, err := r.client.Do(out)
	elapsed := time.Since(start).Seconds()
	r.m.forward(elapsed)

	if err != nil {
		r.finish(w, d, http.StatusBadGateway, ResultTargetError, "target unreachable", "error", scrub(err), "seconds", elapsed)

		return
	}

	defer func() { _ = resp.Body.Close() }()

	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<20))

	switch {
	case resp.StatusCode >= 500:
		r.finish(w, d, http.StatusBadGateway, ResultTargetError, "target failed", "target_status", resp.StatusCode, "seconds", elapsed)
	case resp.StatusCode >= 400:
		// Typically the target refusing our signature: a relay misconfiguration.
		r.finish(w, d, resp.StatusCode, ResultTargetError, "target refused the delivery", "target_status", resp.StatusCode, "seconds", elapsed)
	default:
		r.finish(w, d, resp.StatusCode, ResultForwarded, "forwarded", "target_status", resp.StatusCode, "seconds", elapsed)
	}
}

// scrub names the failure without the request URL the client error embeds.
func scrub(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err.Error()
	}

	return err.Error()
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}

	return s
}
