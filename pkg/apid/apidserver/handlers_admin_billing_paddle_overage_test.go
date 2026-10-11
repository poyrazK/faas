// handlers_admin_billing_paddle_overage_test.go — pins the contract
// of the B4 pre-flight handler added in the Tier 1 follow-up to
// PR #802.
//
// What this file pins:
//
//	1. The route is admin-only + admin-allowlist gated (same two-
//	   layer pattern as handlers_admin_billing_test.go).
//	2. On a fresh MemStore with no Paddle-overage rows, the handler
//	   returns 200 with table_exists=false and all four has_X=false.
//	   This is the "you forgot to apply migration 00041" tripwire
//	   the CLI subcommand maps to an actionable error.
//	3. After ClaimPaddleOverageWindow + CompletePaddleOverageWindow
//	   seed rows, the response reports table_exists=true, all four
//	   has_X=true, and the per-state counts the meterd loop sees.
//	4. The handler does NOT require a billingProvider — unlike the
//	   reconcile endpoint it queries the store directly, so the
//	   Stripe-vs-Paddle capability gate does not apply. An operator
//	   on a Stripe deployment can still run the pre-flight against
//	   a DB that has stale paddle_overage_dedupe rows from a prior
//	   Paddle deployment.

package apidserver

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// newPreflightEnv wires a minimal testEnv: store + ops metrics +
// admin allowlist + bearer with admin scope. Mirrors newReconcileEnv
// but skips billingProvider entirely — the pre-flight never reads
// from it.
func newPreflightEnv(t *testing.T, scopes []string, adminEmail, callerEmail string) testEnv {
	t.Helper()
	store := state.NewMemStore()
	ops := wire.NewOpsMetrics("apid_preflight_test")
	acct, err := store.CreateAccount(context.Background(), callerEmail, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateAPIKey(context.Background(), acct.ID, hash, "preflight-test", scopes); err != nil {
		t.Fatal(err)
	}
	srv := newServer(store, slog.New(slog.NewTextHandler(io.Discard, nil)), "gregale.dev", noopNotifier{}).WithOpsMetrics(context.Background(), ops)
	srv.WithAdminAllowlist(adminEmail)
	return testEnv{h: srv.handler(), store: store, key: pt, acct: acct, ops: ops}
}

// TestPaddleOveragePreflight_FreshMemStoreReportsTableMissing is
// the tripwire path: a box that has never applied migration 00034
// (let alone 00041) must surface table_exists=false so the CLI
// emits "apply migrations 00034 + 00041". A silent 200 with
// table_exists=true but all HasX=false would still be actionable,
// but the missing-table case has a different remediation order.
func TestPaddleOveragePreflight_FreshMemStoreReportsTableMissing(t *testing.T) {
	e := newPreflightEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/billing-paddle-overage/preflight", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var resp api.BillingPaddleOveragePreflightResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body)
	}
	if resp.TableExists {
		t.Errorf("table_exists = true on fresh MemStore, want false")
	}
	if resp.HasWindowStart || resp.HasState || resp.HasClaimedAt || resp.HasClaimedBy {
		t.Errorf("any HasX = true on fresh MemStore, want all false; got %+v", resp)
	}
	if resp.PendingRows != 0 || resp.CompletedRows != 0 {
		t.Errorf("counts on fresh MemStore must be 0; got pending=%d completed=%d", resp.PendingRows, resp.CompletedRows)
	}
}

// TestPaddleOveragePreflight_WithRowsReportsShape pins the
// happy-path: after the MemStore's paddleOverageWindows map has
// both pending and completed rows, all four HasX bools report true
// and the per-state counts match what was seeded.
func TestPaddleOveragePreflight_WithRowsReportsShape(t *testing.T) {
	e := newPreflightEnv(t, api.ScopesAdminOnly, "ops@example.com", "ops@example.com")
	ctx := context.Background()

	// Seed: two pending windows + one completed window. Use unique
	// (account, window) tuples so the test is reproducible across
	// CI shards that share a state schema. A 1-hour lease is enough
	// to keep the second claim from racing the first (the test never
	// sleeps so a row stays pending for the entire test).
	now := time.Now().UTC().Truncate(time.Hour)
	lease := time.Hour
	if ok, err := e.store.ClaimPaddleOverageWindow(ctx, "acct-A", now, "pod-1", lease); err != nil || !ok {
		t.Fatalf("seed claim A: ok=%v err=%v", ok, err)
	}
	if ok, err := e.store.ClaimPaddleOverageWindow(ctx, "acct-A", now.Add(time.Hour), "pod-1", lease); err != nil || !ok {
		t.Fatalf("seed claim B: ok=%v err=%v", ok, err)
	}
	if ok, err := e.store.ClaimPaddleOverageWindow(ctx, "acct-B", now.Add(2*time.Hour), "pod-2", lease); err != nil || !ok {
		t.Fatalf("seed claim C: ok=%v err=%v", ok, err)
	}
	if err := e.store.CompletePaddleOverageWindow(ctx, "acct-B", now.Add(2*time.Hour), 100); err != nil {
		t.Fatalf("seed complete C: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/billing-paddle-overage/preflight", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var resp api.BillingPaddleOveragePreflightResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v body=%s", err, rec.Body)
	}
	if !resp.TableExists {
		t.Errorf("table_exists = false after seeding rows, want true")
	}
	if !resp.HasWindowStart || !resp.HasState || !resp.HasClaimedAt || !resp.HasClaimedBy {
		t.Errorf("all HasX must be true after seeding; got %+v", resp)
	}
	if resp.PendingRows != 2 {
		t.Errorf("pending_rows = %d, want 2 (acct-A@now, acct-A@now+1h)", resp.PendingRows)
	}
	if resp.CompletedRows != 1 {
		t.Errorf("completed_rows = %d, want 1 (acct-B@now+2h)", resp.CompletedRows)
	}
}

// TestPaddleOveragePreflight_NonAdminGets403 pins the admin
// allowlist gate: the route is admin-scoped, so a caller without
// admin scope is rejected before reaching the handler. Mirrors
// the auth pattern at handlers_admin_billing_test.go:96-142.
func TestPaddleOveragePreflight_NonAdminGets403(t *testing.T) {
	// Mint a non-admin bearer (only the apps:read scope, NOT admin).
	// The requireScope(api.ScopesAdminOnly...) middleware will reject
	// before the admin-allowlist check runs. Note ScopesReadSurface
	// intentionally bundles admin + apps:read, so a single-scope
	// apps:read bearer is what exercises the rejection path.
	e := newPreflightEnv(t, []string{api.ScopeAppsRead}, "ops@example.com", "ops@example.com")

	req := httptest.NewRequest(http.MethodGet, "/v1/admin/billing-paddle-overage/preflight", nil)
	req.Header.Set("Authorization", "Bearer "+e.key)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "admin_required") && !strings.Contains(rec.Body.String(), "insufficient_scope") {
		t.Errorf("body should mention scope/admin failure; got %s", rec.Body.String())
	}
}
