package chartgate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// valueFiles follows Argo CD precedence: listed files in order, then valuesObject
// when present (even empty), otherwise values. Named refs use their Git checkout;
// ordinary paths are relative to the chart (local or in a pulled archive).
func (g *Gate) valueFiles(app Application, src Source, local bool, tgz string) ([]string, error) {
	var files []string
	for _, f := range src.Helm.ValueFiles {
		path, err := g.valueFile(app, src, f, local, tgz)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && src.Helm.IgnoreMissing {
				continue
			}
			return nil, fmt.Errorf("value file %s: %w", f, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("value file %s: %w", f, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("value file %s is not a regular file", f)
		}
		files = append(files, path)
	}
	dir, err := os.MkdirTemp(g.work, "values-")
	if err != nil {
		return nil, err
	}
	write := func(name string, body []byte) error {
		p := filepath.Join(dir, name)
		files = append(files, p)
		return os.WriteFile(p, body, 0o600)
	}
	if src.Helm.ValuesObject != nil {
		raw, err := yaml.Marshal(src.Helm.ValuesObject)
		if err != nil {
			return nil, err
		}
		if err := write("valuesObject.yaml", raw); err != nil {
			return nil, err
		}
	} else if src.Helm.Values != "" {
		if err := write("values.yaml", []byte(src.Helm.Values)); err != nil {
			return nil, err
		}
	}
	return files, nil
}

func (g *Gate) valueFile(app Application, src Source, file string, local bool, tgz string) (string, error) {
	if strings.HasPrefix(file, "$") {
		ref, rel, ok := strings.Cut(strings.TrimPrefix(file, "$"), "/")
		if !ok || ref == "" || rel == "" {
			return "", fmt.Errorf("invalid source reference %q", file)
		}
		for _, source := range app.Sources {
			if source.Ref == ref {
				root, err := g.sourceRoot(source)
				if err != nil {
					return "", err
				}
				return checkoutPath(root, rel)
			}
		}
		return "", fmt.Errorf("unresolved source ref %q (no matching spec.sources[].ref)", ref)
	}
	if !local {
		if tgz == "" {
			return "", fmt.Errorf("chart archive required to resolve shipped values")
		}
		return g.extractShipped(tgz, file)
	}
	root, err := g.sourceRoot(src)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(file) {
		return "", fmt.Errorf("path %q must be relative to the chart", file)
	}
	// A Git chart can refer to ../shared.yaml, but cannot leave its checkout.
	return checkoutPath(root, filepath.Join(src.Path, file))
}
