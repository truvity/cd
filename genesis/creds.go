package genesis

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
)

type (
	// Store keeps the repository credentials between geneses, so a repeated
	// genesis (a rebuilt cluster) needs no seed: a parameter store that
	// outlives the cluster. Get returns "" when the key is absent.
	Store interface {
		Get(ctx context.Context, key string) (string, error)
		Put(ctx context.Context, key, value string) error
	}

	// Seed is where the credentials come from the first time, when the Store
	// does not hold all of them yet (a password manager).
	Seed interface {
		Get(ctx context.Context, field string) (string, error)
	}

	// CredsKeys names each credential in a Store or a Seed.
	CredsKeys struct {
		AppID          string
		InstallationID string
		PrivateKey     string
	}
)

// ResolveRepoCreds reads the credentials from store. When any of the three
// is missing or unreadable it reads all three from seed and writes each into
// store (skipping a value the store already holds), so the next genesis
// needs no seed.
func ResolveRepoCreds(ctx context.Context, logger *slog.Logger, store Store, storeKeys CredsKeys, seed Seed, seedFields CredsKeys) (RepoCreds, error) {
	get := func(key string) string {
		v, err := store.Get(ctx, key)
		if err != nil || v == "" {
			logger.InfoContext(ctx, "stored credential not readable", slog.String("name", key))

			return ""
		}

		return v
	}

	creds := RepoCreds{
		AppID:          get(storeKeys.AppID),
		InstallationID: get(storeKeys.InstallationID),
		PrivateKey:     get(storeKeys.PrivateKey),
	}

	if creds.AppID != "" && creds.InstallationID != "" && creds.PrivateKey != "" {
		logger.InfoContext(ctx, "argocd genesis: credentials read from the store (no seed needed)")

		return creds, nil
	}

	logger.InfoContext(ctx, "argocd genesis: store incomplete, seeding")

	for _, f := range []struct {
		field string
		dst   *string
	}{
		{seedFields.AppID, &creds.AppID},
		{seedFields.InstallationID, &creds.InstallationID},
		{seedFields.PrivateKey, &creds.PrivateKey},
	} {
		v, err := seed.Get(ctx, f.field)
		if err != nil {
			return RepoCreds{}, fmt.Errorf("get %s from the seed: %w", f.field, err)
		}

		*f.dst = v
	}

	for _, p := range []struct{ key, value string }{
		{storeKeys.AppID, creds.AppID},
		{storeKeys.InstallationID, creds.InstallationID},
		{storeKeys.PrivateKey, creds.PrivateKey},
	} {
		if err := ensureStored(ctx, logger, store, p.key, p.value); err != nil {
			return RepoCreds{}, err
		}
	}

	logger.InfoContext(ctx, "argocd genesis: stored credentials ensured")

	return creds, nil
}

func ensureStored(ctx context.Context, logger *slog.Logger, store Store, key, value string) error {
	if v, err := store.Get(ctx, key); err == nil && v == value {
		logger.InfoContext(ctx, "stored credential already up-to-date, skipping", slog.String("name", key))

		return nil
	}

	if err := store.Put(ctx, key, value); err != nil {
		return fmt.Errorf("store %s: %w", key, err)
	}

	logger.InfoContext(ctx, "stored credential written", slog.String("name", key))

	return nil
}

// OnePassword is a Seed that reads the fields of one 1Password item with the
// `op` CLI (an operator's signed-in session). The credentials are FIELDS of
// the item, not file attachments.
type OnePassword struct {
	Vault   string
	Item    string
	Account string
	Logger  *slog.Logger
}

// Get reads one field of the item.
func (o OnePassword) Get(ctx context.Context, field string) (string, error) {
	//nolint:gosec // fixed binary, caller-derived args
	cmd := exec.CommandContext(ctx, "op", "item", "get", o.Item,
		"--vault", o.Vault,
		"--fields", field,
		"--account", o.Account,
	)

	var stdout bytes.Buffer

	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	if o.Logger != nil {
		o.Logger.InfoContext(ctx, "op: reading field",
			slog.String("vault", o.Vault),
			slog.String("item", o.Item),
			slog.String("field", field),
		)
	}

	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("op item get %s --fields %s: %w", o.Item, field, err)
	}

	return strings.TrimSpace(stdout.String()), nil
}
