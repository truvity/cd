package delivery

// DefaultProberComponents are the components the reference end-to-end
// chart's prober calls (truvity/policy's examples/url-shortener e2e journey):
// a property of that chart, not of any one product.
var DefaultProberComponents = []string{"urls", "redirect"}

// Peer is one caller a component accepts: its namespace and ServiceAccount.
type Peer struct {
	Namespace      string
	ServiceAccount string
}

// ProberServiceAccount is the ServiceAccount cd-delivery gives a product's
// end-to-end prober (`prober.serviceAccount.name`).
func ProberServiceAccount(product string) string {
	return product + "-e2e-prober"
}

// ProberGranted reports whether every component the prober calls accepts the
// prober as a peer (cd-delivery's `e2e.proberGranted`): only then may the
// prober present its workload identity, or the callee refuses it at the
// handshake and the prober goes red for a reason that is not the product's.
// peersOf returns a component's accepted callers (nil: none declared).
func ProberGranted(product string, components []string, peersOf func(component string) []Peer) bool {
	want := Peer{Namespace: product, ServiceAccount: ProberServiceAccount(product)}

	for _, component := range components {
		found := false

		for _, p := range peersOf(component) {
			if p == want {
				found = true

				break
			}
		}

		if !found {
			return false
		}
	}

	return true
}
