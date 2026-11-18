package store

import (
	"github.com/flexprice/bento-collector/collector/spec"

	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

var envVarRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Connections are owned by the deployment: read from a YAML file at startup,
// read-only in the UI, with secrets taken from the environment variable each
// connection names in secret_env.
type Connections struct {
	list   []spec.Connection
	getenv func(string) string
}

// LoadConnections reads a connections file, expanding ${VAR} in url, username
// and headers. Every referenced variable, including each secret_env, must be set.
func LoadConnections(path string, getenv func(string) string) (*Connections, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var file struct {
		Connections []spec.Connection `yaml:"connections"`
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
	for i := range file.Connections {
		c := &file.Connections[i]
		c.URL, c.Username = expand(c.URL), expand(c.Username)
		for k, v := range c.Headers {
			c.Headers[k] = expand(v)
		}
		if c.SecretEnv != "" && getenv(c.SecretEnv) == "" {
			missing[c.SecretEnv] = true
		}
		if seen[c.ID] {
			return nil, fmt.Errorf("%s: connection %q is listed twice", path, c.ID)
		}
		seen[c.ID] = true
	}
	if len(missing) > 0 {
		names := make([]string, 0, len(missing))
		for n := range missing {
			names = append(names, n)
		}
		sort.Strings(names)
		return nil, fmt.Errorf("%s: environment variables not set: %s", path, strings.Join(names, ", "))
	}
	for _, c := range file.Connections {
		if err := c.Validate(); err != nil {
			return nil, fmt.Errorf("%s: connection %q: %w", path, c.ID, err)
		}
	}
	return &Connections{list: file.Connections, getenv: getenv}, nil
}

func (s *Connections) List() []spec.Connection {
	return append([]spec.Connection{}, s.list...)
}

func (s *Connections) Get(id string) (spec.Connection, error) {
	for _, c := range s.list {
		if c.ID == id {
			return c, nil
		}
	}
	return spec.Connection{}, fmt.Errorf("connection %q is not in the connections file: %w", id, ErrNotFound)
}

// Secret returns the value of the connection's secret_env, or "" without one.
func (s *Connections) Secret(c spec.Connection) (string, error) {
	if c.SecretEnv == "" {
		return "", nil
	}
	if v := s.getenv(c.SecretEnv); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("environment variable %s is not set", c.SecretEnv)
}
