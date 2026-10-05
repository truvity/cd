package chartgate

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func self(u string) bool { return strings.Contains(u, "example/repo") }

func TestClassify(t *testing.T) {
	t.Parallel()

	const ecr = "1234567.dkr.ecr.eu-central-1.amazonaws.com"

	tests := []struct {
		name      string
		src       Source
		class     Classification
		chart     string
		needsAuth bool
	}{
		{"ref only", Source{RepoURL: "https://github.com/example/repo.git", Ref: "values"}, ClassRefOnly, "", false},
		{"local stack", Source{RepoURL: "https://github.com/example/repo.git", Path: "stacks/iam"}, ClassLocal, "", false},
		{"other git", Source{RepoURL: "https://github.com/keycloak/keycloak-k8s-resources", Path: "kubernetes", TargetRevision: "26.8.0"}, ClassGit, "", false},
		{"oci url is the chart", Source{RepoURL: "oci://ghcr.io/truvity/charts/gateway-fleet", Path: ".", TargetRevision: "1.7.0"}, ClassRemote, "oci://ghcr.io/truvity/charts/gateway-fleet@1.7.0", false},
		{"schemeless oci plus chart", Source{RepoURL: "public.ecr.aws/aws-controllers-k8s", Chart: "s3-chart", TargetRevision: "1.12.2"}, ClassRemote, "oci://public.ecr.aws/aws-controllers-k8s/s3-chart@1.12.2", false},
		{"http repo", Source{RepoURL: "https://charts.jetstack.io", Chart: "cert-manager", TargetRevision: "v1.21.2"}, ClassRemote, "https://charts.jetstack.io cert-manager@v1.21.2", false},
		{"private ecr", Source{RepoURL: "oci://" + ecr + "/dms/charts/dms", Path: ".", TargetRevision: "0.39.13"}, ClassRemote, "oci://" + ecr + "/dms/charts/dms@0.39.13", true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			class, chart := Classify(tc.src, self)
			if class != tc.class {
				t.Fatalf("class = %v, want %v", class, tc.class)
			}

			if class != ClassRemote {
				return
			}

			if chart.String() != tc.chart {
				t.Errorf("chart = %q, want %q", chart.String(), tc.chart)
			}

			if _, auth := chart.NeedsAuth(); auth != tc.needsAuth {
				t.Errorf("NeedsAuth = %v, want %v", auth, tc.needsAuth)
			}
		})
	}
}

func TestParseApplications(t *testing.T) {
	t.Parallel()

	apps, err := ParseApplications([]byte(`
apiVersion: v1
kind: Secret
metadata: {name: s}
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: a}
spec:
  destination: {namespace: ns}
  sources:
    - repoURL: oci://ghcr.io/x/y
      path: .
      targetRevision: 1.0.0
      helm:
        releaseName: rel
        valuesObject: {k: v}
---
apiVersion: argoproj.io/v1alpha1
kind: Application
metadata: {name: b}
spec:
  source: {repoURL: https://r.example, chart: c, targetRevision: "2"}
`))
	if err != nil {
		t.Fatal(err)
	}

	if len(apps) != 2 {
		t.Fatalf("got %d Applications, want 2 (the Secret is not one)", len(apps))
	}

	if a := apps[0]; a.Name != "a" || a.Namespace != "ns" || a.Release(a.Sources[0]) != "rel" || a.Sources[0].Helm.ValuesObject["k"] != "v" {
		t.Errorf("first Application parsed wrong: %+v", a)
	}

	if b := apps[1]; len(b.Sources) != 1 || b.Release(b.Sources[0]) != "b" {
		t.Errorf("single `source` not folded into Sources, or release not defaulted to the name: %+v", b)
	}
}

func TestReportCountsFailuresAndNamesSkips(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	fails := Report(&out, []Result{
		{Cluster: "devel", App: "a", Chart: "c@1", Status: StatusOK},
		{Cluster: "devel", App: "b", Chart: "d@2", Status: StatusFail, Detail: "values don't meet the specification of the schema(s)"},
		{Cluster: "prod", App: "e", Chart: "f@3", Status: StatusSkip, Detail: "needs auth: private ECR registry x"},
	}, time.Second)

	if fails != 1 {
		t.Fatalf("failures = %d, want 1", fails)
	}

	for _, want := range []string{"not checked: needs auth: private ECR registry x", "FAILURES (1)", "values don't meet the specification", "1 FAILED, 1 not checked"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report lacks %q:\n%s", want, out.String())
		}
	}
}
