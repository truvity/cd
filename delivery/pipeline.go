package delivery

import (
	"maps"
	"slices"

	"github.com/truvity/cd/appproject"
)

type (
	// ProductPipeline is one product's Kargo pipeline row: the chart a
	// Warehouse subscribes to, the clusters its Stages promote through (in
	// promotion order), where the metrics gate runs and the product's
	// AppProjects.
	ProductPipeline struct {
		Name        string
		Key         string
		RepoURL     string
		Semver      string
		Clusters    []string
		MetricsGate []string
		// E2EJobOff is true when the product's e2e Job is not part of delivery.
		E2EJobOff   bool
		AppProjects []appproject.AppProject
	}

	// ProductInput is what a platform declares about a product's delivery.
	ProductInput struct {
		Name string
		// ChartRepository is the repository the product's chart is published
		// under (the chart is <ChartRepository>/<Name>); empty means
		// <DefaultRegistry>/<Name>/charts.
		ChartRepository string
		Semver          string
		MetricsGate     []string
		// E2EJob false: the product runs no e2e Job as part of delivery.
		E2EJob *bool
	}

	// ChartSpec is a platform chart's pipeline as declared: what its Warehouse
	// subscribes to and how its Stages are laid out.
	ChartSpec struct {
		RepoURL             string
		ChartName           string
		VersionPrefix       string
		Semver              string
		Apps                []string
		AppsByCluster       map[string][]string
		PromotionChain      map[string][]string
		AutoPromoteClusters []string
	}

	// ChartPipeline is a platform chart's pipeline row: its spec, its Kargo
	// name and the clusters that own its pin, in promotion order.
	ChartPipeline struct {
		ChartSpec

		Key      string
		Name     string
		Clusters []string
	}
)

// Owners lists the clusters of order that own key (owned maps a cluster to
// the pins it owns), in order. A cluster absent from owned owns nothing.
func Owners(order []string, owned map[string][]string, key string) []string {
	var out []string

	for _, cluster := range order {
		if keys, ok := owned[cluster]; ok && slices.Contains(keys, key) {
			out = append(out, cluster)
		}
	}

	return out
}

// Product builds a product's pipeline row: its pin key, the clusters owning
// it in order, its chart, and the AppProjects (of all of them) whose
// destination is the product's namespace. Clusters is empty when no cluster
// owns the pin; the caller decides whether that is an error.
func Product(in ProductInput, defaultRegistry string, order []string, owned map[string][]string, appProjects []appproject.AppProject) ProductPipeline {
	key := PinKey(in.Name)

	repo := in.ChartRepository
	if repo == "" {
		repo = defaultRegistry + "/" + in.Name + "/charts"
	}

	return ProductPipeline{
		Name:        in.Name,
		Key:         key,
		RepoURL:     repo + "/" + in.Name,
		Semver:      in.Semver,
		Clusters:    Owners(order, owned, key),
		MetricsGate: slices.Clone(in.MetricsGate),
		E2EJobOff:   in.E2EJob != nil && !*in.E2EJob,
		AppProjects: appproject.ForNamespace(appProjects, in.Name),
	}
}

// PlatformCharts joins the platform charts' specs with the clusters owning
// each pin, in promotion order, for every pin some cluster of order owns that
// is not a product's (products). The rows are sorted by key. Missing maps a
// pin that is owned but has no spec to its owners (nothing would promote it);
// unowned lists the specs no cluster owns, sorted. The caller decides whether
// either is an error.
func PlatformCharts(order []string, owned map[string][]string, products map[string]bool, specs map[string]ChartSpec) (charts []ChartPipeline, missing map[string][]string, unowned []string) {
	owners := map[string][]string{}

	for _, cluster := range order {
		keys, ok := owned[cluster]
		if !ok {
			continue
		}

		for _, key := range keys {
			if !products[key] {
				owners[key] = append(owners[key], cluster)
			}
		}
	}

	for _, key := range slices.Sorted(maps.Keys(owners)) {
		spec, ok := specs[key]
		if !ok {
			if missing == nil {
				missing = map[string][]string{}
			}

			missing[key] = owners[key]

			continue
		}

		charts = append(charts, ChartPipeline{ChartSpec: spec, Key: key, Name: KebabCase(key), Clusters: owners[key]})
	}

	for _, key := range slices.Sorted(maps.Keys(specs)) {
		if _, ok := owners[key]; !ok {
			unowned = append(unowned, key)
		}
	}

	return charts, missing, unowned
}
