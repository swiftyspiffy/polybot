package app

import (
	"encoding/json"
	"testing"
)

func TestShortID_Util(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"0x1234567890abcdef1234567890abcdef12345678", "0x1234…345678"},
		{"0x123456789012", "0x123456789012"}, // <= 14 chars
		{"shortstring", "shortstring"},
		{"exactly14chars", "exactly14chars"},
		{"fifteencharstr!", "fiftee…arstr!"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := shortID(tt.input)
			if result != tt.expected {
				t.Errorf("shortID(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestNz(t *testing.T) {
	tests := []struct {
		s        string
		fallback string
		expected string
	}{
		{"hello", "default", "hello"},
		{"", "default", "default"},
		{"   ", "default", "default"},
		{"\t\n", "default", "default"},
		{"  content  ", "default", "  content  "},
	}

	for _, tt := range tests {
		t.Run(tt.s, func(t *testing.T) {
			result := nz(tt.s, tt.fallback)
			if result != tt.expected {
				t.Errorf("nz(%q, %q) = %q, want %q", tt.s, tt.fallback, result, tt.expected)
			}
		})
	}
}

func TestParseMaybeJSONStringArray_EmptyInput(t *testing.T) {
	result, err := parseMaybeJSONStringArray(nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for nil input")
	}
}

func TestParseMaybeJSONStringArray_Null(t *testing.T) {
	result, err := parseMaybeJSONStringArray(json.RawMessage("null"))
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if result != nil {
		t.Error("expected nil result for null input")
	}
}

func TestParseMaybeJSONStringArray_DirectArray(t *testing.T) {
	input := json.RawMessage(`["a", "b", "c"]`)
	result, err := parseMaybeJSONStringArray(input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 elements, got %d", len(result))
	}
	if result[0] != "a" || result[1] != "b" || result[2] != "c" {
		t.Errorf("unexpected values: %v", result)
	}
}

func TestParseMaybeJSONStringArray_StringEncodedArray(t *testing.T) {
	// String containing a JSON array
	input := json.RawMessage(`"[\"x\", \"y\", \"z\"]"`)
	result, err := parseMaybeJSONStringArray(input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result) != 3 {
		t.Errorf("expected 3 elements, got %d", len(result))
	}
	if result[0] != "x" || result[1] != "y" || result[2] != "z" {
		t.Errorf("unexpected values: %v", result)
	}
}

func TestParseMaybeJSONStringArray_MixedTypes(t *testing.T) {
	input := json.RawMessage(`[1, "two", 3.5, true]`)
	result, err := parseMaybeJSONStringArray(input)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if len(result) != 4 {
		t.Errorf("expected 4 elements, got %d", len(result))
	}
	// All should be converted to strings
	if result[0] != "1" {
		t.Errorf("expected '1', got %q", result[0])
	}
	if result[1] != "two" {
		t.Errorf("expected 'two', got %q", result[1])
	}
}

func TestParseMaybeJSONStringArray_InvalidJSON(t *testing.T) {
	input := json.RawMessage(`{not valid}`)
	_, err := parseMaybeJSONStringArray(input)
	if err == nil {
		t.Error("expected error for invalid JSON")
	}
}

func TestParseMaybeJSONStringArray_Object(t *testing.T) {
	input := json.RawMessage(`{"key": "value"}`)
	_, err := parseMaybeJSONStringArray(input)
	if err == nil {
		t.Error("expected error for object input")
	}
}
