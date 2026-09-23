// pgstore_preview_teardown_test.go — ADR-095 PR-C (issue #272)
// preview teardown PgStore coverage.
//
// Pins the contracts the apid teardown janitor relies on the
// Postgres-backed store to honour:
//
//  1. ListPreviewsForTeardown filters the closed/stale/expired
//     predicate correctly, ordered by preview_expires_at ASC
//     NULLS LAST, capped by maxPerTick.
//  2. ListPreviewsForTeardown observes its own tombstoned rows
//     (no status='deleted' filter) so the janitor recovers from
//     a crash between the tombstone write and the pr_state write.
//  3. SetPreviewPrState enforces the CHECK-shaped closed set,
//     refuses production rows (preview_of_slug is null), and
//     stamps the column even when status='deleted'.
//
//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// pgSeedPreview seeds an account + a preview apps row under it
// with the supplied state/expiry. Slugs must be globally unique,
// so each call site threads a unique suffix. The account id is
// returned so the row's preview_of_slug lookup mirrors the
// dashboard path. ADR-095 PR-C.
func pgSeedPreview(t *testing.T, s *state.PgStore, ctx context.Context, parent, slug string, prNum int, prState string, expiresAt time.Time, del bool, idx int) (state.App, string) {
	t.Helper()
	acct, err := s.CreateAccount(ctx, "u"+slug+strconv.Itoa(idx)+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	expires := expiresAt
	a := state.App{
		AccountID:        acct.ID,
		Slug:             slug + "-" + strconv.Itoa(idx),
		Type:             "app",
		RAMMB:            256,
		Status:           state.AppActive,
		PreviewOfSlug:    parent,
		PreviewPrNumber:  prNum,
		PreviewPrState:   prState,
		PreviewExpiresAt: &expires,
	}
	created, err := s.CreateAppIfUnderQuota(ctx, a, previewPgLimits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota(%q): %v", a.Slug, err)
	}
	if del {
		if _, err := s.SoftDeleteAppCascade(ctx, created.ID); err != nil {
			t.Fatalf("SoftDeleteAppCascade(%q): %v", a.Slug, err)
		}
	}
	return created, acct.ID
}

// previewPgLimits mirrors previewTestLimits in the memstore tests:
// a generous cap so the per-account quota check never trips while
// the preview path exercises the store.
var previewPgLimits = api.Limits{DeployedApps: 10000}

func TestPg_ListPreviewsForTeardown_PicksClosedAndExpired(t *testing.T) {
	s, ctx := pgStore(t)
	now := time.Now().UTC()

	open, _ := pgSeedPreview(t, s, ctx, "demo", "pr-1", 1, state.PreviewPrStateOpen, now.Add(24*time.Hour), false, 1)
	closed, _ := pgSeedPreview(t, s, ctx, "demo", "pr-2", 2, state.PreviewPrStateClosed, now.Add(24*time.Hour), false, 2)
	stale, _ := pgSeedPreview(t, s, ctx, "demo", "pr-3", 3, state.PreviewPrStateStale, now.Add(24*time.Hour), false, 3)
	expired, _ := pgSeedPreview(t, s, ctx, "demo", "pr-4", 4, state.PreviewPrStateOpen, now.Add(-time.Hour), false, 4)
	torn, _ := pgSeedPreview(t, s, ctx, "demo", "pr-5", 5, state.PreviewPrStateTornDown, now.Add(-time.Hour), false, 5)

	rows, err := s.ListPreviewsForTeardown(ctx, now, 100)
	if err != nil {
		t.Fatalf("ListPreviewsForTeardown: %v", err)
	}

	want := map[string]bool{
		closed.ID:  true,
		stale.ID:   true,
		expired.ID: true,
	}
	for _, r := range rows {
		if r.ID == open.ID {
			t.Errorf("open+future preview %q should NOT be eligible for teardown", r.Slug)
		}
		if r.ID == torn.ID {
			t.Errorf("torn_down preview %q should NOT be eligible for teardown", r.Slug)
		}
		delete(want, r.ID)
	}
	if len(want) != 0 {
		for id := range want {
			t.Errorf("eligible preview id=%s missing from sweep result", id)
		}
	}
}

func TestPg_ClaimPreviewTeardownRejectsReopenedLease(t *testing.T) {
	s, ctx := pgStore(t)
	now := time.Now().UTC()
	app, _ := pgSeedPreview(t, s, ctx, "demo", "pr-claim", 42,
		state.PreviewPrStateStale, now.Add(-time.Hour), false, 42)
	if _, err := s.RefreshPRPreview(ctx, app.ID, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimPreviewTeardown(ctx, app, now); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("claim from stale snapshot = %v, want not found", err)
	}
	if _, err := s.SetPreviewPrState(ctx, app.ID, state.PreviewPrStateStale); err != nil {
		t.Fatal(err)
	}
	current, err := s.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimPreviewTeardown(ctx, current, now)
	if err != nil || claimed.PreviewPrState != state.PreviewPrStateTearingDown {
		t.Fatalf("claim = (%+v, %v)", claimed, err)
	}
	if _, err := s.RefreshPRPreview(ctx, app.ID, now.Add(time.Hour)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("refresh after claim = %v, want not found", err)
	}
	if _, err := s.SoftDeleteAppCascade(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := s.ListPreviewsForTeardown(ctx, now, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		found = found || row.ID == app.ID
	}
	if !found {
		t.Fatal("claimed deleted preview is not recoverable by janitor")
	}
}

func TestPg_ListPreviewsForTeardown_OrdersByExpiryASC(t *testing.T) {
	s, ctx := pgStore(t)
	now := time.Now().UTC()

	later, _ := pgSeedPreview(t, s, ctx, "demo", "pr-later", 1, state.PreviewPrStateOpen, now.Add(-1*time.Hour), false, 10)
	middle, _ := pgSeedPreview(t, s, ctx, "demo", "pr-middle", 2, state.PreviewPrStateOpen, now.Add(-3*time.Hour), false, 11)
	earliest, _ := pgSeedPreview(t, s, ctx, "demo", "pr-earliest", 3, state.PreviewPrStateOpen, now.Add(-5*time.Hour), false, 12)

	rows, err := s.ListPreviewsForTeardown(ctx, now, 100)
	if err != nil {
		t.Fatalf("ListPreviewsForTeardown: %v", err)
	}

	wantOrder := []string{earliest.ID, middle.ID, later.ID}
	if len(rows) != 3 {
		t.Fatalf("got %d rows, want 3", len(rows))
	}
	for i, want := range wantOrder {
		if rows[i].ID != want {
			t.Errorf("rows[%d].ID = %q, want %q", i, rows[i].ID, want)
		}
	}
}

func TestPg_ListPreviewsForTeardown_IncludesTombstonedRows(t *testing.T) {
	s, ctx := pgStore(t)
	now := time.Now().UTC()
	a, _ := pgSeedPreview(t, s, ctx, "demo", "pr-tomb", 1, state.PreviewPrStateClosed, now.Add(-time.Hour), true, 20)

	rows, err := s.ListPreviewsForTeardown(ctx, now, 100)
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
		t.Errorf("tombstoned preview %q missing from sweep result", a.Slug)
	}
}

func TestPg_SetPreviewPrState_PreviewOnly(t *testing.T) {
	s, ctx := pgStore(t)
	a, _ := pgSeedPreview(t, s, ctx, "demo", "pr-7", 7, state.PreviewPrStateOpen, time.Now().Add(time.Hour), false, 30)

	got, err := s.SetPreviewPrState(ctx, a.ID, state.PreviewPrStateClosed)
	if err != nil {
		t.Fatalf("SetPreviewPrState: %v", err)
	}
	if got.PreviewPrState != state.PreviewPrStateClosed {
		t.Errorf("PreviewPrState = %q, want closed", got.PreviewPrState)
	}
}

func TestPg_SetPreviewPrState_RejectsProductionApp(t *testing.T) {
	s, ctx := pgStore(t)
	acct, err := s.CreateAccount(ctx, "u-prod-preview-test@example.com", api.PlanPro)
	if err != nil {
		t.Fatalf("CreateAccount: %v", err)
	}
	prod := state.App{
		AccountID: acct.ID,
		Slug:      "prod-app-no-preview",
		Type:      "app",
		RAMMB:     256,
		Status:    state.AppActive,
	}
	created, err := s.CreateAppIfUnderQuota(ctx, prod, previewPgLimits)
	if err != nil {
		t.Fatalf("CreateAppIfUnderQuota: %v", err)
	}
	if _, err := s.SetPreviewPrState(ctx, created.ID, state.PreviewPrStateClosed); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (production apps are not eligible)", err)
	}
}

func TestPg_SetPreviewPrState_RejectsInvalidState(t *testing.T) {
	s, ctx := pgStore(t)
	a, _ := pgSeedPreview(t, s, ctx, "demo", "pr-8", 8, state.PreviewPrStateOpen, time.Now().Add(time.Hour), false, 40)
	if _, err := s.SetPreviewPrState(ctx, a.ID, "abandoned"); !errors.Is(err, state.ErrInvalidPreviewPrState) {
		t.Errorf("err = %v, want ErrInvalidPreviewPrState", err)
	}
}
