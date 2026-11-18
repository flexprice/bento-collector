// Package spec defines collector specs and connections and validates them.
package spec

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

const (
	ConnectionPrometheus = "prometheus"
	ConnectionFlexprice  = "flexprice"

	ModeWindow    = "window"
	ModeUsageDate = "usage_date"

	CustomerStatic = "static"
	CustomerLabel  = "label"

	TimestampWindowStart = "window_start"
	TimestampWindowEnd   = "window_end"

	UsageDatePlaceholder = "__USAGE_DATE__"
)

var EnvVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	sensitiveHeader = regexp.MustCompile(`(?i)(authorization|cookie|token|secret|password|api[-_]?key|credential)`)
)

// Connection is a source or destination from the deployment's connections file.
// The secret is never stored, only the environment variable that holds it.
type Connection struct {
	ID        string            `json:"id" yaml:"id"`
	Name      string            `json:"name,omitempty" yaml:"name"`
	Type      string            `json:"type" yaml:"type"`
	URL       string            `json:"url" yaml:"url"`
	AuthType  string            `json:"auth_type,omitempty" yaml:"auth_type"`
	Username  string            `json:"username,omitempty" yaml:"username"`
	SecretEnv string            `json:"secret_env,omitempty" yaml:"secret_env"`
	Headers   map[string]string `json:"headers,omitempty" yaml:"headers"`
}

// Collector is one saved recipe: which source to query, when, and how each
// result row becomes a Flexprice event.
type Collector struct {
	ID          string    `json:"id"`
	Name        string    `json:"name,omitempty"`
	Version     int       `json:"version,omitempty"`
	SavedAt     time.Time `json:"saved_at,omitempty"`
	SavedBy     string    `json:"saved_by,omitempty"`
	Source      Source    `json:"source"`
	Trigger     Trigger   `json:"trigger"`
	Mapping     Mapping   `json:"mapping"`
	Destination string    `json:"destination"`
}

type Source struct {
	Type       string `json:"type"`
	Connection string `json:"connection"`
	Query      string `json:"query"`
	// Mode "window" evaluates the query at the window end. "usage_date"
	// substitutes __USAGE_DATE__ and expects a usage_date label on each row.
	Mode string `json:"mode,omitempty"`
}

type Trigger struct {
	// Schedule is a 5-field cron in UTC for the external scheduler.
	Schedule     string        `json:"schedule,omitempty"`
	Window       string        `json:"window,omitempty"`
	Completeness *Completeness `json:"completeness,omitempty"`
}

// Completeness gates a run: the query is evaluated at the window end and every
// returned value must be at least Min, otherwise nothing is sent.
type Completeness struct {
	Query string  `json:"query"`
	Min   float64 `json:"min"`
}

type Mapping struct {
	EventName      string            `json:"event_name"`
	Customer       CustomerMapping   `json:"customer"`
	Unit           string            `json:"unit,omitempty"`
	RequiredLabels []string          `json:"required_labels,omitempty"`
	DropLabels     []string          `json:"drop_labels,omitempty"`
	Properties     map[string]string `json:"properties,omitempty"`
	Timestamp      string            `json:"timestamp,omitempty"`
	EventSource    string            `json:"event_source,omitempty"`
}

// CustomerMapping picks external_customer_id: a fixed value, or a label whose
// value is used directly or translated through Lookup.
type CustomerMapping struct {
	From   string            `json:"from"`
	Value  string            `json:"value,omitempty"`
	Label  string            `json:"label,omitempty"`
	Lookup map[string]string `json:"lookup,omitempty"`
	// Skip lists label values that are known and deliberately not billed
	// (internal or trial tenants). Their rows are counted, not rejected.
	Skip []string `json:"skip,omitempty"`
}

func ValidID(id string) error {
	if !idPattern.MatchString(id) {
		return fmt.Errorf("id %q must be 1-63 lowercase letters, digits, '-' or '_'", id)
	}
	return nil
}

func (c *Connection) Validate() error {
	if err := ValidID(c.ID); err != nil {
		return err
	}
	if c.Type != ConnectionPrometheus && c.Type != ConnectionFlexprice {
		return fmt.Errorf("type must be %q or %q", ConnectionPrometheus, ConnectionFlexprice)
	}
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return fmt.Errorf("url must start with http:// or https://")
	}
	switch c.AuthType {
	case "", "none":
	case "bearer", "api_key":
		if c.AuthType == "api_key" && c.Type != ConnectionFlexprice {
			return fmt.Errorf("auth api_key is only for Flexprice connections; use bearer or basic")
		}
		if c.SecretEnv == "" {
			return fmt.Errorf("auth %q needs secret_env", c.AuthType)
		}
	case "basic":
		if c.Username == "" || c.SecretEnv == "" {
			return fmt.Errorf("basic auth needs a username and secret_env")
		}
	default:
		return fmt.Errorf("unknown auth_type %q", c.AuthType)
	}
	for k := range c.Headers {
		if sensitiveHeader.MatchString(k) {
			return fmt.Errorf("header %q looks like a credential; use auth_type and secret_env instead, headers are plain text", k)
		}
	}
	if c.Type == ConnectionFlexprice && c.SecretEnv == "" {
		return fmt.Errorf("a Flexprice connection needs secret_env for its API key")
	}
	if c.SecretEnv != "" && !EnvVarName.MatchString(c.SecretEnv) {
		return fmt.Errorf("secret_env %q is not an environment variable name", c.SecretEnv)
	}
	return nil
}

// Normalize fills defaults so stored specs are explicit.
func (c *Collector) Normalize() {
	if c.Source.Type == "" {
		c.Source.Type = ConnectionPrometheus
	}
	if c.Source.Mode == "" {
		c.Source.Mode = ModeWindow
	}
	if c.Trigger.Window == "" {
		c.Trigger.Window = "1d"
	}
	if c.Trigger.Schedule == "" {
		c.Trigger.Schedule = "15 0 * * *"
	}
	if c.Mapping.Timestamp == "" {
		c.Mapping.Timestamp = TimestampWindowStart
	}
	if c.Mapping.EventSource == "" {
		c.Mapping.EventSource = "collector:" + c.ID
	}
}

func (c *Collector) Validate() error {
	var errs []string
	add := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if err := ValidID(c.ID); err != nil {
		add("%v", err)
	}
	if c.Source.Type != ConnectionPrometheus {
		add("source.type must be %q", ConnectionPrometheus)
	}
	if c.Source.Connection == "" {
		add("source.connection is required")
	}
	if strings.TrimSpace(c.Source.Query) == "" {
		add("source.query is required")
	}
	switch c.Source.Mode {
	case ModeWindow:
	case ModeUsageDate:
		if !strings.Contains(c.Source.Query, UsageDatePlaceholder) {
			add("usage_date mode needs %s in the query", UsageDatePlaceholder)
		}
	default:
		add("source.mode must be %q or %q", ModeWindow, ModeUsageDate)
	}
	if c.Trigger.Window != "1d" {
		add("trigger.window only supports 1d")
	}
	if _, err := cron.ParseStandard(c.Trigger.Schedule); err != nil {
		add("trigger.schedule: %v", err)
	}
	if cp := c.Trigger.Completeness; cp != nil && strings.TrimSpace(cp.Query) == "" {
		add("trigger.completeness.query is required when completeness is set")
	}
	m := c.Mapping
	if strings.TrimSpace(m.EventName) == "" {
		add("mapping.event_name is required")
	}
	switch m.Customer.From {
	case CustomerStatic:
		if m.Customer.Value == "" {
			add("mapping.customer.value is required for a static customer")
		}
	case CustomerLabel:
		if m.Customer.Label == "" {
			add("mapping.customer.label is required")
		}
		for _, v := range m.Customer.Skip {
			if _, ok := m.Customer.Lookup[v]; ok {
				add("mapping.customer: %q is both mapped and skipped", v)
			}
		}
		for k, v := range m.Customer.Lookup {
			if k == "" || v == "" {
				add("mapping.customer.lookup has an empty key or value")
			}
		}
	default:
		add("mapping.customer.from must be %q or %q", CustomerStatic, CustomerLabel)
	}
	if m.Timestamp != TimestampWindowStart && m.Timestamp != TimestampWindowEnd {
		add("mapping.timestamp must be %q or %q", TimestampWindowStart, TimestampWindowEnd)
	}
	for k := range m.Properties {
		if reserved[k] {
			add("mapping.properties.%s is set by the collector", k)
		}
	}
	if c.Destination == "" {
		add("destination is required")
	}
	if len(errs) > 0 {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

var reserved = map[string]bool{"value": true, "unit": true, "window_start": true, "window_end": true, "window_seconds": true}

// Window is one closed UTC day.
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func DayWindow(day time.Time) Window {
	start := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	return Window{Start: start, End: start.Add(24 * time.Hour)}
}

// ParseDay parses YYYY-MM-DD, defaulting to yesterday, and refuses open days.
func ParseDay(s string, now time.Time) (Window, error) {
	now = now.UTC()
	if s == "" {
		return DayWindow(now.Add(-24 * time.Hour)), nil
	}
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		return Window{}, fmt.Errorf("day must be YYYY-MM-DD: %w", err)
	}
	w := DayWindow(d)
	if w.End.After(now) {
		return Window{}, fmt.Errorf("day %s has not finished in UTC", s)
	}
	return w, nil
}
