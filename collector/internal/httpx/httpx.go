// Package httpx holds small helpers shared by the HTTP clients.
package httpx

import "strings"

// Truncate shortens a response body for an error message.
func Truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
