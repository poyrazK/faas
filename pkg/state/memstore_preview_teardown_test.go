// memstore_preview_teardown_test.go — ADR-095 PR-C (issue #272)
// preview teardown MemStore coverage.
//
// Pins the three contracts the apid teardown janitor and the
// dashboard preview panel rely on the in-memory backend to honour:
//
//  1. PreviewAppsByParent hides soft-deleted rows (so the
//     dashboard's live pane never shows torn_down apps).
//  2. ListPreviewsForTeardown returns closed/stale rows AND any
//     row past preview_expires_at, regardless of status — the
//     janitor must observe its own tombstones.
//  3. SetPreviewPrState refuses production apps and the closed-set
//     validator, returning ErrNotFound and ErrInvalidPreviewPrState
//     respectively.
package state

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// previewTestLimits matches the dispatcher's preview-app shape
// (cmd/apid/githubd_bridge.go:887): a generous ceiling so the
// per-account quota check never trips while the preview path is
// exercising. The dashboard's onboarding flow is the real
// enforcement point; the dispatcher is intentionally permissive.
var previewTestLimits = api.Limits{DeployedApps: 10000}

// seedPreviewAccount ensures the account row exists so the
// preview row's foreign-key-like invariant in MemStore
// (CreateAppIfUnderQuota → ErrNotFound when the account is missing)
// is honoured. Returns the freshly-minted account ID so callers
// can stamp it on App.AccountID. ADR-095 PR-C.
func seedPreviewAccount(t *testing.T, m *MemStore, email string) string {
	t.Helper()
	acct, err := m.CreateAccount(context.Background(), email, api.PlanHobby)
	if err != nil {
		t.Fatalf("CreateAccount(%q): %v", email, err)
	}
	return acct.ID
}

func TestMemStore_ListPreviewsForTeardown_PicksClosedAndExpired(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := seedPreviewAccount(t, m, "test1@example.com")
	parent := "demo-app"
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	// open / future expires: not eligible (PR still live).
	openApp := mustCreatePreview(t, m, acct, parent, "pr-1-demo", 1, PreviewPrStateOpen, now.Add(24*time.Hour))
	// closed / future expires: eligible (closed is in the terminal set).
	closedApp := mustCreatePreview(t, m, acct, parent, "pr-2-demo", 2, PreviewPrStateClosed, now.Add(24*time.Hour))
	// stale / future expires: eligible (stale is in the terminal set).
	staleApp := mustCreatePreview(t, m, acct, parent, "pr-3-demo", 3, PreviewPrStateStale, now.Add(24*time.Hour))
	// open / past expires: eligible (TTL elapsed).
	expiredOpenApp := mustCreatePreview(t, m, acct, parent, "pr-4-demo", 4, PreviewPrStateOpen, now.Add(-time.Hour))
	// torn_down: never eligible (terminal state; sweeper skips).
	tornApp := mustCreatePreview(t, m, acct, parent, "pr-5-demo", 5, PreviewPrStateTornDown, now.Add(-time.Hour))

	rows, err := m.ListPreviewsForTeardown(ctx, now, 100)
	if err != nil {
		t.Fatalf("ListPreviewsForTeardown: %v", err)
	}
	eligibleIDs := map[string]string{
		closedApp.ID:      closedApp.Slug,
		staleApp.ID:       staleApp.Slug,
		expiredOpenApp.ID: expiredOpenApp.Slug,
	}
	for _, a := range rows {
		if a.ID == tornApp.ID {
			t.Errorf("torn_down preview %q should NOT be eligible for teardown", a.Slug)
		}
		if a.ID == openApp.ID {
			t.Errorf("open+future preview %q should NOT be eligible for teardown", a.Slug)
		}
		if _, want := eligibleIDs[a.ID]; want {
			delete(eligibleIDs, a.ID)
		}
	}
	if len(eligibleIDs) != 0 {
		for id, slug := range eligibleIDs {
			t.Errorf("eligible preview %q (id=%s) missing from sweep result", slug, id)
		}
	}
}

func TestMemStore_ListPreviewsForTeardown_OrdersByExpiryASC(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := seedPreviewAccount(t, m, "test2@example.com")
	parent := "demo-app"
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)

	// Three expired rows in arbitrary order. The sweeper should
	// return the oldest expiry first so a stuck janitor drains
	// the oldest backlog before newer rows.
	later := mustCreatePreview(t, m, acct, parent, "pr-later", 1, PreviewPrStateOpen, now.Add(-1*time.Hour))
	middle := mustCreatePreview(t, m, acct, parent, "pr-middle", 2, PreviewPrStateOpen, now.Add(-3*time.Hour))
	earliest := mustCreatePreview(t, m, acct, parent, "pr-earliest", 3, PreviewPrStateOpen, now.Add(-5*time.Hour))

	rows, err := m.ListPreviewsForTeardown(ctx, now, 100)
	if err != nil {
		t.Fatalf("ListPreviewsForTeardown: %v", err)
	}
	wantOrder := []string{earliest.ID, middle.ID, later.ID}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for i, want := range wantOrder {
		if rows[i].ID != want {
			t.Errorf("rows[%d].ID = %q, want %q (full order: %v)", i, rows[i].ID, want, wantOrder)
		}
	}
}

func TestMemStore_ListPreviewsForTeardown_HonoursMaxPerTick(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := seedPreviewAccount(t, m, "test3@example.com")
	parent := "demo-app"
	now := time.Now()
	for i := 1; i <= 5; i++ {
		mustCreatePreview(t, m, acct, parent, "pr-cap-"+string(rune('0'+i)), i,
			PreviewPrStateClosed, now.Add(time.Hour))
	}
	rows, err := m.ListPreviewsForTeardown(ctx, now, 2)
	if err != nil {
		t.Fatalf("ListPreviewsForTeardown: %v", err)
	}
	if len(rows) != 2 {
		t.Errorf("len(rows) = %d, want 2 (maxPerTick cap)", len(rows))
	}
}

func TestMemStore_ListPreviewsForTeardown_ZeroMaxIsNoop(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := seedPreviewAccount(t, m, "test4@example.com")
	parent := "demo-app"
	now := time.Now()
	mustCreatePreview(t, m, acct, parent, "pr-zero", 1, PreviewPrStateClosed, now.Add(-time.Hour))
	rows, err := m.ListPreviewsForTeardown(ctx, now, 0)
	if err != nil {
		t.Fatalf("ListPreviewsForTeardown: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("len(rows) = %d, want 0 (maxPerTick=0 must be a no-op)", len(rows))
	}
}

func TestMemStore_ListPreviewsForTeardown_IncludesTombstonedRows(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := seedPreviewAccount(t, m, "test5@example.com")
	parent := "demo-app"
	now := time.Now()
	a := mustCreatePreview(t, m, acct, parent, "pr-tomb", 1, PreviewPrStateClosed, now.Add(-time.Hour))
	// The janitor itself flips status='deleted' on torn_down. The
	// sweep MUST still observe tombstoned rows so a crash between
	// the tombstone write and the preview_pr_state write recovers
	// on the next tick.
	if _, err := m.SoftDeleteAppCascade(ctx, a.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}
	rows, err := m.ListPreviewsForTeardown(ctx, now, 100)
	if err != nil {
		t.Fatalf("ListPreviewsForTeardown: %v", err)
	}
	found := false
	for _, r := range rows {
		if r.ID == a.ID {
			found = true
		}
	}
	if !found {
		t.Errorf("tombstoned preview %q missing from sweep result (must observe own tombstones)", a.Slug)
	}
}

func TestMemStore_PreviewAppsByParent_HidesTombstoned(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := seedPreviewAccount(t, m, "test6@example.com")
	parent := "demo-app"
	live := mustCreatePreview(t, m, acct, parent, "pr-live", 1, PreviewPrStateOpen, time.Now().Add(time.Hour))
	gone := mustCreatePreview(t, m, acct, parent, "pr-gone", 2, PreviewPrStateClosed, time.Now().Add(time.Hour))
	if _, err := m.SoftDeleteAppCascade(ctx, gone.ID); err != nil {
		t.Fatalf("SoftDeleteAppCascade: %v", err)
	}
	rows, err := m.PreviewAppsByParent(ctx, acct, parent)
	if err != nil {
		t.Fatalf("PreviewAppsByParent: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("len(rows) = %d, want 1 (tombstoned must be hidden)", len(rows))
	}
	if rows[0].ID != live.ID {
		t.Errorf("got %q, want %q (live preview)", rows[0].ID, live.ID)
	}
}

func TestMemStore_SetPreviewPrState_PreviewOnly(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := seedPreviewAccount(t, m, "test7@example.com")
	prev := mustCreatePreview(t, m, acct, "demo", "pr-7-demo", 7, PreviewPrStateOpen, time.Now().Add(time.Hour))

	got, err := m.SetPreviewPrState(ctx, prev.ID, PreviewPrStateClosed)
	if err != nil {
		t.Fatalf("SetPreviewPrState: %v", err)
	}
	if got.PreviewPrState != PreviewPrStateClosed {
		t.Errorf("PreviewPrState = %q, want closed", got.PreviewPrState)
	}
}

func TestMemStore_ClosePRPreviewStartsFixedGraceIdempotently(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := seedPreviewAccount(t, m, "test-close-preview@example.com")
	now := time.Now().UTC()
	prev := mustCreatePreview(t, m, acct, "demo", "pr-close-demo", 17,
		PreviewPrStateOpen, now.Add(7*24*time.Hour))
	deadline := now.Add(PRPreviewClosedGrace)

	closed, err := m.ClosePRPreview(ctx, prev.ID, deadline)
	if err != nil {
		t.Fatalf("ClosePRPreview: %v", err)
	}
	if closed.PreviewPrState != PreviewPrStateClosed || closed.PreviewExpiresAt == nil || !closed.PreviewExpiresAt.Equal(deadline) {
		t.Fatalf("closed preview = state %q expiry %v, want closed at %v", closed.PreviewPrState, closed.PreviewExpiresAt, deadline)
	}

	// A webhook redelivery may arrive after some of the grace period has
	// elapsed; it must not restart the clock.
	replayDeadline := deadline.Add(time.Hour)
	replayed, err := m.ClosePRPreview(ctx, prev.ID, replayDeadline)
	if err != nil {
		t.Fatalf("ClosePRPreview replay: %v", err)
	}
	if replayed.PreviewExpiresAt == nil || !replayed.PreviewExpiresAt.Equal(deadline) {
		t.Fatalf("replayed close expiry = %v, want original deadline %v", replayed.PreviewExpiresAt, deadline)
	}
}

func TestMemStore_ClosePRPreviewCannotReviveStaleOrDeveloperPreview(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := seedPreviewAccount(t, m, "test-close-preview-guard@example.com")
	now := time.Now().UTC()
	stale := mustCreatePreview(t, m, acct, "demo", "pr-stale-close", 18, PreviewPrStateStale, now.Add(time.Hour))
	if _, err := m.ClosePRPreview(ctx, stale.ID, now.Add(PRPreviewClosedGrace)); !errors.Is(err, ErrNotFound) {
		t.Errorf("ClosePRPreview(stale) = %v, want ErrNotFound", err)
	}
	torn := mustCreatePreview(t, m, acct, "demo", "pr-torn-close", 19, PreviewPrStateTornDown, now.Add(time.Hour))
	if _, err := m.ClosePRPreview(ctx, torn.ID, now.Add(PRPreviewClosedGrace)); !errors.Is(err, ErrNotFound) {
		t.Errorf("ClosePRPreview(torn_down) = %v, want ErrNotFound", err)
	}
	dev := mustCreatePreview(t, m, acct, "demo", "dev-close", 0, PreviewPrStateOpen, now.Add(time.Hour))
	if _, err := m.ClosePRPreview(ctx, dev.ID, now.Add(PRPreviewClosedGrace)); !errors.Is(err, ErrNotFound) {
		t.Errorf("ClosePRPreview(developer preview) = %v, want ErrNotFound", err)
	}
}

func TestMemStore_SetPreviewPrState_RejectsProductionApp(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	// A non-preview app row: empty PreviewOfSlug is the production
	// shape. The sweep must never relabel a production app, so
	// SetPreviewPrState returns ErrNotFound for these rows.
	acct := seedPreviewAccount(t, m, "test9@example.com")
	prod := App{
		AccountID: acct,
		Slug:      "demo-app",
		Status:    AppActive,
	}
	created, err := m.CreateAppIfUnderQuota(ctx, prod, previewTestLimits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	if _, err := m.SetPreviewPrState(ctx, created.ID, PreviewPrStateClosed); !errors.Is(err, ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (production apps are not eligible)", err)
	}
}

func TestMemStore_SetPreviewPrState_RejectsInvalidState(t *testing.T) {
	m := NewMemStore()
	ctx := context.Background()
	acct := seedPreviewAccount(t, m, "test8@example.com")
	prev := mustCreatePreview(t, m, acct, "demo", "pr-8-demo", 8, PreviewPrStateOpen, time.Now().Add(time.Hour))
	_, err := m.SetPreviewPrState(ctx, prev.ID, "abandoned")
	if !errors.Is(err, ErrInvalidPreviewPrState) {
		t.Errorf("err = %v, want ErrInvalidPreviewPrState", err)
	}
}

// --- helpers ---

func mustCreatePreview(t *testing.T, m *MemStore, accountID, parentSlug, slug string, prNumber int, prState string, expiresAt time.Time) App {
	t.Helper()
	expires := expiresAt
	a := App{
		AccountID:        accountID,
		Slug:             slug,
		Type:             "app",
		RAMMB:            256,
		Status:           AppActive,
		PreviewOfSlug:    parentSlug,
		PreviewPrNumber:  prNumber,
		PreviewPrState:   prState,
		PreviewExpiresAt: &expires,
	}
	created, err := m.CreateAppIfUnderQuota(context.Background(), a, previewTestLimits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota(%q): %v", slug, err)
	}
	return created
}
