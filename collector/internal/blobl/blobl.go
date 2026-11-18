// Package blobl writes values as Bloblang literals.
package blobl

import (
	"bytes"
	"encoding/json"
	"sort"
	"strings"
)

// Quote returns s as a Bloblang (JSON) string literal.
func Quote(s string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return strings.TrimSpace(buf.String())
}

func QuoteAll(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = Quote(s)
	}
	return out
}

// Literal returns v as a Bloblang literal; string maps keep a stable key order.
func Literal(v any) (string, error) {
	if m, ok := v.(map[string]string); ok {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = Quote(k) + ": " + Quote(m[k])
		}
		return "{" + strings.Join(parts, ", ") + "}", nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
