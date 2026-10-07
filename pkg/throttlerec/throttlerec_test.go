package throttlerec_test

// Issue #881 / ADR-091 D20.5 amendment — pkg/throttlerec tests.
//
// Coverage matrix (mirrors the TestAppMetrics_* naming convention
// from pkg/appmetrics/appmetrics_test.go so future readers find
// corresponding tests by name):
//   - happy path: per-route rate() → suggested_rps/burst with 2× headroom
//   - plan ceiling clamp: observed_rps * 2 > plan ceiling → clamped to plan ceiling
//   - floor clamp: observed_rps < 0.5 → suggested_rps = 1 (not 0)
//   - floor clamp: burst ≈ 0 → suggested_burst = 1
//   - __route_other__ exclusion: overflow bucket dropped from Suggestions, count surfaces
//   - 0-observation skip: a route with rate()=0 has no suggestion
//   - empty window: no rows → empty Suggestions, not degraded
//   - degraded fallback: QueryGrouped errors → zeroed fields + "degraded:" Source
//   - nil-client path: Fetch with nil PromQL → "degraded: prometheus not configured"
//   - route_metrics_disabled: Free plan → empty Suggestions + RouteMetricsDisabled=true
//   - app id injection: appID containing " is rejected as degraded
//   - unknown plan: fail OPEN — Suggestions still produced, no ceiling applies
//   - log-injection guard: err string with \r\n → captured slog JSON has neither

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/throttlerec"
)

// stubPromQL is the test double for throttlerec.PromQL. The fn
// callback returns the per-route map for each QueryGrouped call.
// scalarFn handles QueryScalar (dry-run preview, Phase 4 D1);
// when nil the stub returns 0 — dry-run is opt-in so tests that
// don't request it don't have to wire scalarFn.
type stubPromQL struct {
	fn       func(query, outer, inner string) (map[string]map[string]float64, error)
	scalarFn func(query string) (float64, error)
}

func (s *stubPromQL) QueryGrouped(_ context.Context, query, outer, inner string) (map[string]map[string]float64, error) {
	return s.fn(query, outer, inner)
}

func (s *stubPromQL) QueryScalar(_ context.Context, query string) (float64, error) {
	if s.scalarFn == nil {
		return 0, nil
	}
	return s.scalarFn(query)
}

// captureLog returns a logger that writes JSON to the returned buffer.
// Tests assert on the buffer.
func captureLog(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})), buf
}

// appRow is a row-builder helper — Prometheus returns each
// (app, route) pair as a separate series, so the test stub
// builds the map naturally.
func appRow(appID string, rows map[string]float64) map[string]map[string]float64 {
	return map[string]map[string]float64{appID: rows}
}

// TestThrottleRec_HappyPath: a 3-route app with observed rps in
// the 1.0–10.0 range produces a SuggestedRPS = ceil(observed * 2)
// per route, with SuggestedBurst = ceil(suggested * 1.5) under the
// plan ceiling.
func TestThrottleRec_HappyPath(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return appRow("app-1", map[string]float64{
			"GET /users/4f8a": 1.0,
			"POST /orders":    5.0,
			"GET /health":     10.0,
		}), nil
	}}

	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro, // RateLimitRPS=100, Burst=500
		RouteMetricsEnabled: true,
	})
	if src != throttlerec.SourcePrometheus {
		t.Fatalf("src = %q, want %q", src, throttlerec.SourcePrometheus)
	}
	if resp.RouteMetricsDisabled {
		t.Errorf("RouteMetricsDisabled = true, want false")
	}
	if got := len(resp.Suggestions); got != 3 {
		t.Fatalf("Suggestions count = %d, want 3 (%+v)", got, resp.Suggestions)
	}
	// Walk by route so the test is order-independent.
	byRoute := map[string]api.ThrottleSuggestionRow{}
	for _, s := range resp.Suggestions {
		byRoute[s.Route] = s
	}
	// 1.0 → ceil(2) = 2 / burst = ceil(2 * 1.5) = 3
	if r := byRoute["GET /users/4f8a"]; r.SuggestedRPS != 2 || r.SuggestedBurst != 3 {
		t.Errorf("users/4f8a: suggested=%v burst=%d, want 2/3", r.SuggestedRPS, r.SuggestedBurst)
	}
	// 5.0 → ceil(10) = 10 / 1.5× burst = 15
	if r := byRoute["POST /orders"]; r.SuggestedRPS != 10 || r.SuggestedBurst != 15 {
		t.Errorf("orders: suggested=%v burst=%d, want 10/15", r.SuggestedRPS, r.SuggestedBurst)
	}
	// 10.0 → ceil(20) = 20 / 1.5× = 30
	if r := byRoute["GET /health"]; r.SuggestedRPS != 20 || r.SuggestedBurst != 30 {
		t.Errorf("health: suggested=%v burst=%d, want 20/30", r.SuggestedRPS, r.SuggestedBurst)
	}
	if resp.Multiplier != throttlerec.Multiplier {
		t.Errorf("Multiplier = %v, want %v", resp.Multiplier, throttlerec.Multiplier)
	}
	if resp.PlanCeilingRPS != 100 || resp.PlanCeilingBurst != 500 {
		t.Errorf("PlanCeiling = (%d, %d), want (100, 500)", resp.PlanCeilingRPS, resp.PlanCeilingBurst)
	}
}

// TestThrottleRec_PlanCeilingClamp: a route observed at 200 rps
// on a Pro plan (ceiling 100) gets SuggestedRPS clamped to 100
// (plan ceiling), not 400 (uncapped 2×). Burst is also clamped.
func TestThrottleRec_PlanCeilingClamp(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return appRow("app-1", map[string]float64{
			"GET /hot": 200.0,
		}), nil
	}}

	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro, // RateLimitRPS=100, Burst=500
		RouteMetricsEnabled: true,
	})
	if src != throttlerec.SourcePrometheus {
		t.Fatalf("src = %q, want %q", src, throttlerec.SourcePrometheus)
	}
	if len(resp.Suggestions) != 1 {
		t.Fatalf("Suggestions count = %d, want 1", len(resp.Suggestions))
	}
	r := resp.Suggestions[0]
	if r.SuggestedRPS != 100 {
		t.Errorf("SuggestedRPS = %v, want 100 (plan ceiling)", r.SuggestedRPS)
	}
	// Burst = clamp(ceil(100 * 1.5) = 150, 1, 500) = 150.
	if r.SuggestedBurst != 150 {
		t.Errorf("SuggestedBurst = %d, want 150", r.SuggestedBurst)
	}
	if r.ObservedRPS != 200 {
		t.Errorf("ObservedRPS = %v, want 200 (uncapped)", r.ObservedRPS)
	}
}

// TestThrottleRec_FloorClamp_SparseRoute: a route observed at
// 0.1 rps (1 request per 10 minutes) still gets SuggestedRPS=1,
// the 1-rps floor. Without the floor, ceil(0.1 * 2) = 1, but a
// 0.0 observation would produce 0 — a silent no-op that the
// apid validator rejects (memory leak under the LRU invariant).
func TestThrottleRec_FloorClamp_SparseRoute(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return appRow("app-1", map[string]float64{
			"GET /sparse": 0.1,
		}), nil
	}}

	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if src != throttlerec.SourcePrometheus {
		t.Fatalf("src = %q, want %q", src, throttlerec.SourcePrometheus)
	}
	if len(resp.Suggestions) != 1 {
		t.Fatalf("Suggestions count = %d, want 1", len(resp.Suggestions))
	}
	r := resp.Suggestions[0]
	if r.SuggestedRPS != 1 {
		t.Errorf("SuggestedRPS = %v, want 1 (floor)", r.SuggestedRPS)
	}
	if r.SuggestedBurst != 2 {
		// ceil(1 * 1.5) = 2. The smoke test is asserting the
		// floor is at least the rps value (1) so a flapping-burst
		// customer doesn't get a 0-burst rule. The floor is
		// shared between rps and burst, so any of {1, 2} is
		// acceptable; we pin the exact value to make drift
		// visible.
		t.Errorf("SuggestedBurst = %d, want 2 (ceil(1*1.5))", r.SuggestedBurst)
	}
}

// TestThrottleRec_ZeroObservationSkipped: a route with rate()=0
// has no signal and is dropped from Suggestions. Surfacing it as
// "suggested_rps=1" would be a false-positive recommendation —
// the customer would set a rule on a route that doesn't exist.
func TestThrottleRec_ZeroObservationSkipped(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return appRow("app-1", map[string]float64{
			"GET /live":  5.0,
			"GET /ghost": 0.0,
		}), nil
	}}

	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if src != throttlerec.SourcePrometheus {
		t.Fatalf("src = %q, want %q", src, throttlerec.SourcePrometheus)
	}
	if len(resp.Suggestions) != 1 {
		t.Fatalf("Suggestions count = %d, want 1 (ghost dropped)", len(resp.Suggestions))
	}
	if resp.Suggestions[0].Route != "GET /live" {
		t.Errorf("surviving route = %q, want GET /live", resp.Suggestions[0].Route)
	}
}

// TestThrottleRec_RouteOtherExcluded: __route_other__ is dropped
// from Suggestions and the count surfaces as RoutesCollapsed.
// The wildcard route is exactly the signal the customer needs to
// see surfaced, but it is NOT a settable rule.
func TestThrottleRec_RouteOtherExcluded(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return appRow("app-1", map[string]float64{
			"GET /users/4f8a": 1.0,
			"POST /orders":    5.0,
			"__route_other__": 12.0,
		}), nil
	}}

	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if src != throttlerec.SourcePrometheus {
		t.Fatalf("src = %q, want %q", src, throttlerec.SourcePrometheus)
	}
	if len(resp.Suggestions) != 2 {
		t.Fatalf("Suggestions count = %d, want 2 (overflow dropped)", len(resp.Suggestions))
	}
	for _, s := range resp.Suggestions {
		if s.Route == throttlerec.RangeOtherLabel {
			t.Errorf("__route_other__ leaked into Suggestions: %+v", s)
		}
	}
	if resp.RoutesCollapsed != 1 {
		t.Errorf("RoutesCollapsed = %d, want 1", resp.RoutesCollapsed)
	}
}

// TestThrottleRec_EmptyWindow: no rows for the app → empty
// Suggestions and Source="prometheus" (NOT degraded). The "no
// traffic in the last 5m" state is a healthy state, not a
// failure.
func TestThrottleRec_EmptyWindow(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return appRow("app-1", map[string]float64{}), nil
	}}

	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if src != throttlerec.SourcePrometheus {
		t.Errorf("src = %q, want %q (empty is healthy, not degraded)", src, throttlerec.SourcePrometheus)
	}
	if len(resp.Suggestions) != 0 {
		t.Errorf("Suggestions count = %d, want 0", len(resp.Suggestions))
	}
	// The slice should be non-nil so the JSON encoder emits []
	// rather than null — matches the dashboard's empty-state
	// branch.
	if resp.Suggestions == nil {
		t.Errorf("Suggestions = nil, want [] (encoder emits null vs [])")
	}
}

// TestThrottleRec_DegradedFallback: QueryGrouped errors →
// zeroed fields + "degraded: <err>" Source. The dashboard's
// empty-state branch depends on Suggestions being absent when
// degraded.
func TestThrottleRec_DegradedFallback(t *testing.T) {
	log, buf := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return nil, errors.New("prometheus 500: internal error")
	}}

	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if !strings.HasPrefix(src, throttlerec.SourceDegradedPrefix) {
		t.Errorf("src = %q, want %q prefix", src, throttlerec.SourceDegradedPrefix)
	}
	if len(resp.Suggestions) != 0 {
		t.Errorf("Suggestions count = %d, want 0 on degraded", len(resp.Suggestions))
	}
	if !strings.Contains(buf.String(), "internal error") {
		t.Errorf("log buffer = %q, want it to contain %q", buf.String(), "internal error")
	}
}

// TestThrottleRec_NilClient: nil fetcher → "degraded: prometheus
// not configured". Both interface-nil and typed-nil *promql.Client
// fall into this branch.
func TestThrottleRec_NilClient(t *testing.T) {
	log, _ := captureLog(t)

	resp, src := throttlerec.Fetch(context.Background(), nil, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if src != throttlerec.SourceDegradedPrefix+"prometheus not configured" {
		t.Errorf("src = %q, want %q", src, throttlerec.SourceDegradedPrefix+"prometheus not configured")
	}
	if resp.Source != "" {
		// The response's Source field is what the HTTP handler
		// will emit — we want it empty in the degraded fallback
		// so the wire shape matches the empty-state branch.
		// (Source is the *return* value here; the response struct
		// field is the handler's job to fill.)
		t.Errorf("response.Source = %q, want empty", resp.Source)
	}
}

// TestThrottleRec_RouteMetricsDisabled: Free plan
// (RouteMetricsEnabled=false) → empty Suggestions +
// RouteMetricsDisabled=true + Source="prometheus" (not degraded).
// The customer's plan doesn't bill for this surface; the
// dashboard renders the upsell rather than a misleading zero.
func TestThrottleRec_RouteMetricsDisabled(t *testing.T) {
	log, _ := captureLog(t)
	// The stub is never called — the disabled flag short-circuits
	// before any PromQL query. We assert that by returning a
	// non-nil error from QueryGrouped: if Fetch called us, the
	// test would fail with the error path instead.
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return nil, errors.New("QueryGrouped should not be called when route_metrics_enabled=false")
	}}

	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanFree,
		RouteMetricsEnabled: false,
	})
	if src != throttlerec.SourcePrometheus {
		t.Errorf("src = %q, want %q", src, throttlerec.SourcePrometheus)
	}
	if !resp.RouteMetricsDisabled {
		t.Errorf("RouteMetricsDisabled = false, want true")
	}
	if len(resp.Suggestions) != 0 {
		t.Errorf("Suggestions count = %d, want 0 when disabled", len(resp.Suggestions))
	}
}

// TestThrottleRec_AppIDInjectionGuard: a malformed appID
// containing `"` is rejected as degraded. The PromQL injection
// surface is the outer label literal, so a `"` would close it
// and re-open a new `app=…` selector, leaking data across apps.
func TestThrottleRec_AppIDInjectionGuard(t *testing.T) {
	log, _ := captureLog(t)
	called := false
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		called = true
		return nil, nil
	}}

	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               `app"injected`,
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if called {
		t.Errorf("QueryGrouped was called for malicious appID")
	}
	if !strings.HasPrefix(src, throttlerec.SourceDegradedPrefix) {
		t.Errorf("src = %q, want %q prefix", src, throttlerec.SourceDegradedPrefix)
	}
	if !strings.Contains(src, "invalid app id") {
		t.Errorf("src = %q, want it to contain %q", src, "invalid app id")
	}
	if resp.AppID != `app"injected` {
		// The response carries the original appID through so
		// the handler can echo it on the wire — only the
		// Source degrades.
		t.Errorf("AppID = %q, want %q", resp.AppID, `app"injected`)
	}
}

// TestThrottleRec_UnknownPlan: an unknown plan is fail OPEN —
// Suggestions are produced with no ceiling applied. The apid
// sub-plan validator is the authoritative gate; the recommender
// is advisory.
func TestThrottleRec_UnknownPlan(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return appRow("app-1", map[string]float64{
			"GET /x": 50.0,
		}), nil
	}}

	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.Plan("unknown"),
		RouteMetricsEnabled: true,
	})
	if src != throttlerec.SourcePrometheus {
		t.Errorf("src = %q, want %q", src, throttlerec.SourcePrometheus)
	}
	if resp.PlanCeilingRPS != 0 || resp.PlanCeilingBurst != 0 {
		t.Errorf("PlanCeiling = (%d, %d), want (0, 0) for unknown plan", resp.PlanCeilingRPS, resp.PlanCeilingBurst)
	}
	if len(resp.Suggestions) != 1 {
		t.Fatalf("Suggestions count = %d, want 1", len(resp.Suggestions))
	}
	// 50 rps * 2 = 100; ceil(100) = 100; no ceiling clamp.
	if resp.Suggestions[0].SuggestedRPS != 100 {
		t.Errorf("SuggestedRPS = %v, want 100 (no ceiling on unknown plan)", resp.Suggestions[0].SuggestedRPS)
	}
}

// TestThrottleRec_DefaultRange: empty range falls back to the
// shared DefaultRange. The dashboard and the suggestions card
// render the same "current" window.
func TestThrottleRec_DefaultRange(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return appRow("app-1", map[string]float64{}), nil
	}}

	resp, _ := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if resp.Range != throttlerec.DefaultRange {
		t.Errorf("Range = %q, want %q", resp.Range, throttlerec.DefaultRange)
	}
}

// TestThrottleRec_InvalidRange: a range outside the closed
// vocabulary is rejected as degraded. The check is defensive —
// the HTTP handler validates first — but a malformed range
// downstream would surface as a malformed PromQL query.
func TestThrottleRec_InvalidRange(t *testing.T) {
	log, _ := captureLog(t)
	called := false
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		called = true
		return nil, nil
	}}

	_, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "10y",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if called {
		t.Errorf("QueryGrouped was called for invalid range")
	}
	if !strings.HasPrefix(src, throttlerec.SourceDegradedPrefix) {
		t.Errorf("src = %q, want %q prefix", src, throttlerec.SourceDegradedPrefix)
	}
	if !strings.Contains(src, "invalid range") {
		t.Errorf("src = %q, want it to contain %q", src, "invalid range")
	}
}

// TestThrottleRec_InvalidRange_DegradedZeroes: the InvalidRange
// branch returns a degraded response with PlanCeiling zeroed —
// degradedFromErr zeroes everything so the dashboard's
// empty-state branch handles it uniformly. The pre-PromQL cheap
// work doesn't survive the degraded path because the planning
// pull is folded into the same zeroed struct.
func TestThrottleRec_InvalidRange_DegradedZeroes(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return nil, nil
	}}

	resp, _ := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "10y",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if resp.PlanCeilingRPS != 0 {
		t.Errorf("PlanCeilingRPS = %d, want 0 (degraded path zeroes everything)", resp.PlanCeilingRPS)
	}
}

// TestThrottleRec_LogInjectionGuard: an err string with \r\n
// must NOT flow into the captured slog JSON as a raw newline.
// CodeQL go/log-injection (alert #117) requires the two-call
// strings.ReplaceAll pattern at the call site — see
// memory/codeql-go-log-injection-sanitisers.md for the precedent.
// The check is on the `err` field of the WARN entry, not on the
// whole JSON envelope (slog itself emits a trailing newline
// between records, which is the expected framing).
func TestThrottleRec_LogInjectionGuard(t *testing.T) {
	log, buf := captureLog(t)
	stub := &stubPromQL{fn: func(_, _, _ string) (map[string]map[string]float64, error) {
		return nil, fmt.Errorf("evil\r\nINJECTED")
	}}

	_, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
	})
	if strings.Contains(src, "\r") || strings.Contains(src, "\n") {
		t.Errorf("returned Source has CR/LF: %q", src)
	}
	// production-us hunt #4: query errors carry the internal Prometheus URL
	// and PromQL, so the response gets only the generic reason; the
	// sanitised detail stays in the server log checked below.
	if src != "degraded: telemetry unavailable" {
		t.Errorf("Source = %q, want the generic degraded reason", src)
	}
	if !strings.Contains(buf.String(), "evilINJECTED") {
		t.Errorf("log lost the sanitised query error: %s", buf.String())
	}
	// Walk the captured JSON envelope: extract the "err" field's
	// value and check it has no CR/LF. slog framing (newlines
	// between records) is expected; what's NOT expected is CR/LF
	// inside the user-controlled err value.
	logged := buf.String()
	if strings.Contains(logged, "evil\r\nINJECTED") {
		t.Errorf("captured log contains raw CR/LF in err value: %q", logged)
	}
	if !strings.Contains(logged, "evilINJECTED") {
		t.Errorf("captured log = %q, want it to contain %q", logged, "evilINJECTED")
	}
}

// TestThrottleRec_DryRunPreview_HappyPath — ADR-104 amendment 5
// (issue #881 Phase 4 D1): when DryRun=true + CandidateRPS=5 +
// CandidateBurst=10, the recommender runs the per-route
// would-have-rejected pass in addition to the recommendation
// pass. Each surviving route gets a ThrottlePreviewRow echoing
// the candidate rps and the per-route over-cap count (the
// number of sub-windows where observed rps exceeded the
// candidate). PerConsumerLimitNote carries the static literal
// that names the gateway_requests_by_route_total label gap.
func TestThrottleRec_DryRunPreview_HappyPath(t *testing.T) {
	log, _ := captureLog(t)
	// Recommendation pass returns 2 surviving routes ("GET /a" at
	// 4 rps, "POST /b" at 8 rps). Candidate=5 rps means route /a is
	// under cap (no sub-windows over 5), route /b has 3 sub-windows
	// over 5. We pin the values via scalarFn.
	stub := &stubPromQL{
		fn: func(_, _, _ string) (map[string]map[string]float64, error) {
			return appRow("app-1", map[string]float64{
				"GET /a":  4.0,
				"POST /b": 8.0,
			}), nil
		},
		scalarFn: func(query string) (float64, error) {
			// Map per-route to its pinned over-cap count. The
			// query embeds the route label, which is enough to
			// disambiguate.
			if strings.Contains(query, `route="GET /a"`) {
				return 0, nil
			}
			if strings.Contains(query, `route="POST /b"`) {
				return 3, nil
			}
			return 0, fmt.Errorf("unexpected query: %s", query)
		},
	}
	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
		DryRun:              true,
		CandidateRPS:        5,
		CandidateBurst:      10,
		NowFn:               func() string { return "2026-08-18T12:00:00Z" },
	})
	if src != throttlerec.SourcePrometheus {
		t.Fatalf("src = %q, want %q", src, throttlerec.SourcePrometheus)
	}
	if !resp.DryRun {
		t.Errorf("DryRun = false, want true")
	}
	if resp.CandidateRPS != 5 || resp.CandidateBurst != 10 {
		t.Errorf("CandidateRPS=%v CandidateBurst=%d, want 5/10", resp.CandidateRPS, resp.CandidateBurst)
	}
	if got := resp.PerConsumerLimitNote; got != throttlerec.PerConsumerLimitNoteText {
		t.Errorf("PerConsumerLimitNote = %q, want %q", got, throttlerec.PerConsumerLimitNoteText)
	}
	if got := len(resp.WouldHaveRejected); got != 2 {
		t.Fatalf("WouldHaveRejected count = %d, want 2", got)
	}
	byRoute := map[string]api.ThrottlePreviewRow{}
	for _, p := range resp.WouldHaveRejected {
		byRoute[p.Route] = p
	}
	if p := byRoute["GET /a"]; p.OverCapCount != 0 || p.CandidateRPS != 5 {
		t.Errorf("GET /a preview = %+v, want OverCapCount=0 CandidateRPS=5", p)
	}
	if p := byRoute["POST /b"]; p.OverCapCount != 3 || p.CandidateRPS != 5 {
		t.Errorf("POST /b preview = %+v, want OverCapCount=3 CandidateRPS=5", p)
	}
	if p := byRoute["GET /a"]; p.WindowStart != "2026-08-18T12:00:00Z" || p.WindowEnd != "2026-08-18T12:00:00Z" {
		t.Errorf("GET /a window start/end = %q/%q, want 2026-08-18T12:00:00Z/2026-08-18T12:00:00Z (NowFn pin)",
			p.WindowStart, p.WindowEnd)
	}
	// Recommendation slice must remain populated AND unchanged
	// (dry-run is additive, never destructive).
	if got := len(resp.Suggestions); got != 2 {
		t.Errorf("Suggestions count = %d, want 2 (dry-run must not shrink recommendation)", got)
	}
}

// TestThrottleRec_DryRunPreview_DisabledWhenCandidateZero — the
// dry-run pass must short-circuit when CandidateRPS <= 0 (a
// missing candidate is a wire-shape bug, not "preview with no
// threshold"). The recommendation slice is still returned.
func TestThrottleRec_DryRunPreview_DisabledWhenCandidateZero(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{
		fn: func(_, _, _ string) (map[string]map[string]float64, error) {
			return appRow("app-1", map[string]float64{"GET /a": 4.0}), nil
		},
		scalarFn: func(_ string) (float64, error) {
			t.Errorf("QueryScalar must NOT be called when CandidateRPS<=0 (dry-run disabled)")
			return 0, nil
		},
	}
	resp, src := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
		DryRun:              true,
		CandidateRPS:        0, // missing → no preview
	})
	if src != throttlerec.SourcePrometheus {
		t.Errorf("src = %q, want prometheus", src)
	}
	if resp.DryRun {
		t.Errorf("DryRun = true, want false (CandidateRPS=0 should disable the preview)")
	}
	if len(resp.WouldHaveRejected) != 0 {
		t.Errorf("WouldHaveRejected = %+v, want empty", resp.WouldHaveRejected)
	}
}

// TestThrottleRec_DryRunPreview_OffByDefault — when DryRun=false
// (the default for Phase 1+2+3 callers), the QueryScalar path is
// NEVER taken. This pins the back-compat invariant: Phase 4 D1
// must not change the wire shape for callers that don't opt in.
func TestThrottleRec_DryRunPreview_OffByDefault(t *testing.T) {
	log, _ := captureLog(t)
	stub := &stubPromQL{
		fn: func(_, _, _ string) (map[string]map[string]float64, error) {
			return appRow("app-1", map[string]float64{"GET /a": 4.0}), nil
		},
		scalarFn: func(_ string) (float64, error) {
			t.Errorf("QueryScalar must NOT be called when DryRun=false (back-compat)")
			return 0, nil
		},
	}
	resp, _ := throttlerec.Fetch(context.Background(), stub, log, throttlerec.FetchOptions{
		AppID:               "app-1",
		Range:               "5m",
		Plan:                api.PlanPro,
		RouteMetricsEnabled: true,
		// DryRun=false (default)
	})
	if resp.DryRun {
		t.Errorf("DryRun = true, want false (off-by-default)")
	}
	if len(resp.WouldHaveRejected) != 0 {
		t.Errorf("WouldHaveRejected = %+v, want empty (off-by-default)", resp.WouldHaveRejected)
	}
}
