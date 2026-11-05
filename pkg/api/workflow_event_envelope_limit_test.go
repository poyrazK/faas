package api

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWorkflowEventEnvelopeFitsPostgresNumericExpansion(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          bool
	}{
		{"ordinary exponent", `{"value":1e10000}`, true},
		{"positive expansion", `[` + strings.Repeat(`1e10000,`, 105) + `1e10000]`, false},
		{"negative expansion", `[` + strings.Repeat(`1e-10000,`, 105) + `1e-10000]`, false},
		{"quoted exponent", `{"value":"1e999999999999999999999"}`, true},
		{"escaped quote", `{"value":"\\\"1e999999999999999999999"}`, true},
		{"exponent overflow", `{"value":1e999999999999999999999}`, false},
		{"largest postgres integer exponent", `1e131071`, true},
		{"postgres integer exponent overflow", `1e131072`, false},
		{"largest postgres fractional exponent", `1e-16383`, true},
		{"postgres fractional exponent overflow", `1e-16384`, false},
		{"long integer", `1` + strings.Repeat("0", 131072), false},
		{"long fraction", `0.` + strings.Repeat("0", 16383) + `1`, false},
		{"oversized text", `"` + strings.Repeat("x", int(WorkflowRunInputMaxBytes)) + `"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if !json.Valid([]byte(tc.payload)) {
				t.Fatal("invalid test JSON")
			}
			if got := WorkflowEventEnvelopeFits([]byte(tc.payload)); got != tc.want {
				t.Fatalf("fits=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkflowJSONBCompatibleStringsAndNumbers(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		want          bool
	}{
		{"nul escape", `{"value":"\u0000"}`, false},
		{"escaped backslash", `{"value":"\\u0000"}`, true},
		{"valid surrogate pair", `{"value":"\ud83d\ude00"}`, true},
		{"unpaired high surrogate", `{"value":"\ud800"}`, false},
		{"unpaired low surrogate", `{"value":"\udc00"}`, false},
		{"wrong low surrogate", `{"value":"\ud800\u0041"}`, false},
		{"unsupported number", `{"value":1e200000}`, false},
		{"large valid string", `"` + strings.Repeat("x", int(WorkflowRunInputMaxBytes)) + `"`, true},
		{"malformed JSON", `{`, false},
		{"invalid UTF-8", "{\"value\":\"\xff\"}", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkflowJSONBCompatible([]byte(tc.payload)); got != tc.want {
				t.Fatalf("compatible=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkflowJSONBFitsBoundsSerializedSeparators(t *testing.T) {
	payload := []byte(`{"a":1,"b":[2,3]}`)
	if WorkflowJSONBFits(payload, int64(len(payload))) {
		t.Fatal("JSONB separator expansion exceeded the limit")
	}
	if !WorkflowJSONBFits(payload, int64(len(payload)+4)) {
		t.Fatal("valid JSONB should fit including separator expansion")
	}
	canonical := []byte(`{"a": 1, "b": [2, 3]}`)
	if !WorkflowJSONBFits(canonical, int64(len(canonical))) {
		t.Fatal("canonical JSONB should fit again at its exact size")
	}
}
