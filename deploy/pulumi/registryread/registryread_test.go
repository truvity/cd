package registryread_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pulumi/pulumi-aws/sdk/v7/go/aws"
	"github.com/pulumi/pulumi/sdk/v3/go/common/resource"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
	"github.com/truvity/k8s/pkg/aws/podidentity"

	"github.com/truvity/cd/deploy/pulumi/registryread"
)

type (
	recorded struct {
		typ, name string
		inputs    resource.PropertyMap
	}

	mocks struct {
		mu  sync.Mutex
		got []recorded
	}
)

func (m *mocks) NewResource(args pulumi.MockResourceArgs) (string, resource.PropertyMap, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.got = append(m.got, recorded{args.TypeToken, args.Name, args.Inputs})

	return args.Name + "_id", args.Inputs, nil
}

func (*mocks) Call(args pulumi.MockCallArgs) (resource.PropertyMap, error) {
	return args.Args, nil
}

func (m *mocks) find(t *testing.T, typ, name string) resource.PropertyMap {
	t.Helper()

	for _, r := range m.got {
		if r.typ == typ && r.name == name {
			return r.inputs
		}
	}

	t.Fatalf("no %s %s registered; got %v", typ, name, m.got)

	return nil
}

// arn is a registry's repositories ARN, built so no account ARN is spelled out.
func arn(partition, region, account string) string {
	return fmt.Sprintf("arn:%s:ecr:%s:%s:repository/*", partition, region, account)
}

func statements(t *testing.T, doc string) []map[string]any {
	t.Helper()

	var parsed struct {
		Version   string
		Statement []map[string]any
	}
	if err := json.Unmarshal([]byte(doc), &parsed); err != nil {
		t.Fatalf("policy document %q: %v", doc, err)
	}

	if parsed.Version != "2012-10-17" {
		t.Errorf("Version = %q", parsed.Version)
	}

	return parsed.Statement
}

func TestPolicyDocumentPull(t *testing.T) {
	t.Parallel()

	doc, err := registryread.PolicyDocument("", "region-1", "4242", false)
	if err != nil {
		t.Fatal(err)
	}

	want := `{"Statement":[{"Action":"ecr:GetAuthorizationToken","Effect":"Allow","Resource":"*","Sid":"ECRAuthToken"},` +
		`{"Action":["ecr:BatchGetImage","ecr:GetDownloadUrlForLayer","ecr:BatchCheckLayerAvailability"],"Effect":"Allow",` +
		`"Resource":"` + arn("aws", "region-1", "4242") + `","Sid":"ECRCrossAccountPull"}],"Version":"2012-10-17"}`
	if doc != want {
		t.Errorf("document:\n%s\nwant:\n%s", doc, want)
	}
}

func TestPolicyDocumentDiscover(t *testing.T) {
	t.Parallel()

	doc, err := registryread.PolicyDocument("aws-cn", "region-1", "4242", true)
	if err != nil {
		t.Fatal(err)
	}

	st := statements(t, doc)
	if len(st) != 2 || st[1]["Sid"] != "ECRCrossAccountPullAndDiscover" {
		t.Fatalf("statements %v", st)
	}

	actions, _ := st[1]["Action"].([]any)
	if len(actions) != 5 || actions[3] != registryread.ActionListImages || actions[4] != registryread.ActionDescribeImages {
		t.Errorf("discover actions %v", actions)
	}

	if st[1]["Resource"] != arn("aws-cn", "region-1", "4242") {
		t.Errorf("resource %v", st[1]["Resource"])
	}
}

func TestPolicyDocumentNeedsTheRegistry(t *testing.T) {
	t.Parallel()

	if _, err := registryread.PolicyDocument("", "", "4242", false); err == nil {
		t.Error("no region: want an error")
	}
}

func TestNewRegistersTheReader(t *testing.T) {
	t.Parallel()

	m := &mocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		provider, err := aws.NewProvider(ctx, "east", &aws.ProviderArgs{Region: pulumi.String("region-1")})
		if err != nil {
			return err
		}

		_, err = registryread.New(ctx, "east-kargo", registryread.Args{
			Provider:            provider,
			ClusterName:         "east",
			Region:              "region-1",
			AccountID:           "4343",
			PermissionsBoundary: "boundary",
			RoleName:            "east-kargo-registry",
			Description:         "Kargo reads the chart registry",
			Reader:              registryread.KargoController,
			RegistryAccountID:   "4242",
			RegistryRegion:      "region-2",
		},
			registryread.WithIgnoreAssociationTags(),
			registryread.WithNames(func(c podidentity.Child) string { return c.Component + "/" + string(c.Kind) }),
		)

		return err
	}, pulumi.WithMocks("project", "stack", m))
	if err != nil {
		t.Fatal(err)
	}

	policy := m.find(t, "aws:iam/policy:Policy", "east-kargo/policy")
	if got := policy["name"].StringValue(); got != "east-kargo-registry" {
		t.Errorf("policy name %q", got)
	}

	if got := policy["description"].StringValue(); got != "Kargo reads the chart registry" {
		t.Errorf("policy description %q", got)
	}

	st := statements(t, policy["policy"].StringValue())
	if st[1]["Resource"] != arn("aws", "region-2", "4242") || st[1]["Sid"] != "ECRCrossAccountPullAndDiscover" {
		t.Errorf("policy statement %v", st[1])
	}

	role := m.find(t, "aws:iam/role:Role", "east-kargo/role")
	trust := role["assumeRolePolicy"].StringValue()

	for _, want := range []string{`"kargo"`, `"kargo-controller"`, `cluster/east`, `"4343"`} {
		if !strings.Contains(trust, want) {
			t.Errorf("trust policy %s lacks %s", trust, want)
		}
	}

	assoc := m.find(t, "aws:eks/podIdentityAssociation:PodIdentityAssociation", "east-kargo/association")
	if assoc["namespace"].StringValue() != "kargo" || assoc["serviceAccount"].StringValue() != "kargo-controller" {
		t.Errorf("association %v", assoc)
	}
}

func TestNewRefusesAnEmptyDescription(t *testing.T) {
	t.Parallel()

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := registryread.New(ctx, "x", registryread.Args{Reader: registryread.ArgoCDRepoCreds, RegistryAccountID: "1", RegistryRegion: "r"})

		return err
	}, pulumi.WithMocks("project", "stack", &mocks{}))
	if err == nil || !strings.Contains(err.Error(), "Description is required") {
		t.Errorf("err = %v, want the Description refusal", err)
	}
}
