// adr: 1623
package debugcompare_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/debugcompare"
)

const sourceID = "11111111-1111-4111-8111-111111111111"
const targetID = "22222222-2222-4222-8222-222222222222"

func TestBuildRetainedComparison(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, mode := range []string{"default", "historical", "duration_clamp", "anchored_clamp", "one_side", "empty", "source_failure", "target_failure"} {
		t.Run(mode, func(t *testing.T) {
			req := api.DebugCompareRequest{Source: sourceID, Mirror: targetID, Route: "/orders"}
			wantStart, wantEnd := now.Add(-time.Hour), now
			clamped := false
			switch mode {
			case "historical":
				req.Since, req.Until = "2h", now.Add(-time.Hour).Format(time.RFC3339Nano)
				wantStart, wantEnd = now.Add(-3*time.Hour), now.Add(-time.Hour)
			case "duration_clamp":
				req.Since = "7d"
				wantStart = now.Add(-3 * 24 * time.Hour)
				clamped = true
			case "anchored_clamp":
				req.Since, req.Until = "2d", now.Add(-2*24*time.Hour).Format(time.RFC3339Nano)
				wantStart, wantEnd = now.Add(-3*24*time.Hour), now.Add(-2*24*time.Hour)
				clamped = true
			}
			calls := 0
			out, problem := debugcompare.Build(t.Context(), "app-1", req, 3, now, func(ctx context.Context, dep string, start, end time.Time, route string) (map[string]debugcompare.Stats, error) {
				calls++
				if !start.Equal(wantStart) || !end.Equal(wantEnd) || route != "/orders" {
					t.Fatal("query did not use the exact effective window/filter")
				}
				if (calls == 1 && dep != sourceID) || (calls == 2 && dep != targetID) {
					t.Fatal("deployment order changed")
				}
				if mode == "source_failure" || mode == "target_failure" && calls == 2 {
					return nil, errors.New("private database error")
				}
				if mode == "empty" || mode == "one_side" && calls == 2 {
					return nil, nil
				}
				return map[string]debugcompare.Stats{"/orders": {P50: 0, P95: 50, P99: 100, N: 7}}, nil
			})
			if mode == "source_failure" || mode == "target_failure" {
				if problem == nil || problem.Status != 503 || problem.Detail == "private database error" {
					t.Fatal("read failure was hidden or leaked")
				}
				return
			}
			if problem != nil || calls != 2 || out.AppID != "app-1" || out.RetentionClamped == nil || *out.RetentionClamped != clamped || out.Since != wantEnd.Sub(wantStart).String() || out.WindowStart != wantStart.Format(time.RFC3339Nano) || out.WindowEnd != wantEnd.Format(time.RFC3339Nano) || out.RouteFilter != "/orders" {
				t.Fatalf("comparison metadata: %+v %+v", out, problem)
			}
			if mode == "empty" {
				if out.Routes == nil || len(out.Routes) != 0 {
					t.Fatal("empty union was not an explicit empty array")
				}
				return
			}
			if len(out.Routes) != 1 || out.Routes[0].SourceN != 7 || out.Routes[0].SourceP50 != 0 {
				t.Fatal("weighted counts or observed zero latency lost")
			}
			if mode == "one_side" && (out.Routes[0].MirrorN != 0 || out.Routes[0].MirrorP95 != 0) {
				t.Fatal("missing side synthesized percentiles")
			}
		})
	}
}

func TestBuildRejectsInvalidWindowsBeforeReads(t *testing.T) {
	now := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	for _, mode := range []string{"source", "target", "nil_uuid", "zero", "negative", "malformed", "overflow_days", "invalid_until", "future_until", "expired_until"} {
		t.Run(mode, func(t *testing.T) {
			req := api.DebugCompareRequest{Source: sourceID, Mirror: targetID}
			switch mode {
			case "source":
				req.Source = "bad"
			case "target":
				req.Mirror = "bad"
			case "nil_uuid":
				req.Source = "00000000-0000-0000-0000-000000000000"
			case "zero":
				req.Since = "0h"
			case "negative":
				req.Since = "-1h"
			case "malformed":
				req.Since = "yesterday"
			case "overflow_days":
				req.Since = "999999999999d"
			case "invalid_until":
				req.Until = "bad"
			case "future_until":
				req.Until = now.Add(time.Second).Format(time.RFC3339Nano)
			case "expired_until":
				req.Until = now.Add(-3 * 24 * time.Hour).Format(time.RFC3339Nano)
			}
			_, problem := debugcompare.Build(t.Context(), "app-1", req, 3, now, func(context.Context, string, time.Time, time.Time, string) (map[string]debugcompare.Stats, error) {
				t.Fatal("invalid selection reached telemetry")
				return nil, nil
			})
			if problem == nil || problem.Status != 400 {
				t.Fatalf("expected validation: %+v", problem)
			}
		})
	}
}
