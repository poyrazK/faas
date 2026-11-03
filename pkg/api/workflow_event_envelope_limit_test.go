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
