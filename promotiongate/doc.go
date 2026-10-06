// Package promotiongate holds the two Kargo verification checks of a
// promotion pipeline, as Go instead of shell scripts that need curl and jq in
// their image:
//
//   - [MetricsGate]: after a promotion, wait until the promoted version is the
//     only one serving, then bake on a sample count of prober journeys read
//     from a Prometheus-compatible store (failure ratio, container restarts,
//     firing alerts, the version's own end-to-end Job).
//   - [PromotedVersion]: hold until every target Argo CD Application renders
//     the promoted chart version, has been compared at it, and is Synced,
//     Healthy and not mid-operation.
//
// Both are a line-for-line port of the shell scripts they replace: the same
// environment, the same queries, the same decisions, the same exit codes
// ([ExitCode]). The jq expressions the scripts ran are re-implemented with
// jq's own semantics (a value that is null or false is absent; an index into
// the wrong type is an error that ends the run with jq's exit status 5), and
// awk's float comparisons and rounding are kept, so an edge the script
// decided one way is decided the same way here.
//
// The clock, the sleep and every HTTP client are injectable, so a test never
// waits for real and never needs a cluster.
package promotiongate
