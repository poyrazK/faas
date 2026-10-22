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
	unset := func(f main.ReleaseGateFlags) main.ReleaseGateFlags {
		if f.MaxGroups == 0 && !f.NewErrors {
			f.MaxGroups = -1
		}
		if f.MaxFailurePercent == 0 && f.Check == "" {
			f.MaxFailurePercent = -1
		}
		f.Name = "g"
		return f
	}
	for _, tc := range []struct {
		name     string
		flags    main.ReleaseGateFlags
		wantKind string
		wantErr  bool
	}{
		{name: "slo", flags: main.ReleaseGateFlags{SLO: "checkout", MaxBurn: 10}, wantKind: "slo"},
		{name: "new errors", flags: main.ReleaseGateFlags{NewErrors: true, MaxGroups: 1, MinEvents: 5}, wantKind: "new_errors"},
		{name: "metric below", flags: main.ReleaseGateFlags{Metric: "orders", Below: "5"}, wantKind: "custom_metric"},
		// adr: 20261022082305 — metric changes and synthetic checks.
		{name: "metric drop", flags: main.ReleaseGateFlags{Metric: "orders", MaxDrop: 30}, wantKind: "metric_change"},
		{name: "metric rise", flags: main.ReleaseGateFlags{Metric: "latency_ms", MaxRise: 50}, wantKind: "metric_change"},
		{name: "synthetic", flags: main.ReleaseGateFlags{Check: "checkout", MaxFailurePercent: 20}, wantKind: "synthetic"},
		{name: "two kinds", flags: main.ReleaseGateFlags{SLO: "checkout", NewErrors: true, MaxGroups: -1}, wantErr: true},
		{name: "no kind", wantErr: true},
		{name: "metric with both bounds", flags: main.ReleaseGateFlags{Metric: "orders", Below: "1", Above: "9"}, wantErr: true},
		{name: "metric bound and change", flags: main.ReleaseGateFlags{Metric: "orders", Below: "1", MaxDrop: 9}, wantErr: true},
		{name: "metric bound not a number", flags: main.ReleaseGateFlags{Metric: "orders", Below: "many"}, wantErr: true},
		{name: "burn rate on new errors", flags: main.ReleaseGateFlags{NewErrors: true, MaxBurn: 5, MaxGroups: -1}, wantErr: true},
		{name: "min events on slo", flags: main.ReleaseGateFlags{SLO: "checkout", MinEvents: 3}, wantErr: true},
		{name: "change without metric", flags: main.ReleaseGateFlags{Check: "checkout", MaxRise: 5}, wantErr: true},
		{name: "failure percent on slo", flags: main.ReleaseGateFlags{SLO: "checkout", MaxFailurePercent: 5}, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req, err := main.ReleaseGateRequest(unset(tc.flags))
			if tc.wantErr != (err != nil) {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && req.Kind != tc.wantKind {
				t.Fatalf("kind = %q, want %q", req.Kind, tc.wantKind)
			}
			switch tc.name {
			case "new errors":
				if req.MaxNewGroups == nil || *req.MaxNewGroups != 1 || req.MinEvents != 5 {
					t.Fatalf("request = %+v", req)
				}
			case "metric below":
				if req.Comparison != "below" || req.Threshold == nil || *req.Threshold != 5 {
					t.Fatalf("request = %+v", req)
				}
			case "metric drop":
				if req.Comparison != "below" || req.MaxChangePercent != 30 || req.Threshold != nil {
					t.Fatalf("request = %+v", req)
				}
			case "metric rise":
				if req.Comparison != "above" || req.MaxChangePercent != 50 {
					t.Fatalf("request = %+v", req)
				}
			case "synthetic":
				if req.Check != "checkout" || req.MaxFailurePercent == nil || *req.MaxFailurePercent != 20 {
					t.Fatalf("request = %+v", req)
				}
			}
		})
	}
}
