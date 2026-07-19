// account_e2e_test.go — G6 GDPR self-service acceptance (spec §17 G6,
// ADR-021). Boots apid with FAAS_GRACE_INTERVAL set to a few hundred
// ms so the 30-day "grace expired" tick fires within the test
// deadline, then drives the full customer journey end-to-end:
//
//   - TestE2E_Export_FullBundle — happy-path export, slice assertions
//   - TestE2E_Delete_ExportDuringGrace — proves the D7 carve-out
//     (export still reachable while deleted_pending)
//   - TestE2E_GraceExpiry_HardDelete — UPDATE accounts SET
//     deletion_requested_at=now()-interval '31 days' + wait for
//     grace tick + GET /v1/account/export → 401
//
// All three boot a dedicated apid so the grace-interval env var
// doesn't bleed across tests.
//
// Build tag: (none). CI-safe. Requires Postgres (skip via
// FAAS_SKIP_PG_TESTS) and a buildable ./cmd/apid.

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// seedEmail returns the deterministic email SeedAccount uses for a
// (plan, label) tuple. Each subtest seeds exactly one account on its
// own pgtest schema, so the UPDATE in the grace-expired test can
// WHERE on email directly without needing a separate ID-lookup helper.
func seedEmail(plan api.Plan, label string) string {
	email := "e2e+" + string(plan)
	if label != "" {
		email += "+" + label
	}
	return email + "@test.example"
}

// startAPIDWithGraceInterval is a tiny helper that wraps StartWithEnv
// with FAAS_GRACE_INTERVAL pre-set. The default 60s would push the
// "grace expired" e2e over the test deadline.
func startAPIDWithGraceInterval(t *testing.T, pool *pgxpool.Pool) *e2etest.Harness {
	t.Helper()
	return e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_GRACE_INTERVAL=300ms",
	})
}

// TestE2E_Export_FullBundle — happy-path GDPR export. We seed an app
// via the apid API and assert the bundle contains the slice the
// customer expects.
func TestE2E_Export_FullBundle(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := startAPIDWithGraceInterval(t, pool)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// Seed one app so the bundle has something to render.
	createRec, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "g6-app"})
	if status != http.StatusCreated {
		t.Fatalf("create app: %d %s", status, createRec)
	}

	raw, status := doReq(t, h, key, http.MethodGet, "/v1/account/export", nil)
	if status != http.StatusOK {
		t.Fatalf("export: %d %s", status, raw)
	}
	var bundle api.AccountExportResponse
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("decode bundle: %v (body=%s)", err, raw)
	}
	if bundle.Account.Email == "" {
		t.Errorf("account.email empty in bundle")
	}
	if len(bundle.Apps) != 1 || bundle.Apps[0].Slug != "g6-app" {
		t.Errorf("apps = %+v, want one app 'g6-app'", bundle.Apps)
	}
}

// TestE2E_Delete_ExportDuringGrace — DELETE schedules the account;
// GET /v1/account/export must STILL return 200 during the grace
// window. This is the load-bearing D7 carve-out (spec §17 G6 D7):
// every other /v1/* path is gated, but export + restore stay
// reachable so the customer can take a final dump or cancel.
func TestE2E_Delete_ExportDuringGrace(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := startAPIDWithGraceInterval(t, pool)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// Schedule deletion.
	if body, status := doReq(t, h, key, http.MethodDelete, "/v1/account", nil); status != http.StatusOK {
		t.Fatalf("delete: %d %s", status, body)
	}

	// Export during grace — must still 200.
	raw, status := doReq(t, h, key, http.MethodGet, "/v1/account/export", nil)
	if status != http.StatusOK {
		t.Fatalf("export during grace: %d %s", status, raw)
	}
	var bundle api.AccountExportResponse
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("decode bundle: %v", err)
	}
	if bundle.Account.Status != string("deleted_pending") {
		t.Errorf("account.status = %q, want deleted_pending", bundle.Account.Status)
	}

	// Non-account path during grace — must 402.
	if body, status := doReq(t, h, key, http.MethodGet, "/v1/apps", nil); status != http.StatusPaymentRequired {
		t.Errorf("non-account path during grace: %d, want 402 %s", status, body)
	}
}

// TestE2E_GraceExpiry_HardDelete — fast-forward the deletion
// timestamp past the 30-day window, wait for the grace tick, then
// assert the row is gone and the customer can no longer reach
// /v1/account/export (401). This is the M8 G6 acceptance gate.
func TestE2E_GraceExpiry_HardDelete(t *testing.T) {
	pool := pgtest.Open(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := startAPIDWithGraceInterval(t, pool)
	key := h.SeedAccount(context.Background(), api.PlanHobby)
	email := seedEmail(api.PlanHobby, "")

	// Schedule deletion.
	if body, status := doReq(t, h, key, http.MethodDelete, "/v1/account", nil); status != http.StatusOK {
		t.Fatalf("delete: %d %s", status, body)
	}

	// Fast-forward the deletion timestamp past the 30-day window so
	// the grace tick (300ms interval) sees the row as overdue on its
	// next pass. WHERE on email — each subtest seeds exactly one
	// account on its own pgtest schema, so this is unambiguous.
	if _, err := pool.Exec(context.Background(),
		`update accounts set deletion_requested_at = now() - interval '31 days' where email = $1`,
		email); err != nil {
		t.Fatalf("fast-forward deletion_requested_at: %v", err)
	}

	// Poll /v1/account/export until it 401s. Bound to a generous
	// deadline (10x the grace interval + slack for boot/handshake).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		_, status := doReq(t, h, key, http.MethodGet, "/v1/account/export", nil)
		if status == http.StatusUnauthorized {
			// Row hard-deleted; the API key is gone with it.
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	t.Fatalf("grace tick did not hard-delete the account within 5s")
}
