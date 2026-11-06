package delivery

import (
	"maps"
	"reflect"
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

// The optional fields of a product's row are carried, not interpreted, and
// their zero values leave the row exactly what it was before they existed.
func TestProductCarriesGitPromotionAndKind(t *testing.T) {
	t.Parallel()

	order := []string{"devel", "prod"}
	owned := map[string][]string{"devel": {"shopChart"}, "prod": {"shopChart"}}

	t.Run("zero values reproduce the row", func(t *testing.T) {
		t.Parallel()

		got := Product(ProductInput{Name: "shop", Semver: "^1"}, "oci://reg", order, owned, nil)
		want := ProductPipeline{
			Name:        "shop",
			Key:         "shopChart",
			RepoURL:     "oci://reg/shop/charts/shop",
			Semver:      "^1",
			Clusters:    []string{"devel", "prod"},
			AppProjects: got.AppProjects,
		}

		if !reflect.DeepEqual(got, want) {
			t.Errorf("Product = %#v, want %#v", got, want)
		}

		if got.Kind != "" {
			t.Errorf("Kind = %q, want it left empty", got.Kind)
		}
	})

	t.Run("carried to the row", func(t *testing.T) {
		t.Parallel()

		in := ProductInput{
			Name: "shop",
			Kind: KindChart,
			Git: Git{
				RepoURL: "https://git.example.com/acme/delivery.git",
				Credentials: &GitCredentials{
					SecretName:     "acme-writeback",
					SecretStoreRef: StoreRef{Kind: "ClusterSecretStore", Name: "acme"},
					RemoteKey:      "acme/app",
					Properties:     GitCredentialProperties{AppID: "app", InstallationID: "inst", PrivateKey: "key"},
				},
			},
			Promotion: Promotion{
				Branch:   "main",
				PinFile:  "pins/{stage}.yaml",
				PRLabels: []string{"promotion"},
				Retry:    &PromotionRetry{ErrorThreshold: 5, Timeout: "10m0s"},
				Gate:     &PromotionGate{Timeout: "20m0s"},
			},
		}
		got := Product(in, "oci://reg", order, owned, nil)

		if !reflect.DeepEqual(got.Git, in.Git) || !reflect.DeepEqual(got.Promotion, in.Promotion) || got.Kind != KindChart {
			t.Errorf("row = %+v, want git, promotion and kind of %+v", got, in)
		}

		// The row does not share memory with the input: a caller that reuses
		// its input for the next product cannot change this row.
		in.Git.Credentials.RemoteKey = "changed"
		in.Promotion.PRLabels[0] = "changed"
		in.Promotion.Retry.ErrorThreshold = 99
		in.Promotion.Gate.Timeout = "changed"

		if got.Git.Credentials.RemoteKey != "acme/app" || got.Promotion.PRLabels[0] != "promotion" ||
			got.Promotion.Retry.ErrorThreshold != 5 || got.Promotion.Gate.Timeout != "20m0s" {
			t.Errorf("row aliases its input: %+v", got)
		}
	})

	t.Run("no credentials", func(t *testing.T) {
		t.Parallel()

		got := Product(ProductInput{Name: "shop", Git: Git{NoCredentials: true}}, "oci://reg", order, owned, nil)
		if !got.Git.NoCredentials || got.Git.Credentials != nil {
			t.Errorf("Git = %+v", got.Git)
		}
	})
}
