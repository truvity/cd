package genesis

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
)

type mapStore struct {
	m    map[string]string
	puts []string
}

func (s *mapStore) Get(_ context.Context, key string) (string, error) {
	v, ok := s.m[key]
	if !ok {
		return "", errors.New("absent")
	}

	return v, nil
}

func (s *mapStore) Put(_ context.Context, key, value string) error {
	s.m[key] = value
	s.puts = append(s.puts, key)

	return nil
}

type mapSeed struct {
	m     map[string]string
	reads int
}

func (s *mapSeed) Get(_ context.Context, field string) (string, error) {
	s.reads++

	v, ok := s.m[field]
	if !ok {
		return "", errors.New("no field " + field)
	}

	return v, nil
}

var (
	storeKeys  = CredsKeys{AppID: "/p/app-id", InstallationID: "/p/installation-id", PrivateKey: "/p/private-key"}
	seedFields = CredsKeys{AppID: "github-app-id", InstallationID: "github-installation-id", PrivateKey: "github-private-key"}
	quiet      = slog.New(slog.NewTextHandler(io.Discard, nil))
)

func TestResolveRepoCredsReadsAFullStoreWithoutTheSeed(t *testing.T) {
	t.Parallel()

	store := &mapStore{m: map[string]string{"/p/app-id": "1", "/p/installation-id": "2", "/p/private-key": "k"}}
	seed := &mapSeed{}

	got, err := ResolveRepoCreds(context.Background(), quiet, store, storeKeys, seed, seedFields)
	if err != nil {
		t.Fatal(err)
	}

	if got != (RepoCreds{AppID: "1", InstallationID: "2", PrivateKey: "k"}) || seed.reads != 0 || len(store.puts) != 0 {
		t.Errorf("got %+v, seed reads %d, puts %v; want the stored creds, no seed read, no write", got, seed.reads, store.puts)
	}
}

func TestResolveRepoCredsSeedsAnIncompleteStoreAndWritesOnlyWhatDiffers(t *testing.T) {
	t.Parallel()

	store := &mapStore{m: map[string]string{"/p/app-id": "1"}}
	seed := &mapSeed{m: map[string]string{"github-app-id": "1", "github-installation-id": "2", "github-private-key": "k"}}

	got, err := ResolveRepoCreds(context.Background(), quiet, store, storeKeys, seed, seedFields)
	if err != nil {
		t.Fatal(err)
	}

	if got != (RepoCreds{AppID: "1", InstallationID: "2", PrivateKey: "k"}) {
		t.Errorf("got %+v", got)
	}

	if want := []string{"/p/installation-id", "/p/private-key"}; len(store.puts) != 2 || store.puts[0] != want[0] || store.puts[1] != want[1] {
		t.Errorf("puts = %v, want %v (the app id is already stored)", store.puts, want)
	}
}

func TestResolveRepoCredsFailsOnAMissingSeedField(t *testing.T) {
	t.Parallel()

	store := &mapStore{m: map[string]string{}}
	seed := &mapSeed{m: map[string]string{"github-app-id": "1"}}

	if _, err := ResolveRepoCreds(context.Background(), quiet, store, storeKeys, seed, seedFields); err == nil {
		t.Fatal("want an error when the seed lacks a field")
	}

	if len(store.puts) != 0 {
		t.Errorf("puts = %v; nothing may be stored from a partial seed", store.puts)
	}
}
