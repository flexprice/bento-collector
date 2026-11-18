package collector

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Connections provisioned by the deployment (a mounted file) are read-only in
// the UI; their secrets come from environment variables, never the secret store.

const envRefPrefix = "env:"

var (
	ErrManaged = errors.New("this connection is managed by the deployment; change it in the connections file")
	ErrLocked  = errors.New("connections are managed by the deployment (COLLECTOR_LOCK_CONNECTIONS); add them to the connections file")

	envVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
	envVarRef  = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)
)

type connectionSpec struct {
	ID        string            `yaml:"id"`
	Name      string            `yaml:"name"`
	Type      string            `yaml:"type"`
	URL       string            `yaml:"url"`
	AuthType  string            `yaml:"auth_type"`
	Username  string            `yaml:"username"`
	SecretEnv string            `yaml:"secret_env"`
	Headers   map[string]string `yaml:"headers"`
}

// LoadConnections reads a connections file, expanding ${VAR} from the environment.
// Every referenced variable, including each secret_env, must be set.
func LoadConnections(path string, getenv func(string) string) ([]Connection, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Connections []connectionSpec `yaml:"connections"`
	}
	dec := yaml.NewDecoder(strings.NewReader(string(raw)))
	dec.KnownFields(true)
	if err := dec.Decode(&file); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	missing := map[string]bool{}
	expand := func(s string) string {
		return envVarRef.ReplaceAllStringFunc(s, func(m string) string {
			name := envVarRef.FindStringSubmatch(m)[1]
			v := getenv(name)
			if v == "" {
				missing[name] = true
			}
			return v
		})
	}
	seen := map[string]bool{}
	out := make([]Connection, 0, len(file.Connections))
	for _, s := range file.Connections {
		c := Connection{ID: s.ID, Name: s.Name, Type: s.Type, URL: expand(s.URL), AuthType: s.AuthType, Username: expand(s.Username), Managed: true}
		if c.AuthType == "" && c.Type == ConnectionFlexprice {
			c.AuthType = "api_key"
		}
		if len(s.Headers) > 0 {
			c.Headers = map[string]string{}
			for k, v := range s.Headers {
				c.Headers[k] = expand(v)
			}
		}
		if s.SecretEnv != "" {
			if !envVarName.MatchString(s.SecretEnv) {
				return nil, fmt.Errorf("connection %q: secret_env %q is not an environment variable name", s.ID, s.SecretEnv)
			}
			if getenv(s.SecretEnv) == "" {
				missing[s.SecretEnv] = true
			}
			c.SecretRef = envRefPrefix + s.SecretEnv
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("connection %q is listed twice", c.ID)
		}
		seen[c.ID] = true
		out = append(out, c)
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("%s: environment variables not set: %s", path, strings.Join(names, ", "))
	}
	for i := range out {
		if err := out[i].Validate(); err != nil {
			return nil, fmt.Errorf("connection %q: %w", out[i].ID, err)
		}
	}
	return out, nil
}

// ManagedStore serves provisioned connections ahead of the underlying store.
// Locked hides and refuses connections created in the UI.
type ManagedStore struct {
	Store
	managed []Connection
	Locked  bool
}

func NewManagedStore(next Store, managed []Connection, locked bool) *ManagedStore {
	return &ManagedStore{Store: next, managed: managed, Locked: locked}
}

func (s *ManagedStore) find(id string) (Connection, bool) {
	for _, c := range s.managed {
		if c.ID == id {
			return c, true
		}
	}
	return Connection{}, false
}

func (s *ManagedStore) ListConnections() ([]Connection, error) {
	out := append([]Connection{}, s.managed...)
	if s.Locked {
		return out, nil
	}
	rest, err := s.Store.ListConnections()
	if err != nil {
		return nil, err
	}
	for _, c := range rest {
		if _, ok := s.find(c.ID); !ok {
			out = append(out, c)
		}
	}
	return out, nil
}

func (s *ManagedStore) GetConnection(id string) (Connection, error) {
	if c, ok := s.find(id); ok {
		return c, nil
	}
	if s.Locked {
		return Connection{}, fmt.Errorf("connection %q: %w", id, ErrNotFound)
	}
	return s.Store.GetConnection(id)
}

// CanSave reports why a connection cannot be created or edited in the UI.
func (s *ManagedStore) CanSave(id string) error {
	if _, ok := s.find(id); ok {
		return ErrManaged
	}
	if s.Locked {
		return ErrLocked
	}
	return nil
}

func (s *ManagedStore) SaveConnection(c Connection) (Connection, error) {
	if err := s.CanSave(c.ID); err != nil {
		return Connection{}, err
	}
	c.Managed = false
	return s.Store.SaveConnection(c)
}

// EnvSecrets resolves "env:NAME" references from the environment and the rest from Next.
type EnvSecrets struct {
	Next   Secrets
	Getenv func(string) string
}

func (s EnvSecrets) Get(ref string) (string, error) {
	if name, ok := strings.CutPrefix(ref, envRefPrefix); ok {
		if v := s.Getenv(name); v != "" {
			return v, nil
		}
		return "", fmt.Errorf("environment variable %s is not set", name)
	}
	return s.Next.Get(ref)
}

func (s EnvSecrets) Has(ref string) bool {
	if name, ok := strings.CutPrefix(ref, envRefPrefix); ok {
		return s.Getenv(name) != ""
	}
	return s.Next.Has(ref)
}

func (s EnvSecrets) Put(ref, value string) error {
	if strings.HasPrefix(ref, envRefPrefix) {
		return ErrManaged
	}
	return s.Next.Put(ref, value)
}
