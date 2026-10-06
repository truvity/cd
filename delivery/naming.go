package delivery

import "strings"

// pinKeySuffix ends the version pin of a product's chart.
const pinKeySuffix = "Chart"

// KargoControllerNamespaces are the namespaces a cd-kargo install keeps for
// itself (the upstream chart's defaults): cluster-wide secrets, shared
// resources and system resources.
var KargoControllerNamespaces = []string{"kargo-cluster-secrets", "kargo-shared-resources", "kargo-system-resources"}

// PinKey is the version pin a pipeline promotes for a product's chart:
// `<camelCase product>Chart` (shop-front -> shopFrontChart).
func PinKey(product string) string {
	var b strings.Builder

	upper := false

	for _, r := range product {
		if r == '-' {
			upper = true

			continue
		}

		if upper && r >= 'a' && r <= 'z' {
			r -= 'a' - 'A'
		}

		upper = false

		b.WriteRune(r)
	}

	return b.String() + pinKeySuffix
}

// KebabCase turns a camelCase pin key into kebab-case (argoRollouts ->
// argo-rollouts).
func KebabCase(s string) string {
	var b strings.Builder

	for i, r := range s {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('-')
			}

			r += 'a' - 'A'
		}

		b.WriteRune(r)
	}

	return b.String()
}

// KargoProject is the Kargo Project (and namespace) whose pipeline promotes a
// pin, and the product it belongs to. A product's pin (PinKey) has the
// Project named after the product; any other pin is a platform chart's,
// Project `platform-<kebab-case key>`, and product is "".
func KargoProject(pinKey string) (project, product string) {
	if p, ok := strings.CutSuffix(pinKey, pinKeySuffix); ok {
		name := KebabCase(p)

		return name, name
	}

	return "platform-" + KebabCase(pinKey), ""
}

// KargoProjects maps the Kargo Project of every pin in owned (cluster to the
// pins it owns) to its product ("" for a platform chart).
func KargoProjects(owned map[string][]string) map[string]string {
	out := map[string]string{}

	for _, keys := range owned {
		for _, key := range keys {
			project, product := KargoProject(key)
			out[project] = product
		}
	}

	return out
}
