package app

import (
	"encoding/json"
	"fmt"
	"strings"
)

// shortID truncates long IDs for readable logging.
func shortID(s string) string {
	if len(s) <= 14 {
		return s
	}
	return s[:6] + "…" + s[len(s)-6:]
}

// nz returns fallback if s is empty or whitespace-only.
func nz(s, fallback string) string {
	if strings.TrimSpace(s) == "" {
		return fallback
	}
	return s
}

// parseMaybeJSONStringArray handles Gamma API fields that may be encoded as
// either a JSON array or a string containing a JSON array.
func parseMaybeJSONStringArray(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}

	// Sometimes Gamma fields are JSON-string-encoded arrays.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		var arr []string
		if err := json.Unmarshal([]byte(s), &arr); err == nil {
			return arr, nil
		}
	}

	// Otherwise they might already be a real JSON array.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err == nil {
		return arr, nil
	}

	// Fallback: array of mixed types.
	var anyArr []any
	if err := json.Unmarshal(raw, &anyArr); err == nil {
		out := make([]string, 0, len(anyArr))
		for _, v := range anyArr {
			out = append(out, fmt.Sprint(v))
		}
		return out, nil
	}

	return nil, fmt.Errorf("unhandled array encoding: %s", string(raw))
}

// parseJSONArray parses a JSON array into a slice of strings.
func parseJSONArray(raw json.RawMessage, dest *[]string) error {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return json.Unmarshal(raw, dest)
}

// difference returns elements in a that are not in b.
func difference(a, b []string) []string {
	bSet := make(map[string]struct{}, len(b))
	for _, v := range b {
		bSet[v] = struct{}{}
	}

	var result []string
	for _, v := range a {
		if _, exists := bSet[v]; !exists {
			result = append(result, v)
		}
	}
	return result
}
