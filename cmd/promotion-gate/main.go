// Command promotion-gate runs one Kargo verification check of a promotion
// pipeline, configured by its environment, and exits with its verdict: 0
// passed, 1 failed (2 for a missing or malformed input, 5 for a response that
// could not be read, as the shell scripts it replaces did).
//
//	promotion-gate metrics-gate       the bake-window metrics gate
//	promotion-gate promoted-version   the promoted-version check
//
// See the promotiongate package for the checks and their environment.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/truvity/cd/promotiongate"
)

const usage = `usage: promotion-gate metrics-gate|promoted-version

Configured by the environment; see the promotiongate package.`

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(promotiongate.ExitUsage)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	err := run(ctx, os.Args[1])

	stop()

	if err != nil {
		var ee *promotiongate.ExitError
		if !errors.As(err, &ee) || ee.Code == promotiongate.ExitUsage {
			// A verdict was logged by the check; anything else is said here.
			fmt.Fprintf(os.Stderr, "promotion-gate %s: %v\n", os.Args[1], err)
		}
	}

	os.Exit(promotiongate.ExitCode(err))
}

func run(ctx context.Context, cmd string) error {
	switch cmd {
	case "metrics-gate":
		cfg, err := promotiongate.MetricsConfigFromEnv(os.Getenv)
		if err != nil {
			return err
		}

		return promotiongate.MetricsGate(ctx, cfg)
	case "promoted-version":
		cfg, err := promotiongate.PromotedVersionConfigFromEnv(os.LookupEnv)
		if err != nil {
			return err
		}

		return promotiongate.PromotedVersion(ctx, cfg)
	default:
		return &promotiongate.ExitError{Code: promotiongate.ExitUsage, Err: fmt.Errorf("unknown check %q\n%s", cmd, usage)}
	}
}
