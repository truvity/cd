package delivery

// Suffixes are cd-delivery's `platform.applicationSuffixes`: the suffix of
// each ring's Application name after the product.
type Suffixes struct {
	Infra string
	App   string
	E2E   string
}

// DefaultSuffixes are cd-delivery's defaults: <product>-infra, <product>,
// <product>-e2e.
var DefaultSuffixes = Suffixes{Infra: "-infra", App: "", E2E: "-e2e"}

// Renames maps each ring's Application name under from to its name under to,
// for one product.
func Renames(product string, from, to Suffixes) map[string]string {
	return map[string]string{
		product + from.Infra: product + to.Infra,
		product + from.App:   product + to.App,
		product + from.E2E:   product + to.E2E,
	}
}
