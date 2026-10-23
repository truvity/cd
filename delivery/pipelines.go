package delivery

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/truvity/cd/appproject"
)

var (
	// ErrPinUnowned: a product that has a pipeline, but whose pin no cluster
	// owns, so no Stage could ever promote it.
	ErrPinUnowned = errors.New("no cluster owns the product's pin")
	// ErrChartWithoutSpec: a platform chart's pin is owned somewhere but has no
	// spec, so no Warehouse would promote it.
	ErrChartWithoutSpec = errors.New("a platform chart's pin is owned but has no spec")
	// ErrSpecUnowned: a platform chart's spec that no cluster owns the pin of.
	ErrSpecUnowned = errors.New("a platform chart's spec is owned by no cluster")
	// ErrAppProjectUncovered: an AppProject whose namespace belongs to no
	// product with a pipeline; it would silently vanish with the list it
	// replaces.
	ErrAppProjectUncovered = errors.New("an AppProject belongs to no product with a pipeline")
)

type (
	// PipelineProduct is a product with a pipeline, as a platform declares it.
	PipelineProduct struct {
		ProductInput

		// SkipClusters are the clusters the product is not installed on: it has
		// no Stage there, so its pin is not promoted to them. With no cluster
		// left, the row keeps only its AppProjects.
		SkipClusters []string
	}

	// PipelinesInput is everything the pipelines of a platform derive from.
	PipelinesInput struct {
		// Order is the promotion order of the clusters (the first takes Freight
		// from the Warehouse).
		Order []string
		// Owned maps a cluster to the pins its Kargo owns.
		Owned map[string][]string
		// DefaultRegistry is the registry a product's charts are under when it
		// names no repository (see ProductInput).
		DefaultRegistry string
		// AppProjects are the AppProjects of every product (appproject.Deployer),
		// each handed to the product whose namespace it is.
		AppProjects []appproject.AppProject
		// Products are the products that have a pipeline, in the order their
		// rows are wanted.
		Products []PipelineProduct
		// Charts are the platform charts' specs by pin key.
		Charts map[string]ChartSpec
	}

	// Pipelines are the rows cd-pipeline renders: one per product, one per
	// platform chart.
	Pipelines struct {
		Products []ProductPipeline
		Charts   []ChartPipeline
	}
)

// WithoutPin is owned without key on the clusters listed (owned is not
// changed).
func WithoutPin(owned map[string][]string, key string, clusters []string) map[string][]string {
	out := make(map[string][]string, len(owned))

	for cluster, keys := range owned {
		if !slices.Contains(clusters, cluster) {
			out[cluster] = keys

			continue
		}

		out[cluster] = slices.DeleteFunc(slices.Clone(keys), func(k string) bool { return k == key })
	}

	return out
}

// BuildPipelines derives the pipeline rows of a platform: a row per product
// (Product, with the product's SkipClusters taken out of the owners), and a
// row per platform chart (PlatformCharts) for every pin that is not a
// product's. It refuses what would silently drop an object: a product no
// cluster owns the pin of, a platform chart owned with no spec or specified
// with no owner, and an AppProject no product covers. The errors wrap the
// package's Err values and name the offender.
func BuildPipelines(in PipelinesInput) (*Pipelines, error) {
	out := &Pipelines{}
	covered := map[string]bool{}
	business := map[string]bool{}

	for i := range in.Products {
		p := &in.Products[i]

		// A product the platform does not install on a cluster has no Stage there:
		// nothing to promote to. The ownership check below is against the whole
		// of Owned, so a product skipped everywhere keeps its row (its
		// AppProjects) without being an error.
		owned := in.Owned
		if len(p.SkipClusters) > 0 {
			owned = WithoutPin(in.Owned, PinKey(p.Name), p.SkipClusters)
		}

		row := Product(p.ProductInput, in.DefaultRegistry, in.Order, owned, in.AppProjects)

		if len(Owners(in.Order, in.Owned, row.Key)) == 0 {
			return nil, fmt.Errorf("delivery: product %s, pin %s: %w", p.Name, row.Key, ErrPinUnowned)
		}

		for _, ap := range row.AppProjects {
			covered[ap.Name] = true
		}

		business[row.Key] = true

		out.Products = append(out.Products, row)
	}

	charts, missing, unowned := PlatformCharts(in.Order, in.Owned, business, in.Charts)

	if len(missing) > 0 {
		key := slices.Sorted(maps.Keys(missing))[0]

		return nil, fmt.Errorf("delivery: pin %s is owned on %s: %w", key, strings.Join(missing[key], ", "), ErrChartWithoutSpec)
	}

	if len(unowned) > 0 {
		return nil, fmt.Errorf("delivery: chart %s: %w", unowned[0], ErrSpecUnowned)
	}

	out.Charts = charts

	for _, ap := range in.AppProjects {
		if !covered[ap.Name] {
			return nil, fmt.Errorf("delivery: AppProject %s (namespace %s): %w", ap.Name, ap.DestNamespace, ErrAppProjectUncovered)
		}
	}

	return out, nil
}
