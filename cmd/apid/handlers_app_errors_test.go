package main

// Per-app errors-summary gate + retention-clamp tests (commit 5 of
// the per-app observability PR series).
//
// Coverage matrix:
//   - Free plan → 402 + code assertion
//   - Hobby plan → 200 with documented wire shape
//   - Per-plan retention clamp: Hobby's 7d cap fires when the
//     customer asks for a 30d window; WindowClamped=true lands
//     on the wire so the dashboard can render the upsell/limit
//     tile.
//   - Plan-downgrade mid-session: Hobby → Free flips 200 → 402
//     on the next poll (no stale plan cache).
//
// Hobby+ integration tests wrap *state.MemStore with a shadow
// type that overrides ListAppErrorGroups to return the empty
// slice. The MemStore stub at memstore_app_errors.go returns the
// sentinel error (Postgres-only path); the shadow lets the
// handler exercise the gate + retention-clamp path without
// standing up pgtest.

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TestAppErrorsSummary_FreePlanReturns402 confirms the Free plan
// is rejected with the documented plan_app_errors_not_allowed
// code. Same gate posture as /metrics, /wake-timeline, and /usage.
func TestAppErrorsSummary_FreePlanReturns402(t *testing.T) {
	e := setup(t, api.PlanFree)
	mustSeedApp(t, e, "my-api")

	rec := e.do(t, "GET", "/v1/apps/my-api/errors/summary", nil, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("Free plan status %d, want 402", rec.Code)
	}
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanAppErrorsNotAllowed)
}

// TestAppErrorsSummary_HobbyPlanReturns200 confirms Hobby+ passes
// the gate. Empty fleet → 200 with items=[] + window_clamped=false
// + documented wire shape (AppErrorsSummaryResponse). The empty
// list is NOT a 404 — the dashboard renders "no errors in window"
// rather than a missing-app chip.
func TestAppErrorsSummary_HobbyPlanReturns200(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "my-api")
	swapStoreForAppErrors(t, &e)

	rec := e.do(t, "GET", "/v1/apps/my-api/errors/summary", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Hobby plan status %d: %s", rec.Code, rec.Body.String())
	}
	var out api.AppErrorsSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.AppSlug != "my-api" {
		t.Errorf("app_slug = %q, want my-api", out.AppSlug)
	}
	if out.WindowClamped {
		t.Errorf("window_clamped = true on default 24h window (under the Hobby 7d cap)")
	}
	if len(out.Items) != 0 {
		t.Errorf("items len = %d, want 0 on empty fleet", len(out.Items))
	}
	if out.NextCursor != "" {
		t.Errorf("next_cursor = %q, want empty on no items", out.NextCursor)
	}
	if out.GeneratedAt == "" {
		t.Errorf("generated_at empty")
	}
}

// TestAppErrorsSummary_RetentionClampOnHobby pins the per-plan
// retention clamp (ADR-096): Hobby's 7d cap must fire when the
// customer asks for a 30d window. The clamp sets WindowClamped=true
// on the wire so the dashboard can render the "you widened past
// the cap" tile. Without the clamp, a Free-upgraded-to-Hobby
// customer could query 90d of pre-Hobby history that the
// retention cron has NOT purged yet.
func TestAppErrorsSummary_RetentionClampOnHobby(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "my-api")
	swapStoreForAppErrors(t, &e)

	// 30d window — well past Hobby's 7d cap.
	now := time.Now().UTC()
	until := now.Format(time.RFC3339Nano)
	since := now.AddDate(0, 0, -30).Format(time.RFC3339Nano)

	rec := e.do(t, "GET", "/v1/apps/my-api/errors/summary?since="+since+"&until="+until, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out api.AppErrorsSummaryResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.WindowClamped {
		t.Errorf("window_clamped = false, want true (Hobby's 7d cap must fire on a 30d request)")
	}
	// The clamped window must be ~7d, not 30d. Parse the wire
	// stamps and assert the span is in the [6d, 8d] band (the
	// handler rounds at day granularity via the per-plan field).
	winStart, err := time.Parse(time.RFC3339Nano, out.WindowStart)
	if err != nil {
		t.Fatalf("parse window_start %q: %v", out.WindowStart, err)
	}
	winEnd, err := time.Parse(time.RFC3339Nano, out.WindowEnd)
	if err != nil {
		t.Fatalf("parse window_end %q: %v", out.WindowEnd, err)
	}
	span := winEnd.Sub(winStart)
	if span < 6*24*time.Hour || span > 8*24*time.Hour {
		t.Errorf("clamped window span = %v, want ~7d (Hobby's AppErrorsRetentionDays=7)", span)
	}
}

// TestAppErrorsSummary_PlanDowngradeBounces pins the no-stale-
// plan-cache contract: a downgrade between two consecutive polls
// flips 200 → 402 on the very next request without a session
// refresh. The dashboard relies on this to render its
// upsell_to_resume chip.
func TestAppErrorsSummary_PlanDowngradeBounces(t *testing.T) {
	e := setup(t, api.PlanPro)
	mustSeedApp(t, e, "my-api")
	swapStoreForAppErrors(t, &e)

	// Baseline: Pro passes the gate.
	rec := e.do(t, "GET", "/v1/apps/my-api/errors/summary", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("Pro baseline status %d, want 200", rec.Code)
	}

	// Downgrade Pro → Free in place. The middleware loads the
	// account fresh on every request, so the next poll must
	// observe the new plan without any session refresh.
	if err := e.store.UpdateAccountPlan(context.Background(), e.acct.ID, api.PlanFree); err != nil {
		t.Fatalf("UpdateAccountPlan: %v", err)
	}

	rec = e.do(t, "GET", "/v1/apps/my-api/errors/summary", nil, nil)
	if rec.Code != http.StatusPaymentRequired {
		t.Fatalf("post-downgrade status %d, want 402 (no stale plan cache)", rec.Code)
	}
	assertProblem(t, rec, http.StatusPaymentRequired, api.CodePlanAppErrorsNotAllowed)
}

// appErrorsListStore wraps *state.MemStore and overrides
// ListAppErrorGroups to return the empty slice. Method-shadowing
// via embedding: the outer method wins over the MemStore sentinel
// stub at pkg/state/memstore_app_errors.go:41. Mirrors the
// wakeBootStartedStore wrapper from commit 2's tests.
type appErrorsListStore struct {
	*state.MemStore
}

func (appErrorsListStore) ListAppErrorGroups(_ context.Context, _ sqlc.ListAppErrorGroupsParams) ([]state.AppErrorGroup, error) {
	return nil, nil
}

func (appErrorsListStore) ListAppErrorRequests(_ context.Context, _ sqlc.ListAppErrorRequestsParams) ([]state.AppErrorRequestRow, error) {
	return nil, nil
}

// swapStoreForAppErrors rebuilds the server in e with a shadow
// store wrapper that returns the empty slice from
// ListAppErrorGroups. e.store stays the original MemStore so the
// auth middleware (which reads e.store directly via s.store for
// loadApp) sees the live account row across the in-place plan
// downgrade.
func swapStoreForAppErrors(t *testing.T, e *testEnv) {
	t.Helper()
	wrapped := appErrorsListStore{MemStore: e.store}
	srv := newServer(wrapped, slog.New(slog.NewTextHandler(io.Discard, nil)),
		"gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), e.ops)
	e.h = srv.handler()
	e.s = srv
}

// TestAppErrorRequests_CursorPastLastRowIsEmptyNot404 — next_cursor is
// emitted whenever a drill-down page is full, so for a fingerprint with an
// exact multiple of the page size the follow-up request finds no rows. That
// answered 404 "fingerprint not found", which made ListAppErrorRequestsAll
// fail at the end of every such walk. Without a cursor, an unknown
// fingerprint is still a 404.
func TestAppErrorRequests_CursorPastLastRowIsEmptyNot404(t *testing.T) {
	e := setup(t, api.PlanHobby)
	mustSeedApp(t, e, "my-api")
	swapStoreForAppErrors(t, &e)
	fingerprint := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

	rec := e.do(t, "GET", "/v1/apps/my-api/errors/"+fingerprint, nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("no cursor: status %d, want 404: %s", rec.Code, rec.Body.String())
	}

	cursor := encodeErrorsCursor(errorsCursorShape{
		ReceivedAt: time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC).Format(time.RFC3339Nano),
		RequestID:  "01995a4e-8f20-7d8c-b9e1-2e9d2bd1d4f0",
	})
	rec = e.do(t, "GET", "/v1/apps/my-api/errors/"+fingerprint+"?cursor="+cursor, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("cursor past the last row: status %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out api.AppErrorRequestsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Requests == nil || len(out.Requests) != 0 || out.NextCursor != "" {
		t.Fatalf("response = %+v, want an empty final page", out)
	}
}
