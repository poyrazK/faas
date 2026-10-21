// memstore_preview_mega5_test.go — Coverage Mega-PR #5 cluster 5:
// pin the preview lifecycle on *MemStore. Targets:
//
//   - StampPreviewDestroyCommentedAt (memstore.go:2542) — preview-only
//     idempotent stamp; production rows return ErrNotFound
//   - ListPreviewsForAccount (memstore.go:2559) — every non-deleted
//     preview across parents, sorted CreatedAt DESC
//   - PreviewAppsByParent (memstore.go:2449, 90%) — fill the missing
//     empty-result branch
//
// Whitebox `package state`. No Postgres dependency.

package state_test

import (
	"errors"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

// seedApp inserts an App into m.apps under the given ID. Returns the
// struct so the test can mutate it (e.g. set PreviewOfSlug or Status).
func seedApp_Mega5(m *state.MemStore, id, accountID, slug string) state.App {
	a := state.App{
		ID:        id,
		AccountID: accountID,
		Slug:      slug,
		Status:    state.AppActive,
		CreatedAt: time.Now().UTC(),
	}
	(*m.Apps())[id] = a
	return a
}

// --- StampPreviewDestroyCommentedAt ------------------------------

func TestStampPreviewDestroyCommentedAt_NotFound_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	if _, err := m.StampPreviewDestroyCommentedAt(t.Context(), "missing", time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound", err)
	}
}

func TestStampPreviewDestroyCommentedAt_ProductionApp_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	// Production app (PreviewOfSlug == "") → ErrNotFound per the
	// doc-comment contract.
	seedApp_Mega5(m, "prod-1", "acc-1", "my-app")
	if _, err := m.StampPreviewDestroyCommentedAt(t.Context(), "prod-1", time.Now()); !errors.Is(err, state.ErrNotFound) {
		t.Errorf("err = %v, want ErrNotFound (production app)", err)
	}
}

func TestStampPreviewDestroyCommentedAt_Success_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	a := seedApp_Mega5(m, "prev-1", "acc-1", "my-app")
	a.PreviewOfSlug = "my-app"
	(*m.Apps())["prev-1"] = a

	when := time.Now()
	app, err := m.StampPreviewDestroyCommentedAt(t.Context(), "prev-1", when)
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if app.PreviewDestroyCommentedAt == nil {
		t.Fatal("PreviewDestroyCommentedAt = nil, want non-nil")
	}
	if !app.PreviewDestroyCommentedAt.Equal(when) {
		t.Errorf("PreviewDestroyCommentedAt = %v, want %v", *app.PreviewDestroyCommentedAt, when)
	}
	// Persisted back into m.apps.
	if (*m.Apps())["prev-1"].PreviewDestroyCommentedAt == nil {
		t.Error("m.apps[prev-1].PreviewDestroyCommentedAt = nil, want persisted")
	}
}

func TestStampPreviewDestroyCommentedAt_Idempotent_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	a := seedApp_Mega5(m, "prev-1", "acc-1", "my-app")
	a.PreviewOfSlug = "my-app"
	(*m.Apps())["prev-1"] = a

	when1 := time.Now()
	when2 := time.Now().Add(time.Hour)
	if _, err := m.StampPreviewDestroyCommentedAt(t.Context(), "prev-1", when1); err != nil {
		t.Fatalf("first stamp: %v", err)
	}
	// Re-stamp with a different time → row gets the new time (the
	// comment calls the column value the "dedupe key, not the row
	// identity"; idempotency here means "stamp is always accepted
	// for a preview row", not "stamp preserves the old value").
	if _, err := m.StampPreviewDestroyCommentedAt(t.Context(), "prev-1", when2); err != nil {
		t.Fatalf("second stamp: %v", err)
	}
	if !(*m.Apps())["prev-1"].PreviewDestroyCommentedAt.Equal(when2) {
		t.Errorf("second stamp: got %v, want %v",
			*(*m.Apps())["prev-1"].PreviewDestroyCommentedAt, when2)
	}
}

// --- ListPreviewsForAccount ---------------------------------------

func TestListPreviewsForAccount_Empty_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	apps, err := m.ListPreviewsForAccount(t.Context(), "acc-1")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if len(apps) != 0 {
		t.Errorf("len = %d, want 0", len(apps))
	}
}

func TestListPreviewsForAccount_OnlyDeleted_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	a := seedApp_Mega5(m, "prev-1", "acc-1", "my-app")
	a.PreviewOfSlug = "my-app"
	a.Status = state.AppDeleted
	(*m.Apps())["prev-1"] = a

	apps, err := m.ListPreviewsForAccount(t.Context(), "acc-1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("len = %d, want 0 (deleted preview must be filtered)", len(apps))
	}
}

func TestListPreviewsForAccount_Mixed_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	// Production app (PreviewOfSlug == ""): excluded.
	prod := seedApp_Mega5(m, "prod-1", "acc-1", "my-app")
	prod.PreviewOfSlug = ""
	(*m.Apps())["prod-1"] = prod
	// Other account's preview: excluded by AccountID filter.
	other := seedApp_Mega5(m, "prev-other", "acc-other", "my-app")
	other.PreviewOfSlug = "my-app"
	(*m.Apps())["prev-other"] = other
	// Deleted preview: excluded by Status filter.
	deleted := seedApp_Mega5(m, "prev-del", "acc-1", "my-app")
	deleted.PreviewOfSlug = "my-app"
	deleted.Status = state.AppDeleted
	(*m.Apps())["prev-del"] = deleted
	// Live preview: included.
	live := seedApp_Mega5(m, "prev-live", "acc-1", "my-app")
	live.PreviewOfSlug = "my-app"
	(*m.Apps())["prev-live"] = live

	apps, err := m.ListPreviewsForAccount(t.Context(), "acc-1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(apps) != 1 {
		t.Fatalf("len = %d, want 1; got %+v", len(apps), apps)
	}
	if apps[0].ID != "prev-live" {
		t.Errorf("apps[0].ID = %q, want prev-live", apps[0].ID)
	}
}

func TestListPreviewsForAccount_SortCreatedAtDesc_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	older := seedApp_Mega5(m, "prev-old", "acc-1", "my-app")
	older.PreviewOfSlug = "my-app"
	older.CreatedAt = time.Now().Add(-1 * time.Hour)
	(*m.Apps())["prev-old"] = older
	newer := seedApp_Mega5(m, "prev-new", "acc-1", "my-app")
	newer.PreviewOfSlug = "my-app"
	newer.CreatedAt = time.Now()
	(*m.Apps())["prev-new"] = newer

	apps, err := m.ListPreviewsForAccount(t.Context(), "acc-1")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("len = %d, want 2", len(apps))
	}
	if apps[0].ID != "prev-new" {
		t.Errorf("apps[0] = %q, want prev-new (newer first)", apps[0].ID)
	}
	if apps[1].ID != "prev-old" {
		t.Errorf("apps[1] = %q, want prev-old", apps[1].ID)
	}
}

// --- PreviewAppsByParent (90% → 100%) -----------------------------

func TestPreviewAppsByParent_NoMatches_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	// Seed a production app + an unrelated preview; both are filtered
	// out, leaving the empty-result branch (the missing 10%).
	prod := seedApp_Mega5(m, "prod-1", "acc-1", "my-app")
	prod.PreviewOfSlug = ""
	(*m.Apps())["prod-1"] = prod
	other := seedApp_Mega5(m, "prev-other", "acc-1", "other-app")
	other.PreviewOfSlug = "other-app"
	(*m.Apps())["prev-other"] = other

	apps, err := m.PreviewAppsByParent(t.Context(), "acc-1", "my-app")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(apps) != 0 {
		t.Errorf("len = %d, want 0", len(apps))
	}
}

func TestPreviewAppsByParent_Happy_Mega5(t *testing.T) {
	t.Parallel()
	m := state.NewMemStore()
	p1 := seedApp_Mega5(m, "prev-1", "acc-1", "my-app")
	p1.PreviewOfSlug = "my-app"
	p1.CreatedAt = time.Now().Add(-time.Hour)
	(*m.Apps())["prev-1"] = p1
	p2 := seedApp_Mega5(m, "prev-2", "acc-1", "my-app")
	p2.PreviewOfSlug = "my-app"
	p2.CreatedAt = time.Now()
	(*m.Apps())["prev-2"] = p2

	apps, err := m.PreviewAppsByParent(t.Context(), "acc-1", "my-app")
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(apps) != 2 {
		t.Fatalf("len = %d, want 2", len(apps))
	}
	if apps[0].ID != "prev-2" {
		t.Errorf("apps[0] = %q, want prev-2 (newer first)", apps[0].ID)
	}
}
