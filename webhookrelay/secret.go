package webhookrelay

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"sync"
)

// readSecret returns the file's content without trailing newlines. An empty
// secret is refused: an HMAC over an empty key verifies anything.
func readSecret(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	b = bytes.TrimRight(b, "\r\n")
	if len(b) == 0 {
		return nil, errors.New("secret file is empty")
	}

	return b, nil
}

// secretFile serves the current content of a mounted secret. Each call stats
// the file and re-reads it when its identity, size or modification time moved,
// so a rotation (a Kubernetes Secret volume swaps the file atomically) is
// picked up by the very next delivery with no watcher to lag behind. If a
// re-read fails the last good value keeps serving.
type secretFile struct {
	path string

	mu    sync.Mutex
	info  os.FileInfo
	value []byte
}

func newSecretFile(path string) (*secretFile, error) {
	s := &secretFile{path: path}
	if _, err := s.get(); err != nil {
		return nil, err
	}

	return s, nil
}

func (s *secretFile) get() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	info, err := os.Stat(s.path)
	if err != nil {
		return s.fallback(err)
	}

	if s.value != nil && os.SameFile(s.info, info) && s.info.ModTime().Equal(info.ModTime()) && s.info.Size() == info.Size() {
		return s.value, nil
	}

	v, err := readSecret(s.path)
	if err != nil {
		return s.fallback(err)
	}

	s.info, s.value = info, v

	return v, nil
}

func (s *secretFile) fallback(err error) ([]byte, error) {
	err = fmt.Errorf("secret file %s: %w", s.path, err)
	if s.value != nil {
		return s.value, err
	}

	return nil, err
}

// ready reports whether the file is readable right now.
func (s *secretFile) ready() error {
	_, err := s.get()

	return err
}
