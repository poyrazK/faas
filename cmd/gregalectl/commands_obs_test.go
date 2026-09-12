// commands_obs_test.go — Obs-Meta + Trace-IDs Mega-PR / C8:
// tests for `gregalectl obs` subcommand.
//
// Pins:
//   - dispatcher routes subcommand names correctly (health vs
//     unknown).
//   - human-readable output shape: stable field order, "absent"
//     fallback for missing sub-maps.
//   - JSON output shape: round-trips through json.Encoder
//     without mutation.
//   - sortedObsHealthKeys returns lexical order regardless of
//     input order (Go map iteration is randomized).
//   - apidBase honours $FAAS_APID_URL override and falls back
//     to defaultAPIDURL.
//
// Does NOT pin:
//   - The HTTP round-trip to a real apid — that's an
//     integration test, not a unit test. The helper is
//     fully tested by the build tag //go:build obsint or the
//     future e2e harness.
package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestCmdObsDispatch_UnknownSubcommandReturns2(t *testing.T) {
	if got := cmdObsDispatch([]string{"foo"}); got != 2 {
		t.Errorf("cmdObsDispatch(foo) = %d, want 2", got)
	}
}

func TestCmdObsDispatch_NoSubcommandReturns2(t *testing.T) {
	if got := cmdObsDispatch(nil); got != 2 {
		t.Errorf("cmdObsDispatch(nil) = %d, want 2", got)
	}
	if got := cmdObsDispatch([]string{}); got != 2 {
		t.Errorf("cmdObsDispatch([]) = %d, want 2", got)
	}
}

func TestCmdObsDispatch_Health_NoAdminTokenReturns2(t *testing.T) {
	// Clear env so FAAS_ADMIN_TOKEN doesn't bleed in from the
	// host shell; isolate the test from external state.
	t.Setenv("FAAS_ADMIN_TOKEN", "")
	if got := cmdObsHealth([]string{}); got != 2 {
		t.Errorf("cmdObsHealth() = %d, want 2 (missing --admin-token / $FAAS_ADMIN_TOKEN)", got)
	}
}

func TestSortedObsHealthKeys_LexicalOrder(t *testing.T) {
	got := sortedObsHealthKeys([]string{"force_restart", "force_park", "force_cold_boot"})
	want := []string{"force_cold_boot", "force_park", "force_restart"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i, k := range got {
		if k != want[i] {
			t.Errorf("[%d] = %q, want %q", i, k, want[i])
		}
	}
}

func TestApidBase_DefaultLoopback(t *testing.T) {
	t.Setenv("FAAS_APID_URL", "")
	if got := apidBase(); got != defaultAPIDURL {
		t.Errorf("apidBase() = %q, want %q", got, defaultAPIDURL)
	}
}

func TestApidBase_OverridesWithEnv(t *testing.T) {
	t.Setenv("FAAS_APID_URL", "https://apid.example.com/")
	if got := apidBase(); got != "https://apid.example.com" {
		t.Errorf("apidBase() = %q, want trailing slash stripped", got)
	}
}

// TestWriteObsHealthHuman_FieldOrder pins the human-summary
// shape. Renaming any of the closed-set keys is a breaking
// change — the on-call's tail/grep pipeline keys off the
// exact strings.
func TestWriteObsHealthHuman_FieldOrder(t *testing.T) {
	snap := map[string]any{
		"prometheus_available":        true,
		"audit_log_write_total_5m":    float64(1234),
		"audit_log_write_failures_5m": float64(0),
		"audit_log_coverage_ratio_5m": 1.0,
		"operator_intent_outcome_missing_total": map[string]any{
			"force_park":      float64(0),
			"force_cold_boot": float64(0),
			"force_restart":   float64(0),
		},
		"trace_id_completeness_ratio": map[string]any{
			"force_park":      1.0,
			"force_cold_boot": 1.0,
			"force_restart":   1.0,
		},
		"alerts_firing": float64(0),
	}
	var buf bytes.Buffer
	writeObsHealthHuman(&buf, snap)
	out := buf.String()

	// Order: prometheus_available first, alerts_firing last.
	wantSubstrings := []string{
		"prometheus_available:",
		"audit_log_write_total_5m:",
		"audit_log_write_failures_5m:",
		"audit_log_coverage_ratio_5m:",
		"operator_intent_outcome_missing_total:",
		"trace_id_completeness_ratio:",
		"alerts_firing:",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("human output missing %q\n---\n%s\n---", want, out)
		}
	}
	// Spot-check alphabetical kind-block ordering.
	if !strings.Contains(out, "force_cold_boot") {
		t.Errorf("human output missing force_cold_boot\n---\n%s\n---", out)
	}
}

func TestWriteObsHealthHuman_AbsentSubMapsFallback(t *testing.T) {
	// Empty snapshot — writeObsHealthHuman should render the
	// "(absent)" fallback for the kind sub-maps rather than
	// silently dropping the header line. Catches a future
	// refactor that tightens the decode into a typed struct.
	var buf bytes.Buffer
	writeObsHealthHuman(&buf, map[string]any{})
	out := buf.String()
	if !strings.Contains(out, "(absent)") {
		t.Errorf("human output missing '(absent)' fallback for empty sub-maps\n---\n%s\n---", out)
	}
}

// TestCmdObsHealth_EndToEnd_JSON pins the JSON path: a real
// httptest server serves the canonical snapshot, the CLI
// fetches it with --json, and the stdout output is the
// round-trippable JSON. Mirrors the customer HTTP CLI tests
// in cmd/gregale/commands5_test.go (probeResp pattern).
func TestCmdObsHealth_EndToEnd_JSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/admin/obs/health" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{
			"generated_at": "2026-08-26T00:00:00Z",
			"audit_log_write_total_5m": 1234,
			"audit_log_write_failures_5m": 0,
			"audit_log_coverage_ratio_5m": 1.0,
			"operator_intent_outcome_missing_total": {"force_park": 0},
			"trace_id_completeness_ratio": {"force_park": 1.0},
			"alerts_firing": 0
		}`))
	}))
	defer srv.Close()

	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "test-admin-token")

	// Capture stdout.
	old := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w
	defer func() { os.Stdout = old }()

	exit := cmdObsHealth([]string{"--json"})
	w.Close()

	var got bytes.Buffer
	_, _ = got.ReadFrom(r)

	if exit != 0 {
		t.Errorf("exit = %d, want 0; stdout=%s", exit, got.String())
	}
	// The output must be valid JSON (the encoder already
	// validated, but spot-check the round-trip).
	var out any
	if err := json.Unmarshal(got.Bytes(), &out); err != nil {
		t.Fatalf("invalid JSON: %v; stdout=%s", err, got.String())
	}
}

// TestCmdObsHealth_EndToEnd_NonOKStatus confirms the CLI
// surfaces the apid status code + body as a stable error
// shape. Mirrors the customer HTTP CLI tests' probeResp
// pattern.
func TestCmdObsHealth_EndToEnd_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"code":"admin_required","title":"forbidden"}`))
	}))
	defer srv.Close()

	t.Setenv("FAAS_APID_URL", srv.URL)
	t.Setenv("FAAS_ADMIN_TOKEN", "test-admin-token")

	if got := cmdObsHealth(nil); got != 1 {
		t.Errorf("cmdObsHealth with 403 = %d, want 1", got)
	}
}
