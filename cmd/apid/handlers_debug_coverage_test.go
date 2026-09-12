package main

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDebugCoverageSignal_WeightsRatesByRepresentedRequests(t *testing.T) {
	t.Parallel()
	got := debugCoverageSignal(3, 7, 10)
	if got != (api.DebugCoverageSignal{Rows: 3, Requests: 7, RatePct: 70}) {
		t.Fatalf("signal = %+v, want rows=3 requests=7 rate=70", got)
	}
	if got := debugCoverageSignal(0, 0, 0); got.RatePct != 0 {
		t.Fatalf("empty signal rate = %v, want 0", got.RatePct)
	}
}

func TestDebugCoverageTimestamp_HandlesNullableAggregate(t *testing.T) {
	t.Parallel()
	want := time.Date(2026, 9, 11, 12, 34, 56, 789000000, time.UTC)
	if got := debugCoverageTimestamp(want); got != "2026-09-11T12:34:56.789Z" {
		t.Fatalf("timestamp = %q, want RFC3339Nano value", got)
	}
	if got := debugCoverageTimestamp(nil); got != "" {
		t.Fatalf("nil timestamp = %q, want empty", got)
	}
	if got := debugCoverageTimestamp("not-a-time"); got != "" {
		t.Fatalf("unexpected timestamp type = %q, want empty", got)
	}
}

func TestParseDebugSinceStrict(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		want    time.Duration
		wantErr bool
	}{
		{name: "default", raw: "", want: 24 * time.Hour},
		{name: "whitespace default", raw: "  ", want: 24 * time.Hour},
		{name: "go duration", raw: "90m", want: 90 * time.Minute},
		{name: "day suffix", raw: "3d", want: 3 * 24 * time.Hour},
		{name: "long duration remains valid", raw: "15d", want: 15 * 24 * time.Hour},
		{name: "malformed", raw: "nonsense", wantErr: true},
		{name: "zero", raw: "0h", wantErr: true},
		{name: "negative duration", raw: "-1h", wantErr: true},
		{name: "negative day suffix", raw: "-1d", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseDebugSinceStrict(tt.raw, 24*time.Hour)
			if (err != nil) != tt.wantErr {
				t.Fatalf("parseDebugSinceStrict(%q) error = %v, wantErr=%v", tt.raw, err, tt.wantErr)
			}
			if !tt.wantErr && got != tt.want {
				t.Fatalf("parseDebugSinceStrict(%q) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}

func TestDebugCoverage_RejectsInvalidSince(t *testing.T) {
	e := setup(t, api.PlanPro)
	if _, err := e.store.CreateApp(t.Context(), state.App{
		AccountID: e.acct.ID,
		Slug:      "debug-coverage-validation",
		Status:    state.AppActive,
	}); err != nil {
		t.Fatalf("CreateApp: %v", err)
	}

	for _, raw := range []string{"nonsense", "0h", "-1h", "-1d"} {
		t.Run(raw, func(t *testing.T) {
			rec := e.do(t, http.MethodGet, "/v1/apps/debug-coverage-validation/debug/coverage?since="+raw, nil, nil)
			assertProblem(t, rec, http.StatusBadRequest, "validation_failed")
			if !strings.Contains(rec.Body.String(), "positive duration") {
				t.Fatalf("problem detail = %s, want positive-duration guidance", rec.Body)
			}
		})
	}
}
