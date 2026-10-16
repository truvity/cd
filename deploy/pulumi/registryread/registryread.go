// Package registryread is the cross-account registry read identity of a
// delivery tool on EKS: an IAM role whose managed policy may fetch an ECR
// authorization token and pull (and, for a tool that discovers versions, list
// and describe) every repository of one registry account, bound to the tool's
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
		// RegistryAccountID and RegistryRegion name the registry read.
		RegistryAccountID string
		RegistryRegion    string
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

// PolicyDocument is the reader's managed policy: the token anywhere, and the
// pull (with discover, also the list and describe) actions on every repository
// of the registry account in its region.
func PolicyDocument(partition, registryRegion, registryAccountID string, discover bool) (string, error) {
	if registryRegion == "" || registryAccountID == "" {
		return "", errors.New("registryread: the registry's region and account ID are required")
	}

	if partition == "" {
		partition = "aws"
	}

	sid := "ECRCrossAccountPull"
	actions := []string{ActionBatchGetImage, ActionGetDownloadURL, ActionBatchCheck}

	if discover {
		sid = "ECRCrossAccountPullAndDiscover"
		actions = append(actions, ActionListImages, ActionDescribeImages)
	}

	// A map, so the keys are written sorted: the document is compared with
	// the live one byte for byte.
	raw, err := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{"Sid": "ECRAuthToken", "Effect": "Allow", "Action": ActionAuthToken, "Resource": "*"},
			{
				"Sid":      sid,
				"Effect":   "Allow",
				"Action":   actions,
				"Resource": fmt.Sprintf("arn:%s:ecr:%s:%s:repository/*", partition, registryRegion, registryAccountID),
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("registryread: policy document: %w", err)
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

	doc, err := PolicyDocument(partition, args.RegistryRegion, args.RegistryAccountID, args.Reader.Discover)
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
