package main_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// adr: 20261019071733, 20261019071734 — CLI flag shapes for freezes and gates.

func TestFreezeRequestFromFlags(t *testing.T) {
	now := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name                                               string
		forDur, from, until, weekly, at, tz, afterRollback string
		wantKind                                           string
		wantErr                                            bool
	}{
		{name: "window for 2h", forDur: "2h", wantKind: "window"},
		{name: "window until", until: "2026-12-27T09:00:00Z", wantKind: "window"},
		{name: "open-ended window", wantKind: "window"},
		{name: "weekly", weekly: "fri", at: "16:00", forDur: "64h", tz: "Europe/Istanbul", wantKind: "weekly"},
		{name: "after rollback", afterRollback: "1h", wantKind: "after_rollback"},
		{name: "for and until", forDur: "2h", until: "2026-12-27T09:00:00Z", wantErr: true},
		{name: "weekly without at", weekly: "fri", forDur: "1h", wantErr: true},
		{name: "after rollback with timing", afterRollback: "1h", forDur: "2h", wantErr: true},
		{name: "at without weekly", at: "16:00", wantErr: true},
		{name: "bad duration", forDur: "soon", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := main.FreezeRequestFromFlags(tc.forDur, tc.from, tc.until, tc.weekly, tc.at, tc.tz, tc.afterRollback, now)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if req.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", req.Kind, tc.wantKind)
			}
			if tc.name == "window for 2h" && req.EndsAt != "2026-10-19T12:00:00Z" {
				t.Fatalf("ends_at = %q", req.EndsAt)
			}
			if tc.name == "weekly" && (req.Weekly == nil || req.Weekly.DurationMinutes != 64*60 || req.Weekly.TimeZone != "Europe/Istanbul") {
				t.Fatalf("weekly = %+v", req.Weekly)
			}
			if tc.name == "after rollback" && req.AfterRollbackMinutes != 60 {
				t.Fatalf("after_rollback_minutes = %d", req.AfterRollbackMinutes)
			}
		})
	}
}

func TestReleaseGateRequest(t *testing.T) {
	for _, tc := range []struct {
		name      string
		slo       string
		maxBurn   int
		newErrors bool
		maxGroups int
		minEvents int
		metric    string
		below     string
		above     string
		aiMetric  string
		increase  int
		wantKind  string
		wantErr   bool
	}{
		{name: "slo", slo: "checkout", maxBurn: 10, maxGroups: -1, wantKind: "slo"},
		{name: "new errors", newErrors: true, maxGroups: 1, minEvents: 5, wantKind: "new_errors"},
		{name: "metric below", metric: "orders", below: "5", maxGroups: -1, wantKind: "custom_metric"},
		{name: "two kinds", slo: "checkout", newErrors: true, maxGroups: -1, wantErr: true},
		{name: "no kind", maxGroups: -1, wantErr: true},
		{name: "metric with both bounds", metric: "orders", below: "1", above: "9", maxGroups: -1, wantErr: true},
		{name: "metric bound not a number", metric: "orders", below: "many", maxGroups: -1, wantErr: true},
		{name: "burn rate on new errors", newErrors: true, maxBurn: 5, maxGroups: -1, wantErr: true},
		{name: "min events on slo", slo: "checkout", minEvents: 3, maxGroups: -1, wantErr: true},
		// ADR 20261022153155.
		{name: "ai", aiMetric: "cost_per_call", increase: 30, maxGroups: -1, wantKind: "ai"},
		{name: "ai and metric", aiMetric: "cost_per_call", metric: "orders", below: "5", maxGroups: -1, wantErr: true},
		{name: "increase on slo", slo: "checkout", increase: 30, maxGroups: -1, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := main.ReleaseGateRequest("g", tc.slo, tc.maxBurn, tc.newErrors, tc.maxGroups, tc.minEvents, tc.metric, tc.below, tc.above,
				tc.aiMetric, tc.increase, 0)
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && req.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", req.Kind, tc.wantKind)
			}
			if tc.name == "new errors" && (req.MaxNewGroups == nil || *req.MaxNewGroups != 1 || req.MinEvents != 5) {
				t.Fatalf("request = %+v", req)
			}
			if tc.name == "metric below" && (req.Comparison != "below" || req.Threshold == nil || *req.Threshold != 5) {
				t.Fatalf("request = %+v", req)
			}
			if tc.name == "ai" && (req.Metric != "cost_per_call" || req.MaxIncreasePercent != 30 || req.Threshold != nil) {
				t.Fatalf("request = %+v", req)
			}
		})
	}
}
