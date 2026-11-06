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

// arnRepo is the ARN of the repositories of a registry that match repo.
func arnRepo(partition, region, account, repo string) string {
	return strings.TrimSuffix(arn(partition, region, account), "*") + repo
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

// The two documents below are the ones a single registry has always produced;
// they are compared byte for byte with the live policy, so they are spelled
// out here, and these tests must pass before and after the multi-registry
// support.
//
// (The ARN is built by arn, so no ARN is spelled out in the repository.)
var (
	livePull = `{"Statement":[{"Action":"ecr:GetAuthorizationToken","Effect":"Allow","Resource":"*","Sid":"ECRAuthToken"},` +
		`{"Action":["ecr:BatchGetImage","ecr:GetDownloadUrlForLayer","ecr:BatchCheckLayerAvailability"],"Effect":"Allow",` +
		`"Resource":"` + arn("aws", "region-1", "4242") + `","Sid":"ECRCrossAccountPull"}],"Version":"2012-10-17"}`
	liveDiscover = `{"Statement":[{"Action":"ecr:GetAuthorizationToken","Effect":"Allow","Resource":"*","Sid":"ECRAuthToken"},` +
		`{"Action":["ecr:BatchGetImage","ecr:GetDownloadUrlForLayer","ecr:BatchCheckLayerAvailability","ecr:ListImages","ecr:DescribeImages"],"Effect":"Allow",` +
		`"Resource":"` + arn("aws", "region-1", "4242") + `","Sid":"ECRCrossAccountPullAndDiscover"}],"Version":"2012-10-17"}`
)

func TestPolicyDocumentSingleRegistryByteIdentical(t *testing.T) {
	t.Parallel()

	for discover, want := range map[bool]string{false: livePull, true: liveDiscover} {
		got, err := registryread.PolicyDocument("", "region-1", "4242", discover)
		if err != nil {
			t.Fatal(err)
		}

		if got != want {
			t.Errorf("discover=%v: document changed:\n%s\nwant:\n%s", discover, got, want)
		}
	}
}

func TestNewSingularFieldsKeepTheLivePolicy(t *testing.T) {
	t.Parallel()

	m := &mocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		provider, err := aws.NewProvider(ctx, "east", &aws.ProviderArgs{Region: pulumi.String("region-3")})
		if err != nil {
			return err
		}

		_, err = registryread.New(ctx, "east-argocd", registryread.Args{
			Provider: provider, ClusterName: "east", Region: "region-3", AccountID: "4343", RoleName: "east-argocd-registry",
			Description: "Argo CD reads the chart registry", Reader: registryread.ArgoCDRepoCreds,
			RegistryAccountID: "4242", RegistryRegion: "region-1",
		}, registryread.WithNames(func(c podidentity.Child) string { return c.Component + "/" + string(c.Kind) }))

		return err
	}, pulumi.WithMocks("project", "stack", m))
	if err != nil {
		t.Fatal(err)
	}

	if got := m.find(t, "aws:iam/policy:Policy", "east-argocd/policy")["policy"].StringValue(); got != livePull {
		t.Errorf("policy changed:\n%s\nwant:\n%s", got, livePull)
	}
}

func TestPolicyDocumentForOneRegistryEqualsPolicyDocument(t *testing.T) {
	t.Parallel()

	for discover, want := range map[bool]string{false: livePull, true: liveDiscover} {
		got, err := registryread.PolicyDocumentFor("", []registryread.Registry{{AccountID: "4242", Region: "region-1"}}, discover)
		if err != nil {
			t.Fatal(err)
		}

		if got != want {
			t.Errorf("discover=%v: one registry is not the single-registry document:\n%s", discover, got)
		}
	}
}

func TestPolicyDocumentMultiRegistry(t *testing.T) {
	t.Parallel()

	regs := []registryread.Registry{
		{AccountID: "4343", Region: "region-2"},
		{AccountID: "4242", Region: "region-1"},
		{AccountID: "4242", Region: "region-3"},
	}

	doc, err := registryread.PolicyDocumentFor("", regs, true)
	if err != nil {
		t.Fatal(err)
	}

	st := statements(t, doc)
	if len(st) != 2 {
		t.Fatalf("want the token statement and one registry statement, got %v", st)
	}

	if st[0]["Resource"] != "*" || st[0]["Action"] != registryread.ActionAuthToken {
		t.Errorf("token statement %v", st[0])
	}

	want := []any{arn("aws", "region-1", "4242"), arn("aws", "region-2", "4343"), arn("aws", "region-3", "4242")}
	if got, _ := st[1]["Resource"].([]any); !equal(got, want) {
		t.Errorf("resources %v, want %v (sorted)", st[1]["Resource"], want)
	}

	if st[1]["Sid"] != "ECRCrossAccountPullAndDiscover" {
		t.Errorf("sid %v", st[1]["Sid"])
	}

	// Registered in another order, the document is the same.
	again, err := registryread.PolicyDocumentFor("", []registryread.Registry{regs[2], regs[0], regs[1]}, true)
	if err != nil || again != doc {
		t.Errorf("the document depends on the order of the registries (err %v):\n%s\n%s", err, doc, again)
	}
}

func TestPolicyDocumentRepositoriesNarrowTheGrant(t *testing.T) {
	t.Parallel()

	doc, err := registryread.PolicyDocumentFor("", []registryread.Registry{
		{AccountID: "4242", Region: "region-1", Repositories: []string{"charts/*", "images/web", "charts/*"}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	want := []any{
		arnRepo("aws", "region-1", "4242", "charts/*"),
		arnRepo("aws", "region-1", "4242", "images/web"),
	}
	if got, _ := statements(t, doc)[1]["Resource"].([]any); !equal(got, want) {
		t.Errorf("resources %v, want %v (sorted, de-duplicated)", got, want)
	}

	one, err := registryread.PolicyDocumentFor("", []registryread.Registry{
		{AccountID: "4242", Region: "region-1", Repositories: []string{"charts/*"}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}

	if statements(t, one)[1]["Resource"] != arnRepo("aws", "region-1", "4242", "charts/*") {
		t.Errorf("one resource must stay a string: %s", one)
	}
}

func TestPolicyDocumentForRefusals(t *testing.T) {
	t.Parallel()

	good := registryread.Registry{AccountID: "4242", Region: "region-1"}
	oversize := registryread.Registry{AccountID: "4343", Region: "region-2"}

	for i := range 200 {
		oversize.Repositories = append(oversize.Repositories, fmt.Sprintf("team-%03d/service-%03d/charts", i, i))
	}

	for name, tc := range map[string]struct {
		regs []registryread.Registry
		want string
	}{
		"none":                  {nil, "at least one registry"},
		"empty region":          {[]registryread.Registry{good, {AccountID: "4343"}}, "region and account ID are required"},
		"empty account":         {[]registryread.Registry{{Region: "region-2"}}, "region and account ID are required"},
		"duplicate":             {[]registryread.Registry{good, {AccountID: "4343", Region: "region-2"}, good}, "listed twice"},
		"wildcard account":      {[]registryread.Registry{{AccountID: "*", Region: "region-1"}}, "must not contain"},
		"wildcard region":       {[]registryread.Registry{{AccountID: "4242", Region: "*"}}, "must not contain"},
		"empty repository":      {[]registryread.Registry{{AccountID: "4242", Region: "region-1", Repositories: []string{""}}}, "invalid repository pattern"},
		"blank repository":      {[]registryread.Registry{{AccountID: "4242", Region: "region-1", Repositories: []string{"a b"}}}, "invalid repository pattern"},
		"parent repository":     {[]registryread.Registry{{AccountID: "4242", Region: "region-1", Repositories: []string{"../x"}}}, "invalid repository pattern"},
		"rooted repository":     {[]registryread.Registry{{AccountID: "4242", Region: "region-1", Repositories: []string{"/x"}}}, "invalid repository pattern"},
		"over the policy limit": {[]registryread.Registry{oversize}, "managed policy limit"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := registryread.PolicyDocumentFor("", tc.regs, false); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %v, want it to contain %q", err, tc.want)
			}
		})
	}
}

func TestNewRefusesBothSingularAndPlural(t *testing.T) {
	t.Parallel()

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		_, err := registryread.New(ctx, "x", registryread.Args{
			Reader: registryread.ArgoCDRepoCreds, Description: "d", RoleName: "r",
			RegistryAccountID: "4242", RegistryRegion: "region-1",
			Registries: []registryread.Registry{{AccountID: "4343", Region: "region-2"}},
		})

		return err
	}, pulumi.WithMocks("project", "stack", &mocks{}))
	if err == nil || !strings.Contains(err.Error(), "not both") {
		t.Errorf("err = %v, want the refusal of both forms", err)
	}
}

func TestNewRegistersSeveralRegistries(t *testing.T) {
	t.Parallel()

	m := &mocks{}

	err := pulumi.RunErr(func(ctx *pulumi.Context) error {
		provider, err := aws.NewProvider(ctx, "east", &aws.ProviderArgs{Region: pulumi.String("region-3")})
		if err != nil {
			return err
		}

		_, err = registryread.New(ctx, "east-kargo", registryread.Args{
			Provider: provider, ClusterName: "east", Region: "region-3", AccountID: "4343", RoleName: "east-kargo-registry",
			Description: "Kargo reads the chart registries", Reader: registryread.KargoController,
			Registries: []registryread.Registry{
				{AccountID: "4242", Region: "region-1"},
				{AccountID: "4545", Region: "region-1"},
			},
		}, registryread.WithNames(func(c podidentity.Child) string { return c.Component + "/" + string(c.Kind) }))

		return err
	}, pulumi.WithMocks("project", "stack", m))
	if err != nil {
		t.Fatal(err)
	}

	st := statements(t, m.find(t, "aws:iam/policy:Policy", "east-kargo/policy")["policy"].StringValue())

	want := []any{arn("aws", "region-1", "4242"), arn("aws", "region-1", "4545")}
	if got, _ := st[1]["Resource"].([]any); !equal(got, want) {
		t.Errorf("resources %v, want %v", st[1]["Resource"], want)
	}
}

func TestNeverGrantsWriteActions(t *testing.T) {
	t.Parallel()

	regs := []registryread.Registry{{AccountID: "4242", Region: "region-1"}, {AccountID: "4343", Region: "region-2", Repositories: []string{"charts/*"}}}

	for _, discover := range []bool{false, true} {
		doc, err := registryread.PolicyDocumentFor("", regs, discover)
		if err != nil {
			t.Fatal(err)
		}

		readOnly := map[string]bool{
			registryread.ActionAuthToken: true, registryread.ActionBatchGetImage: true, registryread.ActionGetDownloadURL: true,
			registryread.ActionBatchCheck: true, registryread.ActionListImages: true, registryread.ActionDescribeImages: true,
		}

		for _, s := range statements(t, doc) {
			if s["Effect"] != "Allow" {
				t.Errorf("effect %v", s["Effect"])
			}

			actions, ok := s["Action"].([]any)
			if !ok {
				actions = []any{s["Action"]}
			}

			for _, a := range actions {
				if name, _ := a.(string); !readOnly[name] {
					t.Errorf("%v is not a read action", a)
				}
			}

			// Only the authorization token may be granted on "*".
			if s["Resource"] == "*" && s["Sid"] != "ECRAuthToken" {
				t.Errorf("statement %v grants on every resource", s)
			}
		}
	}
}

func equal(a, b []any) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}

	return true
}
