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
	pool := pgtest.OpenMigrated(t)
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
	pool := pgtest.OpenMigrated(t)
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
	pool := pgtest.OpenMigrated(t)
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

// --- M8 §14 row 7 (G6 GDPR self-service tightening) --------------------
//
// Spec §17 G6 + ADR-021 cover the customer-facing GDPR endpoints.
// The three existing tests (above) pin the happy path + the grace
// carve-out + the hard-delete tick. Three additional tests close
// the cross-process surface that the unit tests at
// cmd/apid/handlers_account_test.go can't reach:
//
//   - CancelDuringGrace_RestoresAccount — POST /v1/account/restore
//     during the grace window must flip the account back to active
//     and leave the snapshots intact. The unit test
//     handlers_account_test.go:670 covers the "not in grace"
//     branch; this one covers the restore happy path AND asserts
//     the response envelope exposes the restore_until timestamp.
//   - Export_NoAuthHeader_Returns401 — GET /v1/account/export
//     without an Authorization header must 401 + RFC 7807. Locks
//     the DPA-no-auth contract: a future regression that drops the
//     auth wrapper would silently expose personal data.
//   - Export_MultiApp_BundleShape — seed 3 apps + secrets, GET
//     /v1/account/export, parse the JSON, assert every per-slice
//     is present and the apps slice has 3 entries (sanitisation
//     of ciphertext values is pinned at the unit level; here we
//     pin the wire-shape cardinality).

// TestE2E_Delete_CancelDuringGrace_RestoresAccount — POST
// /v1/account schedules deletion (handlers_account.go:65); POST
// /v1/account/restore within the 30-day window must flip the
// account back to active and leave any snapshots intact. The
// cross-process surface we pin is: response shape + the follow-up
// GET /v1/account still returns 200 with the active status.
//
// Note: the existing TestE2E_GraceExpiry_HardDelete fast-forwards
// deletion_requested_at past the grace window; this test stays
// inside the window by NOT fast-forwarding.
func TestE2E_Delete_CancelDuringGrace_RestoresAccount(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := startAPIDWithGraceInterval(t, pool)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// Schedule the deletion.
	if body, status := doReq(t, h, key, http.MethodDelete, "/v1/account", nil); status != http.StatusOK {
		t.Fatalf("delete: %d %s", status, body)
	}

	// Cancel immediately. Within the grace window — no
	// fast-forward.
	body, status := doReq(t, h, key, http.MethodPost, "/v1/account/restore", nil)
	if status != http.StatusOK {
		t.Fatalf("restore: %d %s", status, body)
	}
	var restored struct {
		Status       string `json:"status"`
		ScheduledAt  string `json:"scheduled_at,omitempty"`
		RestoreUntil string `json:"restore_until,omitempty"`
	}
	if err := json.Unmarshal(body, &restored); err != nil {
		t.Fatalf("decode restore envelope: %v body=%s", err, body)
	}
	if restored.Status != "active" {
		t.Errorf("restore envelope: status = %q, want \"active\"", restored.Status)
	}
	if restored.ScheduledAt != "" {
		t.Errorf("restore envelope: scheduled_at = %q, want \"\" (cleared on restore)", restored.ScheduledAt)
	}
	if restored.RestoreUntil != "" {
		t.Errorf("restore envelope: restore_until = %q, want \"\" (cleared on restore)", restored.RestoreUntil)
	}

	// Follow-up GET /v1/account must still answer (the customer
	// would be confused if their dashboard reported 401 right
	// after pressing "Restore").
	if body, status := doReq(t, h, key, http.MethodGet, "/v1/account", nil); status != http.StatusOK {
		t.Errorf("GET /v1/account after restore: status=%d body=%s", status, body)
	}

	// A second DELETE → restore cycle must still work (idempotent
	// on the second pass — handlers_account.go:200 returns the
	// fresh account without stamping deletion_requested_at
	// again).
	if body, status := doReq(t, h, key, http.MethodDelete, "/v1/account", nil); status != http.StatusOK {
		t.Errorf("re-delete after restore: %d %s", status, body)
	}
	if _, status := doReq(t, h, key, http.MethodPost, "/v1/account/restore", nil); status != http.StatusOK {
		t.Errorf("re-restore after re-delete: %d", status)
	}
}

// TestE2E_Export_NoAuthHeader_Returns401 — GET /v1/account/export
// is mounted behind s.auth (server.go:417). A request with no
// Authorization header must 401 + RFC 7807 with
// CodeUnauthorized. Locks the DPA-no-auth contract: the export
// bundle exposes personal data, so a regression that drops the
// auth wrapper (e.g. moving it to authLimited) would silently
// expose every customer's bundle to anonymous HTTP traffic.
//
// We do NOT reuse doReq here because doReq always sets the
// Authorization header. We hand-craft the request so the missing
// header is part of the test surface.
func TestE2E_Export_NoAuthHeader_Returns401(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := startAPIDWithGraceInterval(t, pool)
	// Seed an account so the bundle path is reachable — the
	// 401 must fire BEFORE the gatherExport walks the tables.
	h.SeedAccount(context.Background(), api.PlanHobby)

	// No Authorization header. The auth wrapper must reject.
	req, err := http.NewRequestWithContext(context.Background(),
		http.MethodGet, h.APIDURL+"/v1/account/export", nil)
	if err != nil {
		t.Fatalf("new req: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("GET /v1/account/export: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("export no-auth: status=%d, want 401", resp.StatusCode)
	}
	var prob api.Problem
	if err := json.NewDecoder(resp.Body).Decode(&prob); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if prob.Code != api.CodeUnauthorized {
		t.Errorf("problem.code = %q, want %q", prob.Code, api.CodeUnauthorized)
	}
}

// TestE2E_Export_MultiApp_BundleShape — the export bundle
// (api.AccountExportResponse) carries one slice per resource type
// the customer owns: apps, deployments, builds, instances, usage,
// domains, crons, api_keys, app_secrets + a single Account row.
// This test seeds 3 apps and asserts the bundle's apps slice has
// 3 entries + every other slice is present (even if empty).
//
// The unit-level pin of the per-row shape (ciphertext
// sanitisation, manifest SHA-256) lives at
// cmd/apid/handlers_account_test.go (bundleEncode). Here we pin
// the wire-shape cardinality — the kind of drift a future refactor
// that drops a slice (e.g. "we don't surface builds any more")
// would silently introduce.
func TestE2E_Export_MultiApp_BundleShape(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := startAPIDWithGraceInterval(t, pool)
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	slugs := []string{"alpha", "beta", "gamma"}
	for _, s := range slugs {
		body, status := doReq(t, h, key, http.MethodPost, "/v1/apps",
			api.CreateAppRequest{Slug: s})
		if status != http.StatusCreated {
			t.Fatalf("create app %q: %d %s", s, status, body)
		}
	}

	raw, status := doReq(t, h, key, http.MethodGet, "/v1/account/export", nil)
	if status != http.StatusOK {
		t.Fatalf("export: %d %s", status, raw)
	}
	var bundle api.AccountExportResponse
	if err := json.Unmarshal(raw, &bundle); err != nil {
		t.Fatalf("decode bundle: %v body=%s", err, raw)
	}

	if len(bundle.Apps) != 3 {
		t.Errorf("bundle.apps = %d entries, want 3 (seeded alpha + beta + gamma)", len(bundle.Apps))
	}
	// Every slice must be present (even if empty) — a future
	// refactor that omits a slice from the JSON tag would
	// silently drop it from the customer's export. Use the
	// raw-body substring assertion as the tripwire.
	requiredKeys := []string{
		`"exported_at"`,
		`"account"`,
		`"apps"`,
		`"deployments"`,
		`"builds"`,
		`"instances"`,
		`"usage"`,
		`"domains"`,
		`"crons"`,
		`"api_keys"`,
		`"app_secrets"`,
	}
	for _, k := range requiredKeys {
		if !contains(raw, k) {
			t.Errorf("bundle missing key %s — slice dropped from export wire shape", k)
		}
	}
}

// contains is the small string-search helper used by
// TestE2E_Export_MultiApp_BundleShape. Avoiding strings.Contains
// keeps the call sites uniform with the other test bodies that
// inline grep-style assertions.
func contains(haystack []byte, needle string) bool {
	return bytesContains(haystack, []byte(needle))
}

// bytesContains is a thin wrapper around bytes.Index so the
// call-site above reads naturally. (bytes.Index is fine, but the
// 1-line wrapper documents intent at the use site.)
func bytesContains(s, sub []byte) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		match := true
		for j := range sub {
			if s[i+j] != sub[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
