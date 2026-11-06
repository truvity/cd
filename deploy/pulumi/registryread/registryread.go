// Package registryread is the cross-account registry read identity of a
// delivery tool on EKS: an IAM role whose managed policy may fetch an ECR
// authorization token and pull (and, for a tool that discovers versions, list
// and describe) every repository of one or several registry accounts, bound to the tool's
// ServiceAccount by an EKS Pod Identity association.
//
// Two tools of this repository's pipeline read charts that way: Argo CD, whose
// repository credentials argocd-ecr-updater refreshes with a registry token
// ([ArgoCDRepoCreds]), and Kargo, whose Warehouses discover chart versions as
// well as pulling them ([KargoController]). Each carries its own identity, so
// the two are audited and revoked independently.
//
// The resources are registered through truvity/k8s's podidentity component.
// An installation that already runs such an identity keeps its Pulumi URNs and
// adopts the live objects with the options: [WithNames] (the children's
// logical names), [WithImports] (the IDs of what exists),
// [WithIgnoreAssociationTags] and [WithResourceOptions] (options of the
// component itself).
package registryread

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/truvity/k8s/pkg/aws/podidentity"
)

// The actions a reader is granted.
const (
	ActionAuthToken      = "ecr:GetAuthorizationToken"
	ActionBatchGetImage  = "ecr:BatchGetImage"
	ActionGetDownloadURL = "ecr:GetDownloadUrlForLayer"
	ActionBatchCheck     = "ecr:BatchCheckLayerAvailability"
	ActionListImages     = "ecr:ListImages"
	ActionDescribeImages = "ecr:DescribeImages"
)

// readActions are the only actions a reader may be granted.
var readActions = map[string]bool{
	ActionAuthToken: true, ActionBatchGetImage: true, ActionGetDownloadURL: true,
	ActionBatchCheck: true, ActionListImages: true, ActionDescribeImages: true,
}

type (
	// Reader is a tool that reads from the registry: the ServiceAccount it
	// runs as, and whether it discovers versions (lists and describes images)
	// as well as pulling them.
	Reader struct {
		Namespace      string
		ServiceAccount string
		Discover       bool
	}

	// Args configures one reader's identity.
	Args struct {
		// Provider is the AWS provider of the cluster's account.
		Provider pulumi.ProviderResource
		// ClusterName, Region and AccountID name the EKS cluster the
		// association is made in.
		ClusterName string
		Region      string
		AccountID   string
		// Partition is the AWS partition of every ARN built here; default "aws".
		Partition string
		// PermissionsBoundary is the role's permissions boundary ARN, if any.
		PermissionsBoundary string
		// RoleName names the role and its managed policy.
		RoleName string
		// Description is the managed policy's. IAM cannot change it once set,
		// so adopting a live policy needs its live value. Required.
		Description string
		// Reader is the tool the identity is for.
		Reader Reader
		// RegistryAccountID and RegistryRegion name the one registry read
		// (every repository of it). They are exactly one implicit entry of
		// Registries.
		RegistryAccountID string
		RegistryRegion    string
		// Registries names the registries to read, each with the repositories
		// of it (none: every repository). Not together with RegistryAccountID
		// and RegistryRegion. Argo CD's registry token comes from
		// argocd-ecr-updater, which is configured with one region of its own:
		// registries in another region than its token's need that too.
		Registries []Registry
	}

	// Registry is one registry an identity reads: an account and region, and
	// the repositories of it (empty: every repository).
	Registry struct {
		AccountID    string
		Region       string
		Repositories []string
	}

	// Option adjusts the registration, for an installation that adopts an
	// existing identity.
	Option func(*settings)

	settings struct {
		names      podidentity.NameFunc
		imports    podidentity.Imports
		ignoreTags bool
		opts       []pulumi.ResourceOption
	}
)

var (
	// ArgoCDRepoCreds is argocd-ecr-updater, which refreshes Argo CD's
	// repository credentials with a registry token. It pulls.
	ArgoCDRepoCreds = Reader{Namespace: "argocd", ServiceAccount: "argocd-ecr-updater"}

	// KargoController is Kargo's controller: a Warehouse subscribed to a
	// chart repository lists and describes its versions as well as pulling.
	KargoController = Reader{Namespace: "kargo", ServiceAccount: "kargo-controller", Discover: true}
)

// WithNames gives the children the logical names f returns (they are part of
// their URNs). Default: podidentity.DefaultName.
func WithNames(f podidentity.NameFunc) Option {
	return func(s *settings) { s.names = f }
}

// WithImports adopts the objects that exist already (their IDs must be known
// when the program declares them). See podidentity.Imports for the
// association's ID, which must not stay set once the association is in state.
func WithImports(imp podidentity.Imports) Option {
	return func(s *settings) { s.imports = imp }
}

// WithIgnoreAssociationTags ignores tag changes on the association.
func WithIgnoreAssociationTags() Option {
	return func(s *settings) { s.ignoreTags = true }
}

// WithResourceOptions passes options to the component itself (a parent, an
// alias, a provider map).
func WithResourceOptions(opts ...pulumi.ResourceOption) Option {
	return func(s *settings) { s.opts = append(s.opts, opts...) }
}

// maxPolicySize is IAM's limit on a managed policy, counted without whitespace.
const maxPolicySize = 6144

// PolicyDocument is the reader's managed policy: the token anywhere, and the
// pull (with discover, also the list and describe) actions on every repository
// of the registry account in its region.
func PolicyDocument(partition, registryRegion, registryAccountID string, discover bool) (string, error) {
	return PolicyDocumentFor(partition, []Registry{{AccountID: registryAccountID, Region: registryRegion}}, discover)
}

// PolicyDocumentFor is the reader's managed policy over several registries:
// the token anywhere, and the pull (with discover, also the list and describe)
// actions on the repositories of each registry (every repository when a
// registry names none). One registry with every repository gives the document
// [PolicyDocument] has always given, byte for byte; otherwise the statement's
// Resource is a sorted, de-duplicated list. It refuses an empty or duplicate
// registry, an account or region that is not one name (a wildcard would widen
// the grant), a repository pattern that is empty, has whitespace, climbs with
// `..` or is rooted, and a policy over IAM's managed-policy size limit.
func PolicyDocumentFor(partition string, regs []Registry, discover bool) (string, error) {
	if len(regs) == 0 {
		return "", errors.New("registryread: at least one registry is required")
	}

	if partition == "" {
		partition = "aws"
	}

	seen := map[string]bool{}
	resources := map[string]bool{}

	for _, r := range regs {
		if r.Region == "" || r.AccountID == "" {
			return "", errors.New("registryread: the registry's region and account ID are required")
		}

		for what, v := range map[string]string{"region": r.Region, "account ID": r.AccountID} {
			if strings.ContainsAny(v, "*?:/ \t\r\n") {
				return "", fmt.Errorf("registryread: the registry's %s %q must not contain a wildcard, a separator or whitespace", what, v)
			}
		}

		key := r.AccountID + "/" + r.Region
		if seen[key] {
			return "", fmt.Errorf("registryread: registry %s in %s is listed twice", r.AccountID, r.Region)
		}

		seen[key] = true

		repos := r.Repositories
		if len(repos) == 0 {
			repos = []string{"*"}
		}

		for _, repo := range repos {
			if repo == "" || strings.HasPrefix(repo, "/") || strings.Contains(repo, "..") || strings.ContainsAny(repo, " \t\r\n") {
				return "", fmt.Errorf("registryread: invalid repository pattern %q (not empty, rooted, with whitespace or `..`)", repo)
			}

			resources[fmt.Sprintf("arn:%s:ecr:%s:%s:repository/%s", partition, r.Region, r.AccountID, repo)] = true
		}
	}

	sid := "ECRCrossAccountPull"
	actions := []string{ActionBatchGetImage, ActionGetDownloadURL, ActionBatchCheck}

	if discover {
		sid = "ECRCrossAccountPullAndDiscover"
		actions = append(actions, ActionListImages, ActionDescribeImages)
	}

	// Only these read actions are ever granted, and "*" only to the token.
	for _, a := range actions {
		if !readActions[a] {
			return "", fmt.Errorf("registryread: %s is not a read action", a)
		}
	}

	list := slices.Sorted(maps.Keys(resources))

	var resource any = list
	if len(list) == 1 {
		resource = list[0]
	}

	// A map, so the keys are written sorted: the document is compared with
	// the live one byte for byte.
	raw, err := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{"Sid": "ECRAuthToken", "Effect": "Allow", "Action": ActionAuthToken, "Resource": "*"},
			{"Sid": sid, "Effect": "Allow", "Action": actions, "Resource": resource},
		},
	})
	if err != nil {
		return "", fmt.Errorf("registryread: policy document: %w", err)
	}

	if n := len(strings.Join(strings.Fields(string(raw)), "")); n > maxPolicySize {
		return "", fmt.Errorf("registryread: the policy is %d characters, over the managed policy limit of %d: name fewer repositories", n, maxPolicySize)
	}

	return string(raw), nil
}

// New registers the reader's identity as the podidentity component `name`.
func New(ctx *pulumi.Context, name string, args Args, opts ...Option) (*podidentity.PodIdentity, error) {
	var s settings
	for _, o := range opts {
		o(&s)
	}

	if args.Description == "" {
		return nil, fmt.Errorf("registryread %s: Description is required (IAM cannot change it once set)", name)
	}

	if args.Reader.Namespace == "" || args.Reader.ServiceAccount == "" {
		return nil, fmt.Errorf("registryread %s: Reader needs a namespace and a service account", name)
	}

	partition := args.Partition
	if partition == "" {
		partition = "aws"
	}

	regs := args.Registries

	switch {
	case len(regs) > 0 && (args.RegistryAccountID != "" || args.RegistryRegion != ""):
		return nil, fmt.Errorf("registryread %s: set Registries, or RegistryAccountID and RegistryRegion, not both", name)
	case len(regs) == 0:
		regs = []Registry{{AccountID: args.RegistryAccountID, Region: args.RegistryRegion}}
	}

	doc, err := PolicyDocumentFor(partition, regs, args.Reader.Discover)
	if err != nil {
		return nil, fmt.Errorf("registryread %s: %w", name, err)
	}

	return podidentity.New(ctx, name, &podidentity.Args{
		Provider:            args.Provider,
		ClusterName:         pulumi.String(args.ClusterName),
		Region:              args.Region,
		Namespace:           args.Reader.Namespace,
		ServiceAccounts:     []string{args.Reader.ServiceAccount},
		RoleName:            args.RoleName,
		PermissionsBoundary: args.PermissionsBoundary,
		AccountID:           args.AccountID,
		ClusterARN:          fmt.Sprintf("arn:%s:eks:%s:%s:cluster/%s", partition, args.Region, args.AccountID, args.ClusterName),
		ManagedPolicy: &podidentity.ManagedPolicy{
			Name:        args.RoleName,
			Description: args.Description,
			Document:    pulumi.String(doc),
		},
		IgnoreAssociationTags: s.ignoreTags,
		Imports:               s.imports,
		Names:                 s.names,
	}, s.opts...)
}
