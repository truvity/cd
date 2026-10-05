package chartgate

import (
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	// maxErrLines caps how much of one Helm error is echoed.
	maxErrLines = 25
)

// Report writes the outcome and returns the number of failures. Skips are
// printed one per line, never summarized away.
func Report(w io.Writer, results []Result, elapsed time.Duration) int {
	var ok, fail, skip int

	for _, r := range results {
		switch r.Status {
		case StatusOK:
			ok++
		case StatusFail:
			fail++
		case StatusSkip:
			skip++
		}
	}

	if skip > 0 {
		sayf(w, "\nNot checked (%d):\n", skip)

		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for _, r := range results {
			if r.Status == StatusSkip {
				sayf(tw, "  not checked: %s\t%s\t%s\t%s\n", r.Detail, r.Cluster, r.App, r.Chart)
			}
		}

		_ = tw.Flush()
	}

	if fail > 0 {
		sayf(w, "\nFAILURES (%d):\n", fail)

		for _, r := range results {
			if r.Status != StatusFail {
				continue
			}

			sayf(w, "\n  cluster      %s\n  application  %s\n  chart        %s\n  error:\n", r.Cluster, r.App, r.Chart)

			lines := strings.Split(r.Detail, "\n")
			if len(lines) > maxErrLines {
				lines = append(lines[:maxErrLines], fmt.Sprintf("... (%d more lines)", len(lines)-maxErrLines))
			}

			for _, l := range lines {
				sayf(w, "    %s\n", l)
			}
		}

		sayf(w, "\n  %-8s %-40s %s\n", "CLUSTER", "APPLICATION", "CHART")

		for _, r := range results {
			if r.Status == StatusFail {
				sayf(w, "  %-8s %-40s %s\n", r.Cluster, r.App, r.Chart)
			}
		}
	}

	sayf(w, "\nchart-gate: %d rendered ok, %d FAILED, %d not checked, in %s\n", ok, fail, skip, elapsed.Round(100*time.Millisecond))

	return fail
}

// sayf writes to a report sink. A failed write to stdout has nowhere better
// to be reported, so it is dropped here once rather than at every call.
func sayf(w io.Writer, format string, args ...any) {
	_, _ = fmt.Fprintf(w, format, args...)
}
