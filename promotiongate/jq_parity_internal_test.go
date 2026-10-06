package promotiongate

// Differential proof of the jq re-implementation: the jq programs the shell
// scripts ran, run by a real jq over the same inputs, must print what the Go
// port prints and fail where it fails. The promoted-version program is read
// from the chart's own script, so the two cannot drift while both exist.
// Skipped where no jq is installed.

import (
	"bytes"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

func requireJQ(t *testing.T) string {
	t.Helper()

	jq, err := exec.LookPath("jq")
	if err != nil {
		t.Skip("no jq on PATH")
	}

	return jq
}

// runJQ is `printf '%s' "$body" | jq -r <args> <program>`: its stdout and
// whether it exited 0.
func runJQ(t *testing.T, jq, body, program string, args ...string) (string, bool) {
	t.Helper()

	cmd := exec.CommandContext(t.Context(), jq, append(append([]string{"-r"}, args...), program)...)
	cmd.Stdin = strings.NewReader(body)

	var out bytes.Buffer

	cmd.Stdout = &out
	cmd.Stderr = io.Discard

	err := cmd.Run()

	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		t.Fatal(err)
	}

	return out.String(), err == nil
}

var parityBodies = []string{
	``,
	`{}`,
	`null`,
	`"x"`,
	`not json`,
	`{"data":"x"}`,
	`{"data":null}`,
	`{"data":{"result":null}}`,
	`{"data":{"result":{}}}`,
	`{"data":{"result":[]}}`,
	`{"data":{"result":[{}]}}`,
	`{"data":{"result":[{"value":[0,"3"]}]}}`,
	`{"data":{"result":[{"value":[0,"0"]}]}}`,
	`{"data":{"result":[{"value":[0,""]}]}}`,
	`{"data":{"result":[{"value":[0,null]}]}}`,
	`{"data":{"result":[{"value":[0,false]}]}}`,
	`{"data":{"result":[{"value":[0,1.5]}]}}`,
	`{"data":{"result":[{"value":[0,{"a":1}]}]}}`,
	`{"data":{"result":[{"value":"x"}]}}`,
	`{"data":{"result":["x"]}}`,
	`{"data":{"result":[{"value":[0,"1"]}]}} {"data":{"result":[{"value":[0,"2"]}]}}`,
	`{"data":{"result":[{"value":[0,"1"]}]}} "x" {"data":{"result":[{"value":[0,"2"]}]}}`,
	`{"data":{"result":[{"metric":{"alertname":"A","job_name":"j"}},{"metric":{"alertname":"B","pod":"p"}},{"metric":{}},{"metric":{"job_name":""}}]}}`,
	`{"data":{"result":[{"metric":{"alertname":"A"}},{"metric":{"alertname":7}},{"metric":{"alertname":"C"}}]}}`,
	`{"data":{"result":[{"metric":null}]}}`,
	`{"data":{"result":[{"metric":{"alertname":"A","job_name":false,"pod":"p"}}]}}`,
	`{"access_token":"t","expires_in":60}`,
	`{"access_token":"","expires_in":null}`,
	`{"access_token":false}`,
	`{"access_token":{"x":1},"expires_in":"60"}`,
}

func TestJQParityMetricsGate(t *testing.T) {
	t.Parallel()

	jq := requireJQ(t)

	programs := map[string]func(v any) ([]string, error){
		`.data.result[0].value[1] // empty`: func(v any) ([]string, error) {
			x, err := path(v, "data", "result")
			if err == nil {
				x, err = index(x, 0)
			}

			if err == nil {
				x, err = field(x, "value")
			}

			if err == nil {
				x, err = index(x, 1)
			}

			if err != nil || !truthy(x) {
				return nil, err
			}

			return []string{rawOut(x)}, nil
		},
		`.data.result[] | "  alertname=" + (.metric.alertname // "unknown") + (if .metric.job_name then " job_name=" + .metric.job_name elif .metric.pod then " pod=" + .metric.pod else "" end)`: alertLines,
		`.access_token // empty`: func(v any) ([]string, error) {
			x, err := field(v, "access_token")
			if err != nil || !truthy(x) {
				return nil, err
			}

			return []string{rawOut(x)}, nil
		},
		`.expires_in // 300`: func(v any) ([]string, error) {
			x, err := field(v, "expires_in")
			if err != nil {
				return nil, err
			}

			if !truthy(x) {
				return []string{"300"}, nil
			}

			return []string{rawOut(x)}, nil
		},
	}

	for program, f := range programs {
		for _, body := range parityBodies {
			wantOut, wantOK := runJQ(t, jq, body, program)
			gotOut, gotOK := jqRaw(body, io.Discard, f)

			if gotOut != wantOut || gotOK != wantOK {
				t.Errorf("%s over %s:\n jq: %q ok=%v\n go: %q ok=%v", program, body, wantOut, wantOK, gotOut, gotOK)
			}
		}
	}
}

func TestJQParityPromotedVersion(t *testing.T) {
	t.Parallel()

	jq := requireJQ(t)

	script, err := os.ReadFile(filepath.Join("..", "charts", "cd-pipeline", "files", "verify-promoted-version.sh"))
	if err != nil {
		t.Fatal(err)
	}

	m := regexp.MustCompile(`(?s)--arg exp "\$EXPECTED" '(.*?)'\n`).FindSubmatch(script)
	if m == nil {
		t.Fatal("the judge program is not in the script any more")
	}

	program := string(m[1])

	const repo = "oci://registry.example.com/charts/shop"

	src := func(r, rev string) string { return `{"repoURL":"` + r + `","targetRevision":"` + rev + `"}` }
	apps := []string{
		`{}`, `null`, `"x"`, `not json`,
		`{"spec":"x"}`,
		`{"spec":{"source":null}}`,
		`{"spec":{"sources":[]}}`,
		`{"spec":{"sources":{}}}`,
		`{"spec":{"sources":"x"}}`,
		`{"spec":{"sources":[null]}}`,
		`{"spec":{"sources":[false]}}`,
		`{"spec":{"source":{"chart":"shop"}}}`,
		`{"spec":{"source":{"repoURL":1}}}`,
		`{"spec":{"source":` + src(repo, "v1") + `}}`,
		`{"spec":{"source":` + src(repo, "v2") + `}}`,
		`{"spec":{"source":{"repoURL":"` + repo + `","targetRevision":2}}}`,
		`{"spec":{"source":{"repoURL":"` + repo + `","targetRevision":true}}}`,
		`{"spec":{"source":{"repoURL":"` + repo + `","targetRevision":{"a":1}}}}`,
		`{"spec":{"source":{"repoURL":"` + repo + `"}}}`,
		`{"spec":{"source":{"repoURL":"registry.example.com/charts","chart":"shop","targetRevision":"v2"}}}`,
		`{"spec":{"source":{"repoURL":"registry.example.com/charts","chart":7,"targetRevision":"v2"}}}`,
		`{"spec":{"source":{"repoURL":"registry.example.com/charts","chart":"","targetRevision":"v2"}}}`,
		`{"spec":{"source":{"repoURL":"registry.example.com/charts","chart":false,"targetRevision":"v2"}}}`,
		`{"spec":{"sources":[` + src(repo+"/", "v2") + `,` + src(repo, "v1") + `]}}`,
		`{"spec":{"source":` + src(repo, "v2") + `},"status":"x"}`,
		`{"spec":{"source":` + src(repo, "v2") + `},"status":{"sync":{"comparedTo":{"source":` + src(repo, "v1") + `}}}}`,
		`{"spec":{"source":` + src(repo, "v2") + `},"status":{"sync":{"comparedTo":{"sources":[` + src(repo, "v2") + `,` + src(repo, "v1") + `]}}}}`,
		`{"spec":{"source":` + src(repo, "v2") + `},"status":{"sync":{"comparedTo":{"source":` + src(repo, "v2") + `}},"operationState":{"phase":"Running"}}}`,
		`{"spec":{"source":` + src(repo, "v2") + `},"status":{"sync":{"comparedTo":{"source":` + src(repo, "v2") + `}},"operationState":"x"}}`,
		`{"spec":{"source":` + src(repo, "v2") + `},"status":{"sync":{"status":"OutOfSync","comparedTo":{"source":` + src(repo, "v2") + `}}}}`,
		`{"spec":{"source":` + src(repo, "v2") + `},"status":{"sync":{"status":3,"comparedTo":{"source":` + src(repo, "v2") + `}}}}`,
		`{"spec":{"source":` + src(repo, "v2") + `},"status":{"sync":{"status":"Synced","comparedTo":{"source":` + src(repo, "v2") + `}}}}`,
		`{"spec":{"source":` + src(repo, "v2") + `},"status":{"sync":{"status":"Synced","comparedTo":{"source":` + src(repo, "v2") + `}},"health":{"status":"Degraded"}}}`,
		`{"spec":{"source":` + src(repo, "v2") + `},"status":{"sync":{"status":"Synced","comparedTo":{"source":` + src(repo, "v2") + `}},"health":{"status":"Healthy"}}}`,
		`{"spec":{"source":` + src(repo, "v2") + `}} {"spec":{"source":` + src(repo, "v1") + `}}`,
	}

	for _, args := range [][3]string{{repo, "", "v2"}, {"registry.example.com/charts", "shop", "v2"}, {repo + "/", "", "v1"}} {
		for _, app := range apps {
			wantOut, wantOK := runJQ(t, jq, app, program, "--arg", "repo", args[0], "--arg", "chart", args[1], "--arg", "exp", args[2])
			gotOut, gotOK := jqRaw(app, io.Discard, func(v any) ([]string, error) {
				s, err := judge(v, args[0], args[1], args[2])
				if err != nil {
					return nil, err
				}

				return []string{s}, nil
			})

			if gotOut != wantOut || gotOK != wantOK {
				t.Errorf("judge %v over %s:\n jq: %q ok=%v\n go: %q ok=%v", args, app, wantOut, wantOK, gotOut, gotOK)
			}
		}
	}
}

// The awk the scripts compared and rounded with (busybox's, as in the curl
// image they ran in), against the Go port. Skipped where no busybox is
// installed.
func TestAwkParity(t *testing.T) {
	t.Parallel()

	bb, err := exec.LookPath("busybox")
	if err != nil {
		t.Skip("no busybox on PATH")
	}

	awk := func(program string, vars ...string) (string, bool) {
		args := []string{"awk"}
		for i := 0; i < len(vars); i += 2 {
			args = append(args, "-v", vars[i]+"="+vars[i+1])
		}

		cmd := exec.CommandContext(t.Context(), bb, append(args, program)...)

		var out bytes.Buffer

		cmd.Stdout = &out

		err := cmd.Run()

		var ee *exec.ExitError
		if err != nil && !errors.As(err, &ee) {
			t.Fatal(err)
		}

		return out.String(), err == nil
	}

	values := []string{"0", "1", "2", "3", "0.6667", "1e1", "10", "9.98", "-0.7", " 3 ", "", "abc", "3abc", "NaN", "+Inf", "-Inf", "0.05", "0.0500", "0.1000"}
	for _, a := range values {
		for _, b := range values {
			if _, ge := awk(`BEGIN{exit !(a>=b)}`, "a", a, "b", b); ge != numGE(a, b) {
				t.Errorf("num_ge %q %q: awk %v, go %v", a, b, ge, numGE(a, b))
			}

			if _, gt := awk(`BEGIN{exit !(a>b)}`, "a", a, "b", b); gt != numGT(a, b) {
				t.Errorf("num_gt %q %q: awk %v, go %v", a, b, gt, numGT(a, b))
			}
		}

		if a == "" || a == "NaN" || a == "+Inf" || a == "-Inf" {
			continue // ${1:-0} and busybox's INT_MIN, covered by toInt's own contract
		}

		if out, _ := awk(`BEGIN{printf "%d", a + 0.5}`, "a", a); out != strconv.FormatInt(toInt(a), 10) {
			t.Errorf("to_int %q: awk %s, go %d", a, out, toInt(a))
		}
	}

	for _, ft := range [][2]int64{{0, 0}, {0, 10}, {1, 3}, {2, 3}, {10, 100}, {1, 30}, {7, 7}} {
		out, _ := awk(`BEGIN{ if (t == 0) { print 0 } else { printf "%.4f", f/t } }`, "f", strconv.FormatInt(ft[0], 10), "t", strconv.FormatInt(ft[1], 10))
		if strings.TrimSuffix(out, "\n") != ratio4(ft[0], ft[1]) {
			t.Errorf("ratio %v: awk %q, go %q", ft, out, ratio4(ft[0], ft[1]))
		}
	}
}
