package appproject

import (
	"slices"
	"testing"
)

func TestDeployer(t *testing.T) {
	t.Parallel()

	got := Deployer("shop", "stage", "stage:shop:deployer")

	want := AppProject{
		Name: "shop-stage", DestCluster: "stage", DestNamespace: "shop",
		Roles: []Role{{
			Name:   "deployer",
			Groups: []string{"stage:shop:deployer"},
			Policies: []string{
				"p, proj:shop-stage:deployer, applications, get, shop-stage/*, allow",
				"p, proj:shop-stage:deployer, applications, create, shop-stage/*, allow",
				"p, proj:shop-stage:deployer, applications, sync, shop-stage/*, allow",
				"p, proj:shop-stage:deployer, applications, delete, shop-stage/*, allow",
			},
		}},
	}

	if got.Name != want.Name || got.DestCluster != want.DestCluster || got.DestNamespace != want.DestNamespace ||
		len(got.Roles) != 1 || got.Roles[0].Name != want.Roles[0].Name ||
		!slices.Equal(got.Roles[0].Groups, want.Roles[0].Groups) || !slices.Equal(got.Roles[0].Policies, want.Roles[0].Policies) {
		t.Errorf("Deployer = %+v, want %+v", got, want)
	}
}

func TestSortAndForNamespace(t *testing.T) {
	t.Parallel()

	aps := []AppProject{Deployer("b", "prod", "g"), Deployer("a", "prod", "g"), Deployer("a", "devel", "g")}
	Sort(aps)

	var names []string
	for _, ap := range aps {
		names = append(names, ap.Name)
	}

	if want := []string{"a-devel", "a-prod", "b-prod"}; !slices.Equal(names, want) {
		t.Errorf("sorted = %v, want %v", names, want)
	}

	if got := ForNamespace(aps, "a"); len(got) != 2 || got[0].Name != "a-devel" || got[1].Name != "a-prod" {
		t.Errorf("ForNamespace(a) = %+v", got)
	}
}
