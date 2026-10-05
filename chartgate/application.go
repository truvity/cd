// Package chartgate renders every Argo CD Helm Application this estate
// declares against the chart version Argo CD will actually pull and the
// values it will actually hand it.
//
// The golden suite (the repository's render tests) renders the repository's own charts with
// every pin masked as a sentinel and never opens the charts those
// Applications point at, so a chart that refuses a value (its
// values.schema.json) or fails to template with ours is first found when
// Argo CD syncs. This package closes that gap: it starts from the
// committed bootstrap Application of each cluster (clusters/<cluster>.yaml),
// follows it through the repository's own charts it installs the way Argo CD does
// (valueFiles, ignoreMissingValueFiles, $values refs), and for every
// Application source that is a Helm chart elsewhere pulls that chart at
// targetRevision and runs `helm template` with the Application's values.
package chartgate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strings"

	"go.yaml.in/yaml/v3"
)

type (
	// Application is the part of an Argo CD Application the gate needs.
	Application struct {
		Name      string
		Namespace string // spec.destination.namespace
		Sources   []Source
	}

	// Source is one entry of spec.sources (or spec.source).
	Source struct {
		RepoURL        string `yaml:"repoURL"`
		Chart          string `yaml:"chart"`
		Path           string `yaml:"path"`
		Ref            string `yaml:"ref"`
		TargetRevision string `yaml:"targetRevision"`
		Helm           Helm   `yaml:"helm"`
	}

	// Helm is spec.sources[].helm.
	Helm struct {
		ReleaseName   string         `yaml:"releaseName"`
		ValueFiles    []string       `yaml:"valueFiles"`
		IgnoreMissing bool           `yaml:"ignoreMissingValueFiles"`
		Values        string         `yaml:"values"`
		ValuesObject  map[string]any `yaml:"valuesObject"`
	}

	// ChartKind says how a chart is addressed.
	ChartKind int

	// Chart is a remote chart at a pinned version.
	Chart struct {
		Kind    ChartKind
		Ref     string // oci://host/path/chart, or the repository URL
		Name    string // KindRepo only: the chart name inside the repository
		Version string
	}

	// Kind of a source, as the gate sees it.
	Classification int
)

// ParseApplications decodes a multi-document YAML stream and returns its
// Argo CD Applications; every other kind is ignored.
func ParseApplications(stream []byte) ([]Application, error) {
	dec := yaml.NewDecoder(bytes.NewReader(stream))

	var apps []Application

	for {
		var doc struct {
			Kind     string `yaml:"kind"`
			Metadata struct {
				Name string `yaml:"name"`
			} `yaml:"metadata"`
			Spec struct {
				Source      *Source  `yaml:"source"`
				Sources     []Source `yaml:"sources"`
				Destination struct {
					Namespace string `yaml:"namespace"`
				} `yaml:"destination"`
			} `yaml:"spec"`
		}

		if err := dec.Decode(&doc); err != nil {
			if errors.Is(err, io.EOF) {
				return apps, nil
			}

			return nil, fmt.Errorf("decode Application stream: %w", err)
		}

		if doc.Kind != "Application" {
			continue
		}

		app := Application{
			Name:      doc.Metadata.Name,
			Namespace: doc.Spec.Destination.Namespace,
			Sources:   doc.Spec.Sources,
		}
		if doc.Spec.Source != nil {
			app.Sources = append(app.Sources, *doc.Spec.Source)
		}

		apps = append(apps, app)
	}
}

const (
	// KindOCI is a chart in an OCI registry: `helm pull oci://...`.
	KindOCI ChartKind = iota
	// KindRepo is a chart in a classic HTTP(S) Helm repository.
	KindRepo
)

// String is what a reader recognizes: chart@version.
func (c Chart) String() string {
	if c.Kind == KindRepo {
		return c.Ref + " " + c.Name + "@" + c.Version
	}

	return c.Ref + "@" + c.Version
}

// Host is the registry or repository host.
func (c Chart) Host() string {
	u, err := url.Parse(c.Ref)
	if err != nil || u.Host == "" {
		return ""
	}

	return u.Host
}

// ecrHost matches a private AWS ECR registry. public.ecr.aws is a
// different, anonymous registry and deliberately does not match.
var ecrHost = regexp.MustCompile(`^\d+\.dkr\.ecr\.([a-z0-9-]+)\.amazonaws\.com$`)

// NeedsAuth reports whether the chart sits in a registry that refuses an
// anonymous pull, and names which one. The gate logs in to it (Gate.login)
// before pulling; without a login the chart is "not checked", or a
// failure when Gate.RequireECR is set.
func (c Chart) NeedsAuth() (string, bool) {
	if h := c.Host(); ecrHost.MatchString(h) {
		return "private ECR registry " + h, true
	}

	return "", false
}

const (
	// ClassRefOnly is `ref: values`, a pure file source with nothing to render.
	ClassRefOnly Classification = iota
	// ClassLocal is a chart in this repository (rendered locally, and
	// crawled for the Applications it emits).
	ClassLocal
	// ClassRemote is a Helm chart in a registry or repository.
	ClassRemote
	// ClassGit is a non-Helm source (plain manifests in someone's git repo).
	ClassGit
)

// Classify decides what a source is. repoSelf recognizes this repository's
// own git URL. For ClassRemote the returned Chart is the pinned chart.
func Classify(s Source, repoSelf func(string) bool) (Classification, Chart) {
	switch {
	case s.Ref != "" && s.Path == "" && s.Chart == "":
		return ClassRefOnly, Chart{}
	case isGit(s.RepoURL) && s.Chart == "":
		if repoSelf(s.RepoURL) && s.Path != "" && s.Path != "." {
			return ClassLocal, Chart{}
		}

		return ClassGit, Chart{}
	}

	repo := strings.TrimSuffix(s.RepoURL, "/")

	switch {
	case strings.HasPrefix(repo, "http://") || strings.HasPrefix(repo, "https://"):
		return ClassRemote, Chart{Kind: KindRepo, Ref: repo, Name: s.Chart, Version: s.TargetRevision}
	case s.Chart == "":
		// An oci:// URL with path "." names the chart itself.
		return ClassRemote, Chart{Kind: KindOCI, Ref: repo, Version: s.TargetRevision}
	default:
		// Argo CD's schemeless OCI form: repoURL is the registry path and
		// `chart` the leaf.
		repo = strings.TrimPrefix(repo, "oci://")

		return ClassRemote, Chart{Kind: KindOCI, Ref: "oci://" + repo + "/" + s.Chart, Version: s.TargetRevision}
	}
}

func isGit(repoURL string) bool {
	return strings.HasSuffix(repoURL, ".git") ||
		strings.HasPrefix(repoURL, "https://github.com/") ||
		strings.HasPrefix(repoURL, "https://gitlab.com/")
}

// Release is the Helm release name Argo CD uses: helm.releaseName, else
// the Application's name.
func (a Application) Release(s Source) string {
	if s.Helm.ReleaseName != "" {
		return s.Helm.ReleaseName
	}

	return a.Name
}
