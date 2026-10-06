// Package appproject derives the Argo CD AppProjects that confine a product's
// deployers to the product's namespace on one cluster: the rows that
// cd-argocd's and cd-pipeline's `appProjects` values render.
//
// A product deployed to a cluster gets one AppProject, `<product>-<cluster>`,
// whose only destination is the product's namespace on that cluster, with a
// `deployer` role bound to one group. The caller decides which products are
// deployed where and which group holds the role; this package names the
// objects and writes the policy lines.
package appproject

import (
	"fmt"
	"slices"
	"strings"
)

type (
	// AppProject is one AppProject row: its name, its single destination
	// and its roles.
	AppProject struct {
		Name          string `yaml:"name"`
		DestCluster   string `yaml:"destCluster"`
		DestNamespace string `yaml:"destNamespace"`
		Roles         []Role `yaml:"roles,omitempty"`
	}

	// Role is one AppProject role: the groups that hold it and its policy
	// lines.
	Role struct {
		Name     string   `yaml:"name"`
		Groups   []string `yaml:"groups,omitempty"`
		Policies []string `yaml:"policies,omitempty"`
	}
)

// DeployerRole is the role a product's deployers hold.
const DeployerRole = "deployer"

// DeployerActions are what a deployer may do to the Applications of its
// AppProject: everything but override.
var DeployerActions = []string{"get", "create", "sync", "delete"}

// Name is the AppProject of a product on a cluster.
func Name(product, cluster string) string {
	return product + "-" + cluster
}

// Policies are the Casbin lines that allow role of appProject each action on
// the Applications of the AppProject.
func Policies(appProject, role string, actions []string) []string {
	out := make([]string, 0, len(actions))

	for _, action := range actions {
		out = append(out, fmt.Sprintf("p, proj:%s:%s, applications, %s, %s/*, allow", appProject, role, action, appProject))
	}

	return out
}

// Deployer is a product's AppProject on a cluster: destination the product's
// namespace (named after the product), one deployer role held by group.
func Deployer(product, cluster, group string) AppProject {
	name := Name(product, cluster)

	return AppProject{
		Name:          name,
		DestCluster:   cluster,
		DestNamespace: product,
		Roles: []Role{{
			Name:     DeployerRole,
			Groups:   []string{group},
			Policies: Policies(name, DeployerRole, DeployerActions),
		}},
	}
}

// Sort orders AppProjects by name, the order every render lists them in.
func Sort(aps []AppProject) {
	slices.SortFunc(aps, func(a, b AppProject) int { return strings.Compare(a.Name, b.Name) })
}

// ForNamespace returns the AppProjects whose destination is namespace, in
// their order.
func ForNamespace(aps []AppProject, namespace string) []AppProject {
	var out []AppProject

	for _, ap := range aps {
		if ap.DestNamespace == namespace {
			out = append(out, ap)
		}
	}

	return out
}
