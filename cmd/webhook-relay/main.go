// Command webhook-relay is a fan-in relay in front of Argo CD's /api/webhook:
// it accepts GitHub deliveries signed by any of several organisations' own
// secrets and forwards them, re-signed, to Argo CD, which knows only one. See
// the webhookrelay package and docs/webhook-relay.md.
//
//	webhook-relay -config /etc/webhook-relay/config.yaml
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/truvity/cd/webhookrelay"
)

func main() {
	configPath := flag.String("config", "", "path to the YAML/JSON configuration file")
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	if err := run(*configPath, log); err != nil {
		log.Error("webhook-relay", "error", err.Error())
		fmt.Fprintln(os.Stderr, "webhook-relay:", err)
		os.Exit(1)
	}
}

func run(configPath string, log *slog.Logger) error {
	if configPath == "" {
		return errors.New("-config is required")
	}

	cfg, err := webhookrelay.LoadConfig(configPath)
	if err != nil {
		return err
	}

	relay, err := webhookrelay.New(cfg, nil, log)
	if err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           relay.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errc := make(chan error, 1)

	go func() { errc <- srv.ListenAndServe() }()

	log.Info("listening", "addr", cfg.Listen, "senders", len(cfg.Senders))

	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}

	shutdown, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	return srv.Shutdown(shutdown)
}
