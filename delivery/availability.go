package delivery

import (
	"errors"
	"fmt"
)

// A cluster's availability (delivery interface 14): what the application
// ring sizes replicas and disruption budgets from.
const (
	// AvailabilitySingle is one of everything.
	AvailabilitySingle = "single"
	// AvailabilityHigh is two or more, spread.
	AvailabilityHigh = "high"
	// AvailabilityNone is a cluster that runs no products.
	AvailabilityNone = "none"
)

// ValidAvailability refuses an availability that is not one of the three.
// Empty passes: a layered declaration may leave it to another layer, and
// Platform.Complete refuses it once resolved.
func ValidAvailability(availability string) error {
	switch availability {
	case "", AvailabilitySingle, AvailabilityHigh, AvailabilityNone:
		return nil
	default:
		return fmt.Errorf("availability must be single, high or none, got %q", availability)
	}
}

// Platform is a cluster's resolved delivery facts.
type Platform struct {
	Availability      string
	PostgresInstances int
	PostgresStorage   string
}

// Complete refuses a resolved delivery block that leaves a fact unset: every
// product chart reading interface 14 needs the availability, and a database
// needs its size.
func (p Platform) Complete() error {
	if p.Availability == "" {
		return errors.New("availability is required (single, high or none)")
	}

	if p.PostgresInstances < 1 {
		return errors.New("postgres instances must be at least 1")
	}

	if p.PostgresStorage == "" {
		return errors.New("postgres storage is required")
	}

	return nil
}
