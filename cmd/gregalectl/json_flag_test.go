// json_flag_test.go — coverage pass for cmd/gregalectl/json_flag.go
// (Cluster 5b of the gregalectl coverage depth-pass, follow-on to
// PR #1044).
//
// Pins the boolean-string mapping used by --json=BOOL:
//   - "false", "no", "off", "0" (case-insensitive) → false
//   - everything else → true
//
// Mirrors cmd/gregale/json_flag.go:84. Operator commands don't use
// the requireSignedTrue/requireSignedFalse closed enum, so this is
// a simplified version (per output.go header comment).
package main

import (
	"reflect"
	"testing"
)

func TestApplyJSONFlagPreservesTerminatedArguments(t *testing.T) {
	for _, args := range [][]string{
		{"manifest", "--", "--json"},
		{"manifest", "--", "-j"},
		{"manifest", "--", "--json=custom"},
	} {
		t.Run(args[len(args)-1], func(t *testing.T) {
			previousJSON := jsonOutput
			t.Cleanup(func() { jsonOutput = previousJSON })
			jsonOutput = false
			t.Setenv("FAAS_JSON", "0")
			if got := applyJSONFlag(append([]string(nil), args...)); !reflect.DeepEqual(got, args) || jsonOutput {
				t.Fatalf("args=%v json=%t, want %v and human output", got, jsonOutput, args)
			}
			if got := applyJSONFlag(append([]string{"--json"}, args...)); !reflect.DeepEqual(got, args) || !jsonOutput {
				t.Fatalf("args=%v json=%t, want %v and JSON output", got, jsonOutput, args)
			}
		})
	}
}

// TestJsonBoolTrue_FalseStrings pins the closed-vocab of false
// tokens. Operator consumers (manifest, release) read this when
// --json=false/--json=no/etc. is passed.
func TestJsonBoolTrue_FalseStrings(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"false", false},
		{"FALSE", false},
		{"False", false},
		{"no", false},
		{"NO", false},
		{"No", false},
		{"off", false},
		{"OFF", false},
		{"Off", false},
		{"0", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := jsonBoolTrue(tc.in); got != tc.want {
				t.Errorf("jsonBoolTrue(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestJsonBoolTrue_TrueStrings pins that anything not in the
// closed false-vocab maps to true. This is the allow-by-default
// shape operators depend on (--json=true, --json=1, --json=yes,
// --json=on, --json=arbitrary).
func TestJsonBoolTrue_TrueStrings(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"true", true},
		{"yes", true},
		{"on", true},
		{"1", true},
		{"enabled", true},
		{"", true}, // empty string defaults to true
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := jsonBoolTrue(tc.in); got != tc.want {
				t.Errorf("jsonBoolTrue(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestApplyJSONFlag_PrefixFalse pins that --json=false sets
// jsonOutput=false and strips the flag from the residual args.
func TestApplyJSONFlag_PrefixFalse(t *testing.T) {
	prevJSON := jsonOutput
	t.Cleanup(func() { jsonOutput = prevJSON })
	jsonOutput = false

	residual := applyJSONFlag([]string{"--json=false", "foo"})
	if jsonOutput {
		t.Errorf("applyJSONFlag(--json=false) set jsonOutput=true, want false")
	}
	if len(residual) != 1 || residual[0] != "foo" {
		t.Errorf("applyJSONFlag residual = %v, want [foo]", residual)
	}
}

// TestApplyJSONFlag_PrefixTrue pins that --json=true (or any other
// non-false token) sets jsonOutput=true and strips the flag.
func TestApplyJSONFlag_PrefixTrue(t *testing.T) {
	prevJSON := jsonOutput
	t.Cleanup(func() { jsonOutput = prevJSON })
	jsonOutput = false

	residual := applyJSONFlag([]string{"--json=true", "bar"})
	if !jsonOutput {
		t.Errorf("applyJSONFlag(--json=true) did not set jsonOutput=true")
	}
	if len(residual) != 1 || residual[0] != "bar" {
		t.Errorf("applyJSONFlag residual = %v, want [bar]", residual)
	}
}

// TestApplyJSONFlag_FAASEnv pins the FAAS_JSON=1 short-circuit. The
// env is checked first; any non-1 value does NOT auto-enable.
func TestApplyJSONFlag_FAASEnv(t *testing.T) {
	prevJSON := jsonOutput
	t.Cleanup(func() { jsonOutput = prevJSON })
	jsonOutput = false
	t.Setenv("FAAS_JSON", "1")
	residual := applyJSONFlag([]string{"foo"})
	if !jsonOutput {
		t.Errorf("applyJSONFlag with FAAS_JSON=1 did not enable jsonOutput")
	}
	if len(residual) != 1 || residual[0] != "foo" {
		t.Errorf("applyJSONFlag residual = %v, want [foo]", residual)
	}
}

// TestApplyJSONFlag_NoFlag pins the no-flag path: args pass through
// unchanged and jsonOutput is left at its current value.
func TestApplyJSONFlag_NoFlag(t *testing.T) {
	prevJSON := jsonOutput
	t.Cleanup(func() { jsonOutput = prevJSON })
	jsonOutput = false

	residual := applyJSONFlag([]string{"foo", "bar"})
	if jsonOutput {
		t.Errorf("applyJSONFlag no-flag changed jsonOutput to true")
	}
	if len(residual) != 2 || residual[0] != "foo" || residual[1] != "bar" {
		t.Errorf("applyJSONFlag residual = %v, want [foo bar]", residual)
	}
}
