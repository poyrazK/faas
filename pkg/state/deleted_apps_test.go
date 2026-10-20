package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAccountDeletedInventoryIsolationPagesAndIdentity(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	owner, _ := m.CreateAccount(ctx, "deleted-owner@example.com", api.PlanPro)
	other, _ := m.CreateAccount(ctx, "deleted-other@example.com", api.PlanPro)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	deadline := time.Now().Add(time.Hour)
	for _, seed := range []struct{ id, slug, account string }{
		{"00000000-0000-4000-8000-000000000001", "deleted-first", owner.ID},
		{"00000000-0000-4000-8000-000000000002", "deleted-second", owner.ID},
		{"00000000-0000-4000-8000-000000000003", "deleted-third", owner.ID},
		{"00000000-0000-4000-8000-000000000004", "foreign-deleted", other.ID},
	} {
		app, err := m.CreateApp(ctx, state.App{ID: seed.id, Slug: seed.slug, AccountID: seed.account, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := m.ScheduleAppDeletion(ctx, app.ID, deadline); err != nil {
			t.Fatal(err)
		}
		(*m.Mu()).Lock()
		a := (*m.Apps())[app.ID]
		a.DeletedAt = &at
		(*m.Apps())[app.ID] = a
		(*m.Mu()).Unlock()
	}
	_, _ = m.CreateApp(ctx, state.App{Slug: "still-active", AccountID: owner.ID, Status: state.AppActive})
	first, err := m.ListAccountDeletedApps(ctx, owner.ID, time.Time{}, "", 2)
	if err != nil || len(first) != 2 || first[0].Slug != "deleted-third" || first[1].Slug != "deleted-second" {
		t.Fatalf("first page = %+v, %v", first, err)
	}
	// Removing the cursor row does not invalidate the tuple boundary.
	if _, err := m.RestoreApp(ctx, first[1].ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	next, err := m.ListAccountDeletedApps(ctx, owner.ID, first[1].SortAt, first[1].ID, 2)
	if err != nil || len(next) != 1 || next[0].Slug != "deleted-first" {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if _, err := m.AccountDeletedAppByID(ctx, other.ID, first[0].ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign ID lookup = %v", err)
	}
	if _, err := m.AccountDeletedAppByID(ctx, owner.ID, first[1].ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("restored ID lookup = %v", err)
	}
	for _, limit := range []int{0, 52} {
		if _, err := m.ListAccountDeletedApps(ctx, owner.ID, time.Time{}, "", limit); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("limit %d = %v", limit, err)
		}
	}
}

func TestAccountDeletedInventoryPreservesPurgeAndNullableMetadata(t *testing.T) {
	ctx := context.Background()
	m := state.NewMemStore()
	acct, _ := m.CreateAccount(ctx, "purge-inventory@example.com", api.PlanPro)
	app, _ := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "purge-inventory", Status: state.AppActive})
	deadline := time.Now().Add(-time.Hour)
	_, _ = m.ScheduleAppDeletion(ctx, app.ID, deadline)
	if err := m.ClaimAppDeletion(ctx, app.ID); err != nil {
		t.Fatal(err)
	}
	r, err := m.AccountDeletedAppByID(ctx, acct.ID, app.ID)
	if err != nil || !r.PurgeStarted || r.DeletedAt == nil || r.DeleteGraceUntil == nil || !r.DeleteGraceUntil.Equal(deadline) {
		t.Fatalf("record = %+v, %v", r, err)
	}
	// Legacy metadata is preserved as absent rather than inventing a deadline.
	legacy, _ := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "legacy-tombstone", Status: state.AppActive})
	(*m.Mu()).Lock()
	legacy.Status = state.AppDeleted
	legacy.DeletedAt, legacy.DeleteGraceUntil = nil, nil
	(*m.Apps())[legacy.ID] = legacy
	(*m.Mu()).Unlock()
	r, err = m.AccountDeletedAppByID(ctx, acct.ID, legacy.ID)
	if err != nil || r.DeletedAt != nil || r.DeleteGraceUntil != nil || !r.SortAt.Equal(legacy.CreatedAt) {
		t.Fatalf("legacy metadata = %+v, %v", r, err)
	}
}
