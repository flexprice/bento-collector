package store

import (
	"github.com/flexprice/bento-collector/collector/spec"

	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Secrets resolves secret references. FileSecrets reads one file per key,
// which is also the layout of a mounted Kubernetes Secret volume.
type Secrets interface {
	Get(ref string) (string, error)
	Has(ref string) bool
	Put(ref, value string) error
}

type FileSecrets struct {
	Dir string
}

func (s FileSecrets) path(ref string) (string, error) {
	if err := spec.ValidID(ref); err != nil {
		return "", err
	}
	return filepath.Join(s.Dir, ref), nil
}

func (s FileSecrets) Get(ref string) (string, error) {
	p, err := s.path(ref)
	if err != nil {
		return "", err
	}
	b, err := os.ReadFile(p)
	if errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("secret %q is not set", ref)
	}
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(b), "\r\n"), nil
}

func (s FileSecrets) Has(ref string) bool {
	p, err := s.path(ref)
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return err == nil
}

func (s FileSecrets) Put(ref, value string) error {
	p, err := s.path(ref)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(value), 0o600); err != nil {
		return fmt.Errorf("secret store is read-only here; create the secret %q in your secret manager: %w", ref, err)
	}
	return os.Rename(tmp, p)
}
