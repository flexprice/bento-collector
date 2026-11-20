// Package spec defines collector specs and connections and validates them.
package spec

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	ModeWindow    = "window"
	ModeUsageDate = "usage_date"

	// CustomerStatic sends every row to one customer.
	CustomerStatic = "static"
	// CustomerLabel sends a label's value as the customer ID.
	CustomerLabel = "label"

	TimestampWindowStart = "window_start"
	TimestampWindowEnd   = "window_end"

	DefaultValueProperty = "value"

	// Query placeholders, filled in for each window.
	UsageDatePlaceholder = "__USAGE_DATE__" // YYYY-MM-DD of the window start
	WindowPlaceholder    = "__WINDOW__"     // window length as a PromQL duration, e.g. 6h or 1d
)

// WindowSizes are the supported window lengths: whole hours that divide a
// day, so windows stay aligned to UTC midnight.
var WindowSizes = map[string]time.Duration{
	"1h":  time.Hour,
	"2h":  2 * time.Hour,
	"3h":  3 * time.Hour,
	"4h":  4 * time.Hour,
	"6h":  6 * time.Hour,
	"8h":  8 * time.Hour,
	"12h": 12 * time.Hour,
	"1d":  24 * time.Hour,
}

// defaultDelays wait for late samples after a window closes.

var EnvVarName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	propertyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,127}$`)
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

// QueryFor is the query for one window, with every placeholder filled in.
func (s Source) QueryFor(w Window) string {
	return strings.NewReplacer(
		UsageDatePlaceholder, w.Start.UTC().Format("2006-01-02"),
		WindowPlaceholder, w.Length(),
	).Replace(s.Query)
}

type Trigger struct {
	// Window is the length of one usage window, a key of WindowSizes.
	Window string `json:"window,omitempty"`
	// Delay waits for late samples after a window closes before querying it.
	Delay        string        `json:"delay,omitempty"`
	Completeness *Completeness `json:"completeness,omitempty"`
}

func (t Trigger) Size() time.Duration { return WindowSizes[t.Window] }

func (t Trigger) DelayDuration() time.Duration {
	d, _ := time.ParseDuration(t.Delay)
	return d
}

// Cron is when Bento runs the published config: once per window, Delay
// after it closes. A run covers the newest window that closed at least Delay
// earlier.
func (t Trigger) Cron() string {
	delay := int(t.DelayDuration() / time.Minute)
	switch hours := int(t.Size() / time.Hour); {
	case hours == 1:
		return fmt.Sprintf("%d * * * *", delay)
	case hours < 24:
		return fmt.Sprintf("%d */%d * * *", delay, hours)
	}
	return fmt.Sprintf("%d %d * * *", delay%60, delay/60)
}

// defaultDelay waits for late samples: 15 minutes after a day, 5 after shorter windows.
func defaultDelay(window string) string {
	if window == "1d" {
		return "15m"
	}
	return "5m"
}

// Completeness gates a run: the query is evaluated at the window end and every
// returned value must be at least Min, otherwise nothing is sent.
type Completeness struct {
	Query string  `json:"query"`
	Min   float64 `json:"min"`
}

type Mapping struct {
	// MeterID is the Flexprice meter the events feed; EventName and
	// ValueProperty are copied from it.
	MeterID   string `json:"meter_id,omitempty"`
	EventName string `json:"event_name"`
	// ValueProperty is the event property that holds the row's value: the
	// field the meter aggregates.
	ValueProperty  string          `json:"value_property,omitempty"`
	Customer       CustomerMapping `json:"customer"`
	RequiredLabels []string        `json:"required_labels,omitempty"`
	// PropertyLabels and Properties are the event's properties besides the
	// value: a label's value sent under a chosen name (property → label), or a
	// default value. Labels not listed are not sent.
	PropertyLabels map[string]string `json:"property_labels,omitempty"`
	Properties     map[string]string `json:"properties,omitempty"`
	Timestamp      string            `json:"timestamp,omitempty"`
	EventSource    string            `json:"event_source,omitempty"`
}

// CustomerMapping picks external_customer_id: a fixed value, or a label's
// value as is. Customers to leave out are filtered in the query.
type CustomerMapping struct {
	From  string `json:"from"`
	Value string `json:"value,omitempty"`
	Label string `json:"label,omitempty"`
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
	if c.Type == "" {
		return fmt.Errorf("type is required")
	}
	if !strings.HasPrefix(c.URL, "http://") && !strings.HasPrefix(c.URL, "https://") {
		return fmt.Errorf("url must start with http:// or https://")
	}
	switch c.AuthType {
	case "", "none":
	case "bearer", "api_key":
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
	if c.SecretEnv != "" && !EnvVarName.MatchString(c.SecretEnv) {
		return fmt.Errorf("secret_env %q is not an environment variable name", c.SecretEnv)
	}
	return nil
}

// Normalize fills defaults so stored specs are explicit.
func (c *Collector) Normalize() {
	if c.Source.Mode == "" {
		c.Source.Mode = ModeWindow
	}
	if c.Trigger.Window == "" {
		c.Trigger.Window = "1d"
	}
	if c.Trigger.Delay == "" {
		c.Trigger.Delay = defaultDelay(c.Trigger.Window)
	}
	if c.Mapping.ValueProperty == "" {
		c.Mapping.ValueProperty = DefaultValueProperty
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
	if c.Source.Type == "" {
		add("source.type is required")
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
		if c.Trigger.Window != "1d" {
			add("usage_date mode needs a 1d window: usage_date labels name whole days")
		}
	default:
		add("source.mode must be %q or %q", ModeWindow, ModeUsageDate)
	}
	t := c.Trigger
	size, ok := WindowSizes[t.Window]
	if !ok {
		add("trigger.window must be 1h, 2h, 3h, 4h, 6h, 8h, 12h or 1d")
	}
	if d, err := time.ParseDuration(t.Delay); err != nil || d < 0 || d%time.Minute != 0 {
		add("trigger.delay must be a whole number of minutes, e.g. 5m")
	} else if ok && d >= size {
		add("trigger.delay must be shorter than the %s window", t.Window)
	}
	if cp := t.Completeness; cp != nil && strings.TrimSpace(cp.Query) == "" {
		add("trigger.completeness.query is required when completeness is set")
	}
	m := c.Mapping
	if strings.TrimSpace(m.EventName) == "" {
		add("mapping.event_name is required: pick the Flexprice meter")
	}
	if !propertyPattern.MatchString(m.ValueProperty) {
		add("mapping.value_property %q is not a property name", m.ValueProperty)
	}
	switch m.Customer.From {
	case CustomerStatic:
		if m.Customer.Value == "" {
			add("mapping.customer.value is required for a fixed customer")
		}
	case CustomerLabel:
		if m.Customer.Label == "" {
			add("mapping.customer.label is required")
		}
	default:
		add("mapping.customer.from must be %q or %q", CustomerLabel, CustomerStatic)
	}
	if m.Timestamp != TimestampWindowStart && m.Timestamp != TimestampWindowEnd {
		add("mapping.timestamp must be %q or %q", TimestampWindowStart, TimestampWindowEnd)
	}
	reserved := map[string]bool{m.ValueProperty: true}
	for k := range m.Properties {
		if reserved[k] {
			add("mapping.properties.%s is set by the collector", k)
		}
	}
	for k, label := range m.PropertyLabels {
		switch {
		case !propertyPattern.MatchString(k) || reserved[k]:
			add("mapping.property_labels: %q cannot be a property name", k)
		case label == "":
			add("mapping.property_labels.%s needs a label", k)
		case m.Properties[k] != "":
			add("mapping.property_labels.%s also has a default value; pick one", k)
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

// Window is one closed, UTC-aligned usage window.
type Window struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

func WindowOf(start time.Time, size time.Duration) Window {
	start = start.UTC()
	return Window{Start: start, End: start.Add(size)}
}

func DayWindow(day time.Time) Window {
	return WindowOf(time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC), 24*time.Hour)
}

func (w Window) Seconds() int64 { return int64(w.End.Sub(w.Start) / time.Second) }

// Length is the window length as a PromQL duration (1d, 6h, 15m, ...).
func (w Window) Length() string {
	s := w.Seconds()
	switch {
	case s%86400 == 0:
		return strconv.FormatInt(s/86400, 10) + "d"
	case s%3600 == 0:
		return strconv.FormatInt(s/3600, 10) + "h"
	case s%60 == 0:
		return strconv.FormatInt(s/60, 10) + "m"
	}
	return strconv.FormatInt(s, 10) + "s"
}

// LastClosed is the newest window that closed at least delay before now.
// Rendered configs compute the same window in Bloblang.
func LastClosed(size, delay time.Duration, now time.Time) Window {
	sz := int64(size / time.Second)
	end := (now.Unix() - int64(delay/time.Second)) / sz * sz
	return WindowOf(time.Unix(end-sz, 0), size)
}

func parseUTC(s string) (time.Time, error) {
	for _, layout := range []string{"2006-01-02", "2006-01-02T15:04", "2006-01-02T15:04:05", time.RFC3339} {
		if t, err := time.ParseInLocation(layout, s, time.UTC); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("use YYYY-MM-DD or YYYY-MM-DDTHH:MM in UTC: %q", s)
}

// MaxRange bounds a sample range.
const MaxRange = 90 * 24 * time.Hour

// ParseRange parses a sample range in UTC, snapping both ends down to window
// boundaries. An empty to means the end of the last closed window; an empty
// from means seven days before to.
func ParseRange(from, to string, size, delay time.Duration, now time.Time) (Window, error) {
	end := LastClosed(size, delay, now).End
	if to != "" {
		t, err := parseUTC(to)
		if err != nil {
			return Window{}, err
		}
		end = t.Truncate(size)
	}
	start := end.Add(-7 * 24 * time.Hour)
	if from != "" {
		f, err := parseUTC(from)
		if err != nil {
			return Window{}, err
		}
		start = f.Truncate(size)
	}
	switch {
	case end.After(now):
		return Window{}, fmt.Errorf("to must be in the past")
	case !start.Before(end):
		return Window{}, fmt.Errorf("from must be before to, at least one %s apart", WindowOf(end, size).Length())
	case end.Sub(start) > MaxRange:
		return Window{}, fmt.Errorf("the range can be at most 90 days")
	}
	return Window{Start: start.UTC(), End: end.UTC()}, nil
}

// ParseWindow parses a window start in UTC (YYYY-MM-DD or YYYY-MM-DDTHH:MM),
// defaulting to the last closed window. It refuses unaligned and open windows.
func ParseWindow(s string, size, delay time.Duration, now time.Time) (Window, error) {
	if size <= 0 {
		return Window{}, fmt.Errorf("unknown window size")
	}
	if s == "" {
		return LastClosed(size, delay, now), nil
	}
	start, err := parseUTC(s)
	if err != nil {
		return Window{}, err
	}
	if start.Unix()%int64(size/time.Second) != 0 {
		return Window{}, fmt.Errorf("%s is not the start of a %s window (windows are aligned to UTC)", s, WindowOf(start, size).Length())
	}
	w := WindowOf(start, size)
	if w.End.After(now) {
		return Window{}, fmt.Errorf("the window starting %s has not finished yet", w.Start.Format(time.RFC3339))
	}
	return w, nil
}
