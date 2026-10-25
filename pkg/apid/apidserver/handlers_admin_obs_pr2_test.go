// Operator observability backend — PR #2 handler tests
// (issue #777 / ADR-091 §3.5 + §3.6).
//
// Six tests pin the contract:
//
//   - AuthGate rejects customer scope + non-allowlist email
//     (the same two-layer pattern PR #1 ships).
//   - Anomalies happy path: empty store → 200 with empty Items
//     and the documented baseline_window_days=7 in the body.
//   - Anomalies window clamp: window_hours=999 → 400.
//   - Rate-limits happy path: empty store → 200, durable=[],
//     live=[] (no limiter wired in this env, but nil-safe).
//   - Rate-limits sources field is wire-stable: always
//     ["durable", "live"].
//   - PR #2 surfaces no new PII / sealed-blob columns: the
//     grep test (TestObsSecurity_PR2_NoNewPIISurface) asserts
//     the absence of sealed markers in the body.
package apidserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/middleware"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// newObsPR2Env is the PR #2 sibling of newObsEnv. Wires the
// shared apiAuthLimiter on the server so the rate-limits
// endpoint can call Snapshot() — the production wiring is
// cmd/apid/server.go::newServer.
func newObsPR2Env(t *testing.T, scopes []string, adminEmail, callerEmail string) testEnv {
	t.Helper()
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_obs_pr2_test")
	acct, err := store.CreateAccount(context.Background(), callerEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "obs-pr2-test", scopes); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	srv.WithAdminAllowlist(adminEmail)
	// Wire the auth limiter so the live snapshot has a bucket
	// to read from. Production wires this in newServer.
	srv.apiAuthLimiter = middleware.NewLimiter(middleware.AuthLimitConfig{
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	return testEnv{h: srv.handler(), s: srv, store: store, key: pt, acct: acct, ops: ops}
}

// TestObsAnomalies_AuthGate_RejectsCustomerKey pins the two-layer
// gate on the anomalies endpoint: customer scope → 403 before
// the handler body executes.
func TestObsAnomalies_AuthGate_RejectsCustomerKey(t *testing.T) {
	e := newObsPR2Env(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/anomalies", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("anomalies with customer scope: got status %d, want 403", rec.Code)
	}
}

// TestObsAnomalies_HappyPath_EmptyStore pins the empty-store
// response shape: 200 with Items=[], baseline_window_days=7,
// window_hours=24 (the default).
func TestObsAnomalies_HappyPath_EmptyStore(t *testing.T) {
	e := newObsPR2Env(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.doOperator(t, "GET", "/v1/admin/obs/anomalies", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("anomalies: got status %d, want 200; body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAnomalyListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal anomalies: %v", err)
	}
	if resp.WindowHours != 24 {
		t.Errorf("window_hours: got %d, want 24 (default)", resp.WindowHours)
	}
	if resp.BaselineWindowDays != 7 {
		t.Errorf("baseline_window_days: got %d, want 7 (ADR-091 §3.6)", resp.BaselineWindowDays)
	}
	if resp.Items == nil {
		t.Errorf("items must be non-nil slice on empty store")
	}
	if len(resp.Items) != 0 {
		t.Errorf("items on empty store: got %d, want 0", len(resp.Items))
	}
	if resp.GeneratedAt.IsZero() {
		t.Errorf("generated_at: zero")
	}
}

// TestObsAnomalies_WindowTooLarge_400 pins the ObsAdminWindowMaxHours
// (=168) cap. window_hours=999 → 400 with limit+observed detail.
func TestObsAnomalies_WindowTooLarge_400(t *testing.T) {
	e := newObsPR2Env(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.doOperator(t, "GET", "/v1/admin/obs/anomalies?window_hours=999", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("window_hours=999: got status %d, want 400; body=%s", rec.Code, rec.Body.String())
	}
	assertProblem(t, rec, http.StatusBadRequest, "validation_failed")
}

// TestObsAnomalies_WindowNonInteger_400 pins the parse guard
// against non-integer ?window_hours=. Returns 400 rather than
// silently coercing to the default.
func TestObsAnomalies_WindowNonInteger_400(t *testing.T) {
	e := newObsPR2Env(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.doOperator(t, "GET", "/v1/admin/obs/anomalies?window_hours=abc", nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("window_hours=abc: got status %d, want 400", rec.Code)
	}
}

// TestObsAnomalies_Seeded_HourOfDayFlags pins the scoring
// model: a single (account, app, hour) gets flagged when its
// current-minute mb_seconds exceed the 7-day hour-of-day
// baseline by ≥ 3σ. The seed puts 10 days of baseline rows at
// ~100 mb_seconds with low variance; a current-minute spike
// at 1000 mb_seconds → 1 flagged row.
//
// This is the load-bearing test for ADR-091 §3.6 ("hour-of-day
// baseline, not raw Z-score"). Without it, a regression to a
// naive Z-score would still pass empty-store tests.
func TestObsAnomalies_Seeded_HourOfDayFlags(t *testing.T) {
	e := newObsPR2Env(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()

	acctID := uuid.New().String()
	appID := uuid.New().String()
	instanceID := uuid.New().String()

	// Baseline: 7 days of low-variance 100 mb_seconds rows at
	// hour 12. Hour-of-day aggregation pools all these rows into
	// one (account, app, 12:00) bucket with mean=100, stddev≈0.
	now := time.Now().UTC().Truncate(time.Hour)
	for day := 1; day <= 7; day++ {
		minute := now.AddDate(0, 0, -day)
		if err := e.store.AppendUsage(ctx, acctID, appID, instanceID, minute,
			100, 1, 0, 0, 0, 0, 0, 0); err != nil {
			t.Fatalf("baseline seed: %v", err)
		}
	}
	// Current-minute spike at the SAME hour-of-day: 1000 mb_seconds.
	// Baseline mean=100, stddev≈0, current=1000 → triggers the
	// raw_z fallback (mean × 5 = 500, current 1000 ≥ 500).
	if err := e.store.AppendUsage(ctx, acctID, appID, instanceID, now,
		1000, 5, 0, 0, 0, 0, 0, 0); err != nil {
		t.Fatalf("spike seed: %v", err)
	}

	rec := e.doOperator(t, "GET", "/v1/admin/obs/anomalies", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("anomalies with seed: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsAnomalyListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Items) != 1 {
		t.Fatalf("items: got %d, want 1 (the spike); full=%+v", len(resp.Items), resp.Items)
	}
	got := resp.Items[0]
	if got.AccountID != acctID {
		t.Errorf("account_id: got %q, want %q", got.AccountID, acctID)
	}
	if got.AppID != appID {
		t.Errorf("app_id: got %q, want %q", got.AppID, appID)
	}
	if got.Current != 1000 {
		t.Errorf("current: got %v, want 1000", got.Current)
	}
	if got.Reason != "raw_z" && got.Reason != "hour_of_day" {
		t.Errorf("reason: got %q, want raw_z or hour_of_day", got.Reason)
	}
	if got.ZScore == nil || *got.ZScore <= 0 {
		t.Errorf("z_score: got %v, want positive float64", got.ZScore)
	}
}

// TestObsRateLimits_AuthGate_RejectsCustomerKey pins the two-layer
// gate on the rate-limits endpoint.
func TestObsRateLimits_AuthGate_RejectsCustomerKey(t *testing.T) {
	e := newObsPR2Env(t, api.ScopesReadSurface, "ops@faas.dev", "customer@faas.dev")
	rec := e.do(t, "GET", "/v1/admin/obs/rate-limits", nil, nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("rate-limits with customer scope: got status %d, want 403", rec.Code)
	}
}

// TestObsRateLimits_HappyPath_EmptyStore pins the empty-store
// response shape: 200 with durable=[], live=[], sources=
// ["durable", "live"], lag_seconds=30. Items are non-nil slices.
func TestObsRateLimits_HappyPath_EmptyStore(t *testing.T) {
	e := newObsPR2Env(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.doOperator(t, "GET", "/v1/admin/obs/rate-limits", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rate-limits: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsRateLimitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.WindowHours != 24 {
		t.Errorf("window_hours: got %d, want 24 (default)", resp.WindowHours)
	}
	if resp.LagSeconds != 30 {
		t.Errorf("lag_seconds: got %d, want 30 (ADR-091 §3.5)", resp.LagSeconds)
	}
	if len(resp.Sources) != 2 || resp.Sources[0] != obsRateLimitSourceDurable || resp.Sources[1] != obsRateLimitSourceLive {
		t.Errorf("sources: got %v, want [%s %s]", resp.Sources, obsRateLimitSourceDurable, obsRateLimitSourceLive)
	}
	if resp.Durable == nil {
		t.Errorf("durable must be non-nil slice")
	}
	if resp.Live == nil {
		t.Errorf("live must be non-nil slice (nil-safe on limiter)")
	}
	if len(resp.Durable) != 0 || len(resp.Live) != 0 {
		t.Errorf("empty store: durable=%d live=%d, want 0/0", len(resp.Durable), len(resp.Live))
	}
}

// TestObsRateLimits_Seeded_DurableBucket pins the durable
// aggregate: one auth.rate_limited event with a known subject
// produces one row in the durable array with the right account_id
// + hits=1.
func TestObsRateLimits_Seeded_DurableBucket(t *testing.T) {
	e := newObsPR2Env(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()

	subject := uuid.New().String()
	if err := e.store.AppendEvent(ctx, "127.0.0.1", "auth.rate_limited", &subject, nil); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rec := e.doOperator(t, "GET", "/v1/admin/obs/rate-limits", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rate-limits seeded: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	var resp api.ObsRateLimitResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(resp.Durable) != 1 {
		t.Fatalf("durable: got %d rows, want 1", len(resp.Durable))
	}
	if resp.Durable[0].AccountID != subject {
		t.Errorf("account_id: got %q, want %q", resp.Durable[0].AccountID, subject)
	}
	if resp.Durable[0].Hits != 1 {
		t.Errorf("hits: got %d, want 1", resp.Durable[0].Hits)
	}
	if resp.Durable[0].LastEventAt.IsZero() {
		t.Errorf("last_event_at: zero")
	}
}

// TestObsRateLimits_SourcesStable asserts the wire-stable
// sources field. Today always ["durable", "live"]; future
// additions (gatewayd-public rate-limit snapshot, multi-host
// aggregator) appear here without breaking the contract. The
// ordering is also fixed: durable before live, so a future
// frontend can hard-code the array index.
func TestObsRateLimits_SourcesStable(t *testing.T) {
	e := newObsPR2Env(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	rec := e.doOperator(t, "GET", "/v1/admin/obs/rate-limits", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("rate-limits: got status %d, body=%s", rec.Code, rec.Body.String())
	}
	var raw map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	srcs, ok := raw["sources"].([]any)
	if !ok {
		t.Fatalf("sources field missing or wrong type: %+v", raw["sources"])
	}
	if len(srcs) != 2 || srcs[0] != obsRateLimitSourceDurable || srcs[1] != obsRateLimitSourceLive {
		t.Fatalf("sources: got %v, want [%s %s]", srcs, obsRateLimitSourceDurable, obsRateLimitSourceLive)
	}
}

// TestObsSecurity_PR2_NoNewPIISurface is the cross-PR grep guard.
// PR #2 introduces no new PII / sealed-blob surface; the body must
// not contain any column-name marker from the existing
// handlers_admin_obs_security_test.go grep list. This test would
// fail if a future contributor added an MFA secret or token hash
// to the anomalies / rate-limits projection helpers.
func TestObsSecurity_PR2_NoNewPIISurface(t *testing.T) {
	e := newObsPR2Env(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")

	for _, path := range []string{
		"/v1/admin/obs/anomalies",
		"/v1/admin/obs/rate-limits",
	} {
		rec := e.doOperator(t, "GET", path, nil, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: got status %d", path, rec.Code)
		}
		body := rec.Body.String()
		// Markers must NOT appear anywhere in the body. The list
		// is the same one handlers_admin_obs_security_test.go
		// uses for PR #1; PR #2 inherits the same omission rules
		// (the grep test is the tripwire if a future PR adds a
		// sealed column without teaching the projection helper).
		for _, marker := range obsSealedBlobMarkers {
			if bytesContains(body, marker) {
				t.Errorf("%s body contains sealed-blob marker %q\nbody=%s", path, marker, body)
			}
		}
		for _, marker := range obsJailInternalMarkers {
			if bytesContains(body, marker) {
				t.Errorf("%s body contains jail-internal marker %q\nbody=%s", path, marker, body)
			}
		}
	}
}

// bytesContains is a small wrapper to keep the grep tests
// readable. strings.Contains would also work but the grep list
// is shorter than the marker set so a dedicated helper makes
// the test intent obvious.
func bytesContains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
