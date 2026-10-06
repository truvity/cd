package promotiongate

// One HTTP call the way the scripts made it with curl: no redirects followed,
// the status as curl's %{http_code} prints it ("000" when no response came
// back), and a transport failure named by the curl exit code it would have
// been, so a log reads the same as before.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"
)

// response is one call's outcome: the body, the status ("000" if none) and
// the curl exit code (0 when the transfer completed), with the error behind
// a nonzero one.
type response struct {
	body   string
	status string
	exit   int
	err    error
}

// noRedirect keeps a response as it is, as curl without -L does.
func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// newClient is a client of the default transport (proxy settings from the
// environment included), following no redirects. roots nil is the system
// trust store; otherwise ONLY roots is trusted, never merged with it.
func newClient(roots *x509.CertPool, timeout time.Duration) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if roots != nil {
		tr.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
		// A fresh trust store per call, so nothing pools across calls.
		tr.DisableKeepAlives = true
	}

	return &http.Client{Transport: tr, CheckRedirect: noRedirect, Timeout: timeout}
}

// errCABundle is curl's exit 77: the CA bundle could not be read or holds no
// certificate.
type errCABundle struct{ err error }

func (e *errCABundle) Error() string { return e.err.Error() }

func (e *errCABundle) Unwrap() error { return e.err }

// caClient is curl --cacert <file>: the file read now (curl reads it on every
// call), trusted alone. It is consulted only for an https URL, as curl only
// loads it for one.
func caClient(file, rawURL string, timeout time.Duration) (*http.Client, error) {
	if !strings.HasPrefix(strings.ToLower(rawURL), "https://") {
		return newClient(nil, timeout), nil
	}

	pem, err := os.ReadFile(file)
	if err != nil {
		return nil, &errCABundle{err: fmt.Errorf("error setting certificate verify locations: %w", err)}
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return nil, &errCABundle{err: fmt.Errorf("error setting certificate verify locations: no certificate in %s", file)}
	}

	return newClient(pool, timeout), nil
}

// do sends req with client, or reports why it could not, as curl would.
func do(client *http.Client, req *http.Request) response {
	resp, err := client.Do(req)
	if err != nil {
		return response{status: "000", exit: curlExit(err), err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	status := fmt.Sprintf("%03d", resp.StatusCode)

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return response{body: string(b), status: status, exit: curlExit(err), err: err}
	}

	return response{body: string(b), status: status}
}

// failed is a response that never got one: a client that could not be built.
func failed(err error) response { return response{status: "000", exit: curlExit(err), err: err} }

// curlExit is the curl(1) exit code a transport error corresponds to.
func curlExit(err error) int {
	var (
		ca       *errCABundle
		unknown  x509.UnknownAuthorityError
		invalid  x509.CertificateInvalidError
		hostname x509.HostnameError
		verify   *tls.CertificateVerificationError
		dns      *net.DNSError
		op       *net.OpError
		netErr   net.Error
	)

	switch {
	case errors.As(err, &ca):
		return 77 // problem with the CA cert
	case errors.As(err, &unknown), errors.As(err, &invalid), errors.As(err, &hostname), errors.As(err, &verify):
		return 60 // peer certificate cannot be authenticated
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return 28 // operation timeout
	case errors.As(err, &dns):
		return 6 // could not resolve host
	case errors.Is(err, syscall.ECONNREFUSED), errors.As(err, &op) && op.Op == "dial":
		return 7 // failed to connect
	case strings.Contains(err.Error(), "tls:"):
		return 35 // TLS handshake failure
	case strings.Contains(err.Error(), "unsupported protocol scheme"):
		return 1 // unsupported protocol
	default:
		return 56 // failure receiving network data
	}
}

// curlLine is what curl -sS prints on stderr for a failed transfer.
func curlLine(r response) string { return fmt.Sprintf("curl: (%d) %v", r.exit, r.err) }

// urlencode is curl --data-urlencode's encoding of a value: every byte but
// ALPHA, DIGIT and -._~ as %XX.
func urlencode(s string) string {
	const hex = "0123456789ABCDEF"

	var b strings.Builder

	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '.' || c == '_' || c == '~' {
			b.WriteByte(c)

			continue
		}

		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&15])
	}

	return b.String()
}

// form is a POST body of --data-urlencode "name=value" pairs, in order.
func form(pairs ...[2]string) string {
	parts := make([]string, 0, len(pairs))
	for _, p := range pairs {
		parts = append(parts, p[0]+"="+urlencode(p[1]))
	}

	return strings.Join(parts, "&")
}

// postForm is curl's --data-urlencode request: a POST of an urlencoded form.
func postForm(ctx context.Context, rawURL, body string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "*/*")

	return req, nil
}

// readFileSub is `$(cat file)`: the content without trailing newlines, or ""
// with cat's complaint on stderr.
func readFileSub(file string, stderr io.Writer) string {
	b, err := os.ReadFile(file)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "cat: %v\n", err)

		return ""
	}

	return substitution(string(b))
}
