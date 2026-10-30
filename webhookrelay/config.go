// Package webhookrelay is a fan-in relay for GitHub webhooks, placed in front
// of Argo CD's /api/webhook.
//
// Argo CD verifies every GitHub delivery against ONE secret
// (webhook.github.secret). An installation whose Applications pull from
// repositories of several GitHub organisations, each organisation's hook or
// GitHub App with its own independently rotated secret, cannot point them all
// at Argo CD (upstream request: argoproj/argo-cd#24067). The relay verifies a
// delivery against the secret of whichever configured sender signed it, checks
// that the sender is entitled to the repository owner the payload names, and
// forwards the same body re-signed with the one secret Argo CD knows.
package webhookrelay

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

// Defaults of the optional settings.
const (
	DefaultListen       = ":8080"
	DefaultMaxBodyBytes = 25 << 20
	DefaultReadTimeout  = 30 * time.Second
	DefaultWriteTimeout = 60 * time.Second
	DefaultTargetTimout = 30 * time.Second
)

// Config is the relay's configuration file (YAML, and therefore JSON).
type Config struct {
	// Listen is the address the HTTP server binds; default ":8080".
	Listen string `yaml:"listen"`
	// MaxBodyBytes caps a delivery body; default 25 MiB (GitHub's own cap).
	MaxBodyBytes int64 `yaml:"maxBodyBytes"`
	// ReadTimeout and WriteTimeout bound the inbound connection.
	ReadTimeout  time.Duration `yaml:"readTimeout"`
	WriteTimeout time.Duration `yaml:"writeTimeout"`
	// TargetTimeout bounds the forwarded request.
	TargetTimeout time.Duration `yaml:"targetTimeout"`
	// Senders are the organisations (or users) allowed to deliver.
	Senders []SenderConfig `yaml:"senders"`
	// Target is Argo CD's webhook endpoint.
	Target TargetConfig `yaml:"target"`
}

// SenderConfig is one GitHub hook (or App) secret and what it may trigger.
type SenderConfig struct {
	Name       string   `yaml:"name"`
	SecretFile string   `yaml:"secretFile"`
	Owners     []string `yaml:"owners"`
}

// TargetConfig is where deliveries are forwarded.
type TargetConfig struct {
	URL        string `yaml:"url"`
	SecretFile string `yaml:"secretFile"`
}

var senderName = regexp.MustCompile(`^[a-zA-Z0-9]([-_.a-zA-Z0-9]*[a-zA-Z0-9])?$`)

// LoadConfig reads, defaults and validates the file at path.
func LoadConfig(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	var c Config

	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)

	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}

	if err := c.Validate(); err != nil {
		return nil, err
	}

	return &c, nil
}

// Validate applies the defaults and checks the configuration, returning every
// problem found.
func (c *Config) Validate() error {
	if c.Listen == "" {
		c.Listen = DefaultListen
	}

	if c.MaxBodyBytes == 0 {
		c.MaxBodyBytes = DefaultMaxBodyBytes
	}

	if c.ReadTimeout == 0 {
		c.ReadTimeout = DefaultReadTimeout
	}

	if c.WriteTimeout == 0 {
		c.WriteTimeout = DefaultWriteTimeout
	}

	if c.TargetTimeout == 0 {
		c.TargetTimeout = DefaultTargetTimout
	}

	var errs []error

	if c.MaxBodyBytes < 0 {
		errs = append(errs, errors.New("maxBodyBytes must be positive"))
	}

	if len(c.Senders) == 0 {
		errs = append(errs, errors.New("senders: at least one is required"))
	}

	seen := map[string]bool{}

	for i, s := range c.Senders {
		where := fmt.Sprintf("senders[%d]", i)

		switch {
		case s.Name == "":
			errs = append(errs, fmt.Errorf("%s: name is required", where))
		case !senderName.MatchString(s.Name):
			errs = append(errs, fmt.Errorf("%s: name %q must be alphanumeric with -_.", where, s.Name))
		case seen[s.Name]:
			errs = append(errs, fmt.Errorf("%s: duplicate name %q", where, s.Name))
		}

		seen[s.Name] = true

		if len(s.Owners) == 0 {
			errs = append(errs, fmt.Errorf("%s (%s): owners must not be empty", where, s.Name))
		}

		for _, o := range s.Owners {
			if strings.TrimSpace(o) == "" || strings.ContainsAny(o, "/ \t") {
				errs = append(errs, fmt.Errorf("%s (%s): owner %q must be a bare login", where, s.Name, o))
			}
		}

		if err := readable(s.SecretFile); err != nil {
			errs = append(errs, fmt.Errorf("%s (%s): secretFile: %w", where, s.Name, err))
		}
	}

	u, err := url.Parse(c.Target.URL)

	switch {
	case c.Target.URL == "":
		errs = append(errs, errors.New("target.url is required"))
	case err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		errs = append(errs, fmt.Errorf("target.url %q must be an absolute http(s) URL", c.Target.URL))
	}

	if err := readable(c.Target.SecretFile); err != nil {
		errs = append(errs, fmt.Errorf("target.secretFile: %w", err))
	}

	return errors.Join(errs...)
}

func readable(path string) error {
	if path == "" {
		return errors.New("is required")
	}

	_, err := readSecret(path)

	return err
}
