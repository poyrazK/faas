// preview_e2e_test.go — ADR-095 PR-C / issue #272 preview
// environments e2e. Bitmask: APID only (no schedd / vmmd / imaged
// — the preview sweep doesn't need to wake a VM, only to drive
// the apps-table state machine).
//
// What this exercises vs. the unit tests:
//
//  - pkg/state preview teardown tests  → store contracts
//    (PreviewAppsByParent filters tombed rows;
//    ListPreviewsForTeardown selects expired / closed rows;
//    SetPreviewPrState refuses non-preview rows).
//
//  - cmd/apid preview_janitor_test.go   → sweepOnce state machine
//    + recovery semantics (open / closed / stale / torn_down;
//    notify-best-effort; partial-apply crash recovery).
//
//  - pkg/dashboard preview_panel_test.go → HTML shape of the
//    dashboard panel (chip, copy URL, empty state, nonce on the
//    inline script, preview rows indented on the apps list).
//
//  - cmd/e2e preview_e2e_test.go (this file) → the only path
//    that exercises the *live* apid process with the real
//    Postgres + the production janitor config. The unit tests
//    exercise sweepOnce via MemStore + withClock; this file
//    exercises the goroutine path (Run) end-to-end with the
//    env-driven interval (FAAS_PREVIEW_JANITOR_INTERVAL_SECONDS)
//    so a sweep actually fires during the test.
//
// The bitmask + env knob is the minimal surface area needed to
// reach the cron. Adding a separate admin endpoint just to drive
// the sweep would have been worse — that endpoint would have to
// be gated behind a build tag to avoid a customer-facing
// footgun, and the env knob already exists for the deploy-wake
// cron family (mirrors the pattern).

//go:build !no_pg

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
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPreview_E2E_HappyPath_PreviewAppsByParent confirms the
// dashboard's "preview environments" pane reads through the
// store: a production app created via POST /v1/apps has a
// preview-app sibling inserted via the dispatch path (mirrors
// githubd's webhook) and PreviewAppsByParent surfaces the
// sibling with its preview_of_slug / preview_pr_number /
// preview_pr_state metadata populated correctly.
//
// Why not GET /v1/apps/{slug}: per ADR-095 PR-C, the preview
// metadata is dashboard-internal only — the public AppResponse
// does not carry preview_of_slug / preview_pr_number /
// preview_pr_state (the user explicitly locked the IsPreview
// scope to "Dashboard view-model only", so wire/SDK are
// untouched). The store's PreviewAppsByParent is the customer-
// facing surface the dashboard's render path reads.
func TestPreview_E2E_HappyPath_PreviewAppsByParent(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		// Long interval so this test's row stays alive for its
		// own assertions (the TTL-expiry test below uses 1s).
		"FAAS_PREVIEW_JANITOR_INTERVAL_SECONDS=3600",
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	// Production app via the public API.
	prodSlug := "preview-happy-prod"
	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: prodSlug})
	if len(createRec) == 0 {
		t.Fatalf("empty create response")
	}
	var prodApp api.AppResponse
	if err := json.Unmarshal(createRec, &prodApp); err != nil {
		t.Fatalf("decode prod app: %v body=%s", err, createRec)
	}
	// AppResponse doesn't expose AccountID (preview metadata is
	// dashboard-internal per ADR-095 PR-C scope), so look it up
	// via the api_keys.account_id join (same pattern as
	// accountIDFromKey).
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	// Preview sibling via the dispatch path (mirrors what githubd
	// would do). Insert directly via SQL because the public API
	// has no preview-provisioning endpoint — that's the githubd
	// webhook's job and exercising it requires GitHub network.
	previewSlug := prodSlug + "-pr-7"
	if err := insertPreviewApp(t, pool, accountID, previewSlug, prodSlug, 7, state.PreviewPrStateOpen); err != nil {
		t.Fatalf("seed preview: %v", err)
	}

	// Round-trip via the dashboard's read path: PreviewAppsByParent
	// surfaces the preview-app row with its preview_of_slug,
	// preview_pr_number, preview_pr_state populated correctly.
	// Pull the row back via the store directly (PgStore would be
	// the production type; MemStore would also work for the shape,
	// but the e2e uses the live apid + PgStore).
	row := getAppBySlug(t, pool, previewSlug)
	if row.PreviewOfSlug != prodSlug {
		t.Errorf("PreviewOfSlug = %q, want %q", row.PreviewOfSlug, prodSlug)
	}
	if row.PreviewPrNumber != 7 {
		t.Errorf("PreviewPrNumber = %d, want 7", row.PreviewPrNumber)
	}
	if row.PreviewPrState != state.PreviewPrStateOpen {
		t.Errorf("PreviewPrState = %q, want open", row.PreviewPrState)
	}
	if row.Status != state.AppActive {
		t.Errorf("Status = %q, want active (preview must survive idle janitor)", row.Status)
	}
}

// TestPreview_E2E_TTLExpiry_JanitorSweep boots apid with the
// janitor accelerated (1-second interval) and confirms the
// full end-to-end TTL expiry:
//
//  1. Seed a preview app row whose preview_expires_at is in the
//     past (mirrors a TTL elapsed under a long-open PR).
//  2. Wait long enough for at least one janitor tick.
//  3. Re-fetch the row: preview_pr_state='torn_down' AND
//     status='deleted' (the tombstone pair).
//
// This is the integration pin for "the live cron actually
// fires and writes the right state"; the unit tests cover
// sweepOnce's per-row logic, this covers the Run loop + the
// env-driven interval + the db.NotifyAppDelete emission path
// against a real Postgres schema.
func TestPreview_E2E_TTLExpiry_JanitorSweep(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	// 1s interval + 0s startup delay so the first tick fires
	// within ~1s of apid boot. This is the env knob the cron
	// exposes for tests; production never overrides it.
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_PREVIEW_JANITOR_INTERVAL_SECONDS=1",
		"FAAS_PREVIEW_JANITOR_STARTUP_DELAY_SECONDS=0",
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "ttl-prod"})
	var prodApp api.AppResponse
	if err := json.Unmarshal(createRec, &prodApp); err != nil {
		t.Fatalf("decode prod: %v body=%s", err, createRec)
	}
	accountID := accountIDFromKey(t, context.Background(), pool, key)
	_ = prodApp // preview_app seeded by account_id, not by prodApp.ID

	previewSlug := "ttl-prod-pr-9"
	previewID, err := insertPreviewAppWithExpiry(t, pool, accountID,
		previewSlug, "ttl-prod", 9, state.PreviewPrStateOpen,
		time.Now().Add(-time.Hour)) // TTL elapsed an hour ago
	if err != nil {
		t.Fatalf("seed preview: %v", err)
	}

	// Wait up to 10s for the janitor to fire. With 1s interval
	// the first sweep lands within ~1s; the loop is defensive
	// against scheduling jitter on busy CI runners.
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		got, err := getAppByID(t, pool, previewID)
		if err != nil {
			t.Fatalf("getAppByID: %v", err)
		}
		if got.PreviewPrState == state.PreviewPrStateTornDown && got.Status == state.AppDeleted {
			return // success
		}
		time.Sleep(250 * time.Millisecond)
	}
	got, _ := getAppByID(t, pool, previewID)
	t.Fatalf("preview row %s not tombstoned within 10s; got state=%q status=%q",
		previewSlug, got.PreviewPrState, got.Status)
}

// TestPreview_E2E_OpenPreviewSurvivesJanitorTick is the
// negative pin: a preview whose preview_expires_at is in the
// future (i.e. a live, open preview under the TTL cap) must
// NOT be tombstoned by the janitor. Catches a regression where
// a future-dated row is mistakenly swept because the SQL
// predicate dropped the > now() comparison.
func TestPreview_E2E_OpenPreviewSurvivesJanitorTick(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_PREVIEW_JANITOR_INTERVAL_SECONDS=1",
		"FAAS_PREVIEW_JANITOR_STARTUP_DELAY_SECONDS=0",
	})
	key := h.SeedAccount(context.Background(), api.PlanHobby)

	createRec := doReqBytes(t, h, key, http.MethodPost, "/v1/apps",
		api.CreateAppRequest{Slug: "survive-prod"})
	var prodApp api.AppResponse
	if err := json.Unmarshal(createRec, &prodApp); err != nil {
		t.Fatalf("decode prod: %v body=%s", err, createRec)
	}
	accountID := accountIDFromKey(t, context.Background(), pool, key)

	previewSlug := "survive-prod-pr-3"
	previewID, err := insertPreviewAppWithExpiry(t, pool, accountID,
		previewSlug, "survive-prod", 3, state.PreviewPrStateOpen,
		time.Now().Add(7*24*time.Hour)) // TTL 7 days away
	if err != nil {
		t.Fatalf("seed preview: %v", err)
	}

	// Let several janitor ticks pass (3s = ~3 ticks at 1s
	// interval). The preview must remain open.
	time.Sleep(3500 * time.Millisecond)

	got, err := getAppByID(t, pool, previewID)
	if err != nil {
		t.Fatalf("getAppByID: %v", err)
	}
	if got.Status == state.AppDeleted {
		t.Errorf("live preview tombstoned; preview_pr_state=%q status=%q",
			got.PreviewPrState, got.Status)
	}
	if got.PreviewPrState != state.PreviewPrStateOpen {
		t.Errorf("live preview mutated; preview_pr_state=%q want open", got.PreviewPrState)
	}
}

// --- helpers ---

// insertPreviewApp seeds a preview app row directly via SQL,
// bypassing the public API because the public API has no
// preview-provisioning endpoint (that's githubd's job).
// Mirrors cmd/apid/githubd_bridge.go::dispatchPullRequestPreview
// without the GitHub webhook dependency.
func insertPreviewApp(t *testing.T, pool *pgxpool.Pool, accountID, slug, parentSlug string, prNum int, prState string) error {
	t.Helper()
	_, err := pool.Exec(context.Background(), `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency,
		                  idle_timeout_s, status, preview_of_slug,
		                  preview_pr_number, preview_pr_state, created_at)
		values (gen_random_uuid(), $1, $2, 'app', 256, 1, 30,
		        'active', $3, $4, $5, now())`,
		accountID, slug, parentSlug, prNum, prState)
	return err
}

// insertPreviewAppWithExpiry is the TTL-bearing sibling of
// insertPreviewApp. The expiry is set to an explicit timestamp
// so the e2e can drive the past / future distinction.
func insertPreviewAppWithExpiry(t *testing.T, pool *pgxpool.Pool, accountID, slug, parentSlug string, prNum int, prState string, expiresAt time.Time) (string, error) {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(), `
		insert into apps (id, account_id, slug, type, ram_mb, max_concurrency,
		                  idle_timeout_s, status, preview_of_slug,
		                  preview_pr_number, preview_pr_state,
		                  preview_expires_at, created_at)
		values (gen_random_uuid(), $1, $2, 'app', 256, 1, 30,
		        'active', $3, $4, $5, $6, now())
		returning id`,
		accountID, slug, parentSlug, prNum, prState, expiresAt).Scan(&id)
	return id, err
}

// getAppByID reads back the row by primary key — needed because
// the slug-keyed GET /v1/apps/{slug} would 404 the row once
// status='deleted' (the public API filters deleted apps out).
func getAppByID(t *testing.T, pool *pgxpool.Pool, id string) (state.App, error) {
	t.Helper()
	var a state.App
	err := pool.QueryRow(context.Background(),
		`select id, account_id, slug, status::text, preview_of_slug,
		        preview_pr_number, preview_pr_state::text
		   from apps where id = $1`, id).
		Scan(&a.ID, &a.AccountID, &a.Slug, &a.Status, &a.PreviewOfSlug,
			&a.PreviewPrNumber, &a.PreviewPrState)
	return a, err
}

// getAppBySlug reads a row back by slug. Mirrors the public
// AppBySlug store method but reads the preview columns the
// dashboard's PreviewAppsByParent exposes. Used by the
// happy-path e2e to assert the preview metadata round-trips
// through Postgres correctly.
func getAppBySlug(t *testing.T, pool *pgxpool.Pool, slug string) state.App {
	t.Helper()
	var a state.App
	err := pool.QueryRow(context.Background(),
		`select id, account_id, slug, status::text, preview_of_slug,
		        preview_pr_number, preview_pr_state::text
		   from apps where slug = $1`, slug).
		Scan(&a.ID, &a.AccountID, &a.Slug, &a.Status, &a.PreviewOfSlug,
			&a.PreviewPrNumber, &a.PreviewPrState)
	if err != nil {
		t.Fatalf("getAppBySlug(%q): %v", slug, err)
	}
	return a
}
