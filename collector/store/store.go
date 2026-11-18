// Package store keeps versioned collector specs (files, optional git) and the
// deployment's connections file.
package store

import (
	"github.com/flexprice/bento-collector/collector/spec"

	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrNotFound = errors.New("not found")

// Store keeps versioned collector specs.
type Store interface {
	ListCollectors() ([]spec.Collector, error)
	GetCollector(id string, version int) (spec.Collector, error)
	ListVersions(id string) ([]spec.Collector, error)
	SaveCollector(c spec.Collector) (spec.Collector, error)
}

// FileStore writes one JSON file per collector version:
//
//	collectors/<id>/v0001.json
//
// With Git set, every save is committed so the directory doubles as an audit log.
type FileStore struct {
	Dir string
	Git bool
	Now func() time.Time
	mu  sync.Mutex
}

func NewFileStore(dir string, git bool) (*FileStore, error) {
	if err := os.MkdirAll(filepath.Join(dir, "collectors"), 0o755); err != nil {
		return nil, err
	}
	s := &FileStore{Dir: dir, Git: git, Now: time.Now}
	if git {
		if _, err := os.Stat(filepath.Join(dir, ".git")); errors.Is(err, os.ErrNotExist) {
			if err := s.git("init", "-q"); err != nil {
				return nil, err
			}
		}
	}
	return s, nil
}

func (s *FileStore) ListCollectors() ([]spec.Collector, error) {
	dirs, err := os.ReadDir(filepath.Join(s.Dir, "collectors"))
	if err != nil {
		return nil, err
	}
	var out []spec.Collector
	for _, d := range dirs {
		if !d.IsDir() || spec.ValidID(d.Name()) != nil {
			continue
		}
		c, err := s.GetCollector(d.Name(), 0)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// GetCollector returns the given version, or the latest when version is 0.
func (s *FileStore) GetCollector(id string, version int) (spec.Collector, error) {
	var c spec.Collector
	if err := spec.ValidID(id); err != nil {
		return c, err
	}
	if version == 0 {
		vs, err := s.versions(id)
		if err != nil {
			return c, err
		}
		if len(vs) == 0 {
			return c, fmt.Errorf("collector %s: %w", id, ErrNotFound)
		}
		version = vs[len(vs)-1]
	}
	err := readJSON(s.versionPath(id, version), &c)
	return c, err
}

func (s *FileStore) ListVersions(id string) ([]spec.Collector, error) {
	vs, err := s.versions(id)
	if err != nil {
		return nil, err
	}
	out := make([]spec.Collector, 0, len(vs))
	for i := len(vs) - 1; i >= 0; i-- {
		c, err := s.GetCollector(id, vs[i])
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, nil
}

// SaveCollector validates and writes a new version; versions are never overwritten.
func (s *FileStore) SaveCollector(c spec.Collector) (spec.Collector, error) {
	c.Normalize()
	if err := c.Validate(); err != nil {
		return c, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	vs, err := s.versions(c.ID)
	if err != nil {
		return c, err
	}
	c.Version = 1
	if len(vs) > 0 {
		c.Version = vs[len(vs)-1] + 1
	}
	c.SavedAt = s.Now().UTC()
	if err := os.MkdirAll(filepath.Join(s.Dir, "collectors", c.ID), 0o755); err != nil {
		return c, err
	}
	path := s.versionPath(c.ID, c.Version)
	if err := writeJSON(path, c); err != nil {
		return c, err
	}
	return c, s.commit(path, fmt.Sprintf("collector %s v%d", c.ID, c.Version))
}

func (s *FileStore) versions(id string) ([]int, error) {
	if err := spec.ValidID(id); err != nil {
		return nil, err
	}
	files, err := filepath.Glob(filepath.Join(s.Dir, "collectors", id, "v*.json"))
	if err != nil {
		return nil, err
	}
	var vs []int
	for _, f := range files {
		n, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(filepath.Base(f), "v"), ".json"))
		if err == nil && n > 0 {
			vs = append(vs, n)
		}
	}
	sort.Ints(vs)
	return vs, nil
}

func (s *FileStore) versionPath(id string, v int) string {
	return filepath.Join(s.Dir, "collectors", id, fmt.Sprintf("v%04d.json", v))
}

func (s *FileStore) commit(path, msg string) error {
	if !s.Git {
		return nil
	}
	rel, err := filepath.Rel(s.Dir, path)
	if err != nil {
		return err
	}
	if err := s.git("add", "--", rel); err != nil {
		return err
	}
	return s.git("-c", "user.name=flexprice-collector", "-c", "user.email=collector@flexprice.local",
		"commit", "-q", "-m", msg, "--", rel)
}

func (s *FileStore) git(args ...string) error {
	cmd := exec.Command("git", args...)
	cmd.Dir = s.Dir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git %s: %v: %s", args[0], err, strings.TrimSpace(string(out)))
	}
	return nil
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%s: %w", filepath.Base(path), ErrNotFound)
	}
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
