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

func TestDeployerInNamespace(t *testing.T) {
	t.Parallel()

	base := Deployer("shop-globex", "stage", "g")

	got := Deployer("shop-globex", "stage", "g", InNamespace("finttech-shop-globex"))
	if got.Name != "shop-globex-stage" || got.DestNamespace != "finttech-shop-globex" || got.DestCluster != "stage" {
		t.Errorf("Deployer in a namespace = %+v", got)
	}

	// Only the destination moves: the name, the role and the policies stay.
	if got.Name != base.Name || !slices.Equal(got.Roles[0].Policies, base.Roles[0].Policies) ||
		!slices.Equal(got.Roles[0].Groups, base.Roles[0].Groups) {
		t.Errorf("Deployer in a namespace = %+v, base %+v", got, base)
	}

	// Unset or empty is the product's name, as without the option.
	if d := Deployer("shop-globex", "stage", "g", InNamespace("")); d.DestNamespace != "shop-globex" {
		t.Errorf("empty namespace = %q, want the product's name", d.DestNamespace)
	}

	if base.DestNamespace != "shop-globex" {
		t.Errorf("default namespace = %q", base.DestNamespace)
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
