package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
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

func TestParseDebugSincePositive(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		raw  string
		want time.Duration
	}{
		{raw: "30m", want: 30 * time.Minute},
		{raw: "24h", want: 24 * time.Hour},
		{raw: "3d", want: 3 * 24 * time.Hour},
	} {
		got, ok := parseDebugSincePositive(tc.raw)
		if !ok || got != tc.want {
			t.Errorf("parseDebugSincePositive(%q) = (%s, %t), want (%s, true)", tc.raw, got, ok, tc.want)
		}
	}
	for _, raw := range []string{"", "nonsense", "0h", "-1h", "0d", "-1d"} {
		if got, ok := parseDebugSincePositive(raw); ok || got != 0 {
			t.Errorf("parseDebugSincePositive(%q) = (%s, %t), want (0, false)", raw, got, ok)
		}
	}
}

func TestDebugCoverageRejectsInvalidSince(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "coverage-since-validation")
	for _, raw := range []string{"nonsense", "0h", "-1h", "0d", "-1d"} {
		rec := e.do(t, http.MethodGet, "/v1/apps/coverage-since-validation/debug/coverage?since="+url.QueryEscape(raw), nil, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("since=%q status = %d, want 400: %s", raw, rec.Code, rec.Body.String())
		}
		var problem api.Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &problem); err != nil {
			t.Fatalf("since=%q decode problem: %v", raw, err)
		}
		if problem.Code != api.CodeValidation {
			t.Errorf("since=%q problem code = %q, want %q", raw, problem.Code, api.CodeValidation)
		}
	}
}
