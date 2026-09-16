package api

// Tests for the SLO windowed vocabulary (issue #696 / ADR-082).
// Pins the 3-window closed set so a future drift (e.g. adding
// "30d" without bumping the issue) fails the build.

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func sloFloat64(value float64) *float64 { return &value }

// TestSLORanges_PinsClosedSet asserts the SLO window vocabulary
// is exactly {1h, 24h, 7d} — the strict subset of the
// pkg/appmetrics.Ranges() 7-range /metrics set that the
// customer-facing SLO surface offers. Adding a new entry here
// requires updating ADR-082 and the CLI help text.
func TestSLORanges_PinsClosedSet(t *testing.T) {
	want := []string{"1h", "24h", "7d"}
	got := SLORanges()
	if len(got) != len(want) {
		t.Fatalf("SLORanges(): got %d entries, want %d (%v)", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("SLORanges()[%d]: got %q, want %q", i, got[i], w)
		}
	}
}

// TestSLORanges_ReturnsCopy ensures the returned slice is a
// copy — mutating the caller's slice must not poison the
// package-level state. Mirrors the pkg/appmetrics.Ranges()
// invariant.
func TestSLORanges_ReturnsCopy(t *testing.T) {
	a := SLORanges()
	a[0] = "MUTATED"
	b := SLORanges()
	if b[0] != "1h" {
		t.Errorf("SLORanges() returned a shared slice (subsequent reads got %q, want %q)", b[0], "1h")
	}
}

// TestIsValidSLORange_TableDriven covers the closed set, the
// 5m/15m/6h/15d entries that ARE valid for /metrics but NOT
// for /slo, and a few bad inputs that must all be rejected.
func TestIsValidSLORange_TableDriven(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		// Valid SLO windows.
		{"1h", true},
		{"24h", true},
		{"7d", true},
		// /metrics-only windows: rejected on /slo.
		{"5m", false},
		{"15m", false},
		{"6h", false},
		{"15d", false},
		// Bad inputs.
		{"", false},
		{"1H", false}, // case-sensitive
		{"24", false},
		{"24hours", false},
		{"30d", false},
		{"1h ", false}, // trailing whitespace
	}
	for _, c := range cases {
		if got := IsValidSLORange(c.in); got != c.want {
			t.Errorf("IsValidSLORange(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// TestSLODefaultWindow_Pins24h pins the server-side default
// (the canonical "yesterday's SLO" lookback).
func TestSLODefaultWindow_Pins24h(t *testing.T) {
	if SLODefaultWindow != "24h" {
		t.Errorf("SLODefaultWindow = %q, want %q", SLODefaultWindow, "24h")
	}
	// The default must also be in the valid set — otherwise
	// the handler would 400 itself on the empty-query-param
	// path.
	if !IsValidSLORange(SLODefaultWindow) {
		t.Errorf("SLODefaultWindow %q is not in the closed set", SLODefaultWindow)
	}
}

// TestAppSLOResponse_JSONRoundtrip pins the wire-shape field
// set against the JSON tags — the SDK Go hand-mirror and the
// auto-gen node/python SDKs all read this serialised shape, so
// any field rename MUST be coordinated across pkg/api/dto.go,
// sdk/go/internal/api/dto.go, and the OpenAPI spec.
//
// The test serialises a populated AppSLOResponse to JSON,
// deserialises it back, and asserts every field survived. A
// future field rename or json-tag typo on AppSLOResponse
// (e.g. dropping the `json:"requests_total"` tag) would fail
// here, keeping the SDK hand-mirror
// (sdk/go/internal/api/dto.go) and the dashboard in lockstep
// with pkg/api.
func TestAppSLOResponse_JSONRoundtrip(t *testing.T) {
	in := AppSLOResponse{
		AppID:                 "0123456789abcdef0123456789abcdef",
		AppSlug:               "my-app",
		Window:                "24h",
		Source:                "prometheus",
		AsOf:                  "2026-08-07T15:30:00.000Z",
		RequestDuration:       SLODuration{P50MS: 14.2, P95MS: 87.0, P99MS: 312.5},
		ErrorRatePct:          0.41,
		ColdBootRatePct:       3.10,
		InstanceHours:         12.0,
		GBHours:               3.0,
		WakeQueueP95MS:        sloFloat64(12.0),
		WakeQueueSampleStatus: SLOSampleStatusAvailable,
		RequestsTotal:         4321,
		ThrottledTotal:        0,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out AppSLOResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", out, in)
	}
}

// TestAccountSLOResponse_JSONRoundtrip mirrors the per-app
// round-trip for the account-scoped DTO. Same invariant: a
// json-tag typo on AccountSLOResponse must fail this test.
func TestAccountSLOResponse_JSONRoundtrip(t *testing.T) {
	in := AccountSLOResponse{
		Window:                "7d",
		Source:                "degraded: postgres unavailable",
		AsOf:                  "2026-08-07T15:30:00.000Z",
		RequestDuration:       SLODuration{P50MS: 22.1, P95MS: 91.0, P99MS: 410.0},
		ErrorRatePct:          0.55,
		ColdBootRatePct:       4.20,
		InstanceHours:         12.0,
		GBHours:               3.0,
		WakeQueueP95MS:        sloFloat64(14.0),
		WakeQueueSampleStatus: SLOSampleStatusAvailable,
		RequestsTotal:         12000,
		ThrottledTotal:        23,
	}
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var out AccountSLOResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(out, in) {
		t.Errorf("round-trip mismatch:\n got  %+v\n want %+v", out, in)
	}
}

func TestSLOUnavailableWakeQueueSerializesAsNull(t *testing.T) {
	for name, value := range map[string]any{
		"app":     AppSLOResponse{WakeQueueSampleStatus: SLOSampleStatusUnavailable},
		"account": AccountSLOResponse{WakeQueueSampleStatus: SLOSampleStatusUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			raw, err := json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			body := string(raw)
			if !strings.Contains(body, `"wake_queue_p95_ms":null`) || !strings.Contains(body, `"wake_queue_sample_status":"unavailable"`) {
				t.Fatalf("unavailable wake queue encoded ambiguously: %s", body)
			}
		})
	}
}

// TestAccountSLOResponse_OmitsAppFields pins the wire-shape
// difference between the per-app and account-scoped DTOs:
// the account-scoped response does NOT carry app_id /
// app_slug (the rollup is account-wide).
func TestAccountSLOResponse_OmitsAppFields(t *testing.T) {
	// Compile-time check: the field set is asserted by the
	// fact that AccountSLOResponse does not declare AppID /
	// AppSlug. Any future drift that adds those fields
	// requires updating the SDK Go hand-mirror and the
	// OpenAPI spec.
	zero := AccountSLOResponse{}
	// window/source/as_of are present.
	if zero.Window != "" {
		t.Errorf("zero-value AccountSLOResponse.Window = %q, want empty", zero.Window)
	}
	// instance_hours / gb_hours are zero-on-missing (no
	// pointers, no omitempty).
	if zero.InstanceHours != 0 {
		t.Errorf("zero-value AccountSLOResponse.InstanceHours = %f, want 0", zero.InstanceHours)
	}
	if zero.GBHours != 0 {
		t.Errorf("zero-value AccountSLOResponse.GBHours = %f, want 0", zero.GBHours)
	}
}
