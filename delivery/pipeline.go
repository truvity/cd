package delivery

import (
	"maps"
	"slices"

	"github.com/truvity/cd/appproject"
)

// The kinds of product cd-delivery renders. A ProductInput with no Kind is a
// KindProduct.
const (
	KindProduct    = "product"
	KindChart      = "chart"
	KindStaticSite = "static-site"
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
		// Git and Promotion are the product's own write-back target and
		// promotion settings, copied from the input; the zero values override
		// nothing. Kind is the product's kind, copied as given: empty is a
		// product.
		Git       Git
		Promotion Promotion
		Kind      string
	}

	// ProductInput is what a platform declares about a product's delivery.
	ProductInput struct {
		Name string
		// ChartRepository is the repository the product's chart is published
		// under (the chart is <ChartRepository>/<Name>); empty means
		// <DefaultRegistry>/<Name>/charts.
		ChartRepository string
		// Chart is the product whose charts this one installs (the chart is
		// <repository>/<Chart>, the default repository <DefaultRegistry>/
		// <Chart>/charts); empty means Name. A tenant of a product is its own
		// product -- namespace, pin, Stages and AppProjects named after it --
		// installing that product's charts.
		Chart       string
		Semver      string
		MetricsGate []string
		// E2EJob false: the product runs no e2e Job as part of delivery.
		E2EJob *bool
		// Git overrides where this product's promotions write and with which
		// credentials; Promotion overrides how they write. Both lay over the
		// release's settings in cd-pipeline, and the zero values override
		// nothing.
		Git       Git
		Promotion Promotion
		// Kind is the product's kind in cd-delivery (KindProduct, KindChart or
		// KindStaticSite). Empty is a product. It is carried to the row as
		// given and not checked here.
		Kind string
	}

	// Git is a product's override of cd-pipeline's release-wide git settings
	// (projects[].git). The zero value overrides nothing.
	Git struct {
		// RepoURL is the repository this product's promotions write the pin to;
		// empty keeps the release's.
		RepoURL string
		// Credentials replace the release's write-back credentials for this
		// product's namespace; nil keeps the release's.
		Credentials *GitCredentials
		// NoCredentials renders no write-back credentials for this product
		// (cd-pipeline's `credentials: false`). It wins over Credentials.
		NoCredentials bool
	}

	// GitCredentials is an ExternalSecret that produces the git credentials
	// Kargo writes back with (a GitHub App): the keys of cd-pipeline's
	// git.credentials.
	GitCredentials struct {
		SecretName      string
		RefreshInterval string
		SecretStoreRef  StoreRef
		RemoteKey       string
		Properties      GitCredentialProperties
	}

	// StoreRef names the secret store a credential is read from; Kind is
	// SecretStore or ClusterSecretStore.
	StoreRef struct {
		Kind string
		Name string
	}

	// GitCredentialProperties names the property of the remote key that each
	// credential is read from.
	GitCredentialProperties struct {
		AppID          string
		InstallationID string
		PrivateKey     string
	}

	// Promotion is a product's override of cd-pipeline's release-wide
	// promotion settings (projects[].promotion). An empty string, a nil slice
	// and a nil pointer keep the release's value. The zero value overrides
	// nothing.
	Promotion struct {
		Branch        string
		PinFile       string
		PinKey        string
		CommitMessage string
		PRTitle       string
		PRLabels      []string
		Provider      string
		Retry         *PromotionRetry
		Gate          *PromotionGate
	}

	// PromotionRetry is the retry budget of a promotion step. Zero fields keep
	// the release's.
	PromotionRetry struct {
		ErrorThreshold int
		Timeout        string
	}

	// PromotionGate is the budget of the wait on an upstream Application. A
	// zero field keeps the release's.
	PromotionGate struct {
		Timeout string
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

	chart := in.Chart
	if chart == "" {
		chart = in.Name
	}

	repo := in.ChartRepository
	if repo == "" {
		repo = defaultRegistry + "/" + chart + "/charts"
	}

	return ProductPipeline{
		Name:        in.Name,
		Key:         key,
		RepoURL:     repo + "/" + chart,
		Semver:      in.Semver,
		Clusters:    Owners(order, owned, key),
		MetricsGate: slices.Clone(in.MetricsGate),
		E2EJobOff:   in.E2EJob != nil && !*in.E2EJob,
		AppProjects: appproject.ForNamespace(appProjects, in.Name),
		Git:         in.Git.clone(),
		Promotion:   in.Promotion.clone(),
		Kind:        in.Kind,
	}
}

// clone copies g so that a row does not share memory with its input.
func (g Git) clone() Git {
	if g.Credentials != nil {
		c := *g.Credentials
		g.Credentials = &c
	}

	return g
}

// clone copies p so that a row does not share memory with its input.
func (p Promotion) clone() Promotion {
	p.PRLabels = slices.Clone(p.PRLabels)

	if p.Retry != nil {
		r := *p.Retry
		p.Retry = &r
	}

	if p.Gate != nil {
		g := *p.Gate
		p.Gate = &g
	}

	return p
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
