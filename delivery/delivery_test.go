package delivery

import (
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/truvity/cd/appproject"
)

func TestPinKeyAndKargoProject(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ product, key string }{
		{"shop", "shopChart"}, {"shop-front", "shopFrontChart"}, {"a-b-c", "aBCChart"},
	} {
		if got := PinKey(tc.product); got != tc.key {
			t.Errorf("PinKey(%q) = %q, want %q", tc.product, got, tc.key)
		}

		if project, product := KargoProject(tc.key); project != tc.product || product != tc.product {
			t.Errorf("KargoProject(%q) = %q, %q; want the product %q twice", tc.key, project, product, tc.product)
		}
	}

	if project, product := KargoProject("argoRollouts"); project != "platform-argo-rollouts" || product != "" {
		t.Errorf("KargoProject(argoRollouts) = %q, %q", project, product)
	}

	got := KargoProjects(map[string][]string{"x": {"shopChart", "certManager"}, "y": {"shopChart"}})
	want := map[string]string{"shop": "shop", "platform-cert-manager": ""}

	if !maps.Equal(got, want) {
		t.Errorf("KargoProjects = %v, want %v", got, want)
	}
}

func TestAvailability(t *testing.T) {
	t.Parallel()

	for _, a := range []string{"", "single", "high", "none"} {
		if err := ValidAvailability(a); err != nil {
			t.Errorf("ValidAvailability(%q) = %v", a, err)
		}
	}

	if ValidAvailability("double") == nil {
		t.Error("ValidAvailability(double) passed")
	}

	if (Platform{Availability: "high", PostgresInstances: 2, PostgresStorage: "10Gi"}).Complete() != nil {
		t.Error("a complete platform was refused")
	}

	for _, p := range []Platform{
		{PostgresInstances: 1, PostgresStorage: "1Gi"},
		{Availability: "single", PostgresStorage: "1Gi"},
		{Availability: "single", PostgresInstances: 1},
	} {
		if p.Complete() == nil {
			t.Errorf("incomplete %+v passed", p)
		}
	}
}

func TestRenames(t *testing.T) {
	t.Parallel()

	got := Renames("shop", DefaultSuffixes, Suffixes{Infra: "-inf", App: "-app", E2E: "-app-e2e"})
	want := map[string]string{"shop-infra": "shop-inf", "shop": "shop-app", "shop-e2e": "shop-app-e2e"}

	if !maps.Equal(got, want) {
		t.Errorf("Renames = %v, want %v", got, want)
	}
}

func TestProberGranted(t *testing.T) {
	t.Parallel()

	prober := Peer{Namespace: "shop", ServiceAccount: "shop-e2e-prober"}
	peers := map[string][]Peer{"urls": {{Namespace: "x", ServiceAccount: "y"}, prober}, "redirect": {prober}}
	of := func(c string) []Peer { return peers[c] }

	if !ProberGranted("shop", DefaultProberComponents, of) {
		t.Error("granted on every component, but refused")
	}

	peers["redirect"] = nil
	if ProberGranted("shop", DefaultProberComponents, of) {
		t.Error("one component does not accept the prober, but granted")
	}
}

func TestProductAndPlatformCharts(t *testing.T) {
	t.Parallel()

	order := []string{"devel", "kernel", "stage", "prod"}
	owned := map[string][]string{
		"devel": {"shopChart", "certManager", "orphan"},
		"stage": {"shopChart", "certManager"},
		"prod":  {"shopChart"},
	}
	aps := []appproject.AppProject{appproject.Deployer("shop", "prod", "g"), appproject.Deployer("other", "prod", "g")}
	off := false

	p := Product(ProductInput{Name: "shop", Semver: "^1", MetricsGate: []string{"devel"}, E2EJob: &off}, "oci://reg", order, owned, aps)

	if p.Key != "shopChart" || p.RepoURL != "oci://reg/shop/charts/shop" || !slices.Equal(p.Clusters, []string{"devel", "stage", "prod"}) ||
		!p.E2EJobOff || len(p.AppProjects) != 1 || p.AppProjects[0].Name != "shop-prod" {
		t.Errorf("Product = %+v", p)
	}

	if q := Product(ProductInput{Name: "shop", ChartRepository: "oci://x/charts"}, "oci://reg", order, owned, nil); q.RepoURL != "oci://x/charts/shop" || q.E2EJobOff {
		t.Errorf("Product with its own repository = %+v", q)
	}

	// A tenant installs another product's charts under its own name.
	if q := Product(ProductInput{Name: "shop-ie", Chart: "shop"}, "oci://reg", order, owned, nil); q.RepoURL != "oci://reg/shop/charts/shop" || q.Key != "shopIeChart" || q.Name != "shop-ie" {
		t.Errorf("tenant product = %+v", q)
	}

	if q := Product(ProductInput{Name: "shop-ie", Chart: "shop", ChartRepository: "oci://x/charts"}, "oci://reg", order, owned, nil); q.RepoURL != "oci://x/charts/shop" {
		t.Errorf("tenant product with repository = %+v", q)
	}

	charts, missing, unowned := PlatformCharts(order, owned, map[string]bool{"shopChart": true},
		map[string]ChartSpec{"certManager": {RepoURL: "r", Semver: "1"}, "unused": {}})

	if len(charts) != 1 || charts[0].Key != "certManager" || charts[0].Name != "cert-manager" || !slices.Equal(charts[0].Clusters, []string{"devel", "stage"}) {
		t.Errorf("charts = %+v", charts)
	}

	if !slices.Equal(missing["orphan"], []string{"devel"}) || len(missing) != 1 {
		t.Errorf("missing = %v", missing)
	}

	if !slices.Equal(unowned, []string{"unused"}) {
		t.Errorf("unowned = %v", unowned)
	}
}

func TestBuildPipelines(t *testing.T) {
	t.Parallel()

	order := []string{"devel", "kernel", "stage", "prod"}
	owned := map[string][]string{
		"devel": {"shopChart", "ghostChart", "certManager"},
		"stage": {"shopChart", "certManager"},
		"prod":  {"shopChart", "ghostChart"},
	}
	aps := []appproject.AppProject{appproject.Deployer("shop", "prod", "g"), appproject.Deployer("ghost", "prod", "g")}
	in := PipelinesInput{
		Order: order, Owned: owned, DefaultRegistry: "oci://reg", AppProjects: aps,
		Products: []PipelineProduct{
			{ProductInput: ProductInput{Name: "shop", Semver: "^1"}},
			// Skipped on every cluster that owns it: the row stays, with no Stage.
			{ProductInput: ProductInput{Name: "ghost", Semver: "^1"}, SkipClusters: []string{"devel", "prod"}},
		},
		Charts: map[string]ChartSpec{"certManager": {RepoURL: "r", Semver: "1"}},
	}

	got, err := BuildPipelines(in)
	if err != nil {
		t.Fatal(err)
	}

	if len(got.Products) != 2 || got.Products[0].Name != "shop" || !slices.Equal(got.Products[0].Clusters, []string{"devel", "stage", "prod"}) {
		t.Errorf("shop = %+v", got.Products)
	}

	if len(got.Products[1].Clusters) != 0 || len(got.Products[1].AppProjects) != 1 {
		t.Errorf("ghost = %+v", got.Products[1])
	}

	if len(got.Charts) != 1 || got.Charts[0].Key != "certManager" {
		t.Errorf("charts = %+v", got.Charts)
	}

	// The caller's owned map is not changed by a skip.
	if !slices.Equal(owned["devel"], []string{"shopChart", "ghostChart", "certManager"}) {
		t.Errorf("owned changed: %v", owned)
	}

	for name, tc := range map[string]struct {
		mutate func(in *PipelinesInput)
		want   error
	}{
		"pin owned nowhere": {func(in *PipelinesInput) {
			in.Products = append(in.Products, PipelineProduct{ProductInput: ProductInput{Name: "lost"}})
		}, ErrPinUnowned},
		"chart without spec": {func(in *PipelinesInput) { in.Charts = nil }, ErrChartWithoutSpec},
		"spec without owner": {func(in *PipelinesInput) {
			in.Charts = map[string]ChartSpec{"certManager": {}, "unused": {}}
		}, ErrSpecUnowned},
		"uncovered AppProject": {func(in *PipelinesInput) {
			in.AppProjects = append(slices.Clone(in.AppProjects), appproject.Deployer("stray", "prod", "g"))
		}, ErrAppProjectUncovered},
	} {
		bad := in
		tc.mutate(&bad)

		if _, err := BuildPipelines(bad); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

func TestWithoutPin(t *testing.T) {
	t.Parallel()

	owned := map[string][]string{"a": {"x", "y"}, "b": {"x"}}
	got := WithoutPin(owned, "x", []string{"a"})

	if !slices.Equal(got["a"], []string{"y"}) || !slices.Equal(got["b"], []string{"x"}) || !slices.Equal(owned["a"], []string{"x", "y"}) {
		t.Errorf("WithoutPin = %v (owned %v)", got, owned)
	}
}
