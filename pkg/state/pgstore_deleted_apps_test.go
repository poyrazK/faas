//go:build !no_pg

package state_test

import (
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAccountDeletedInventoryOwnershipAndTuplePagination(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "deleted-inventory-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateAccount(ctx, "other-inventory-"+uuid.NewString()+"@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Hour)
	ids := []string{"00000000-0000-4000-8000-000000000001", "00000000-0000-4000-8000-000000000002", "00000000-0000-4000-8000-000000000003"}
	for i, id := range ids {
		owner := acct.ID
		if i == 2 {
			owner = other.ID
		}
		app, err := s.CreateApp(ctx, state.App{ID: id, AccountID: owner, Slug: "deleted-inventory-" + id, RAMMB: 128, IdleTimeoutS: 30, MaxConcurrency: 1, Status: state.AppActive})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.ScheduleAppDeletion(ctx, app.ID, deadline); err != nil {
			t.Fatal(err)
		}
		ids[i] = app.ID
	}
	sort.Strings(ids[:2])
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := pool.Exec(ctx, "UPDATE apps SET deleted_at=$1", at); err != nil {
		t.Fatal(err)
	}
	first, err := s.ListAccountDeletedApps(ctx, acct.ID, time.Time{}, "", 1)
	if err != nil || len(first) != 1 || first[0].ID != ids[1] {
		t.Fatalf("first = %+v, %v", first, err)
	}
	if _, err := s.RestoreApp(ctx, first[0].ID, api.MustLimitsFor(api.PlanPro)); err != nil {
		t.Fatal(err)
	}
	next, err := s.ListAccountDeletedApps(ctx, acct.ID, first[0].SortAt, first[0].ID, 1)
	if err != nil || len(next) != 1 || next[0].ID != ids[0] {
		t.Fatalf("next = %+v, %v", next, err)
	}
	if _, err := s.AccountDeletedAppByID(ctx, other.ID, ids[0]); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign read = %v", err)
	}
	if _, err := s.AccountDeletedAppByID(ctx, acct.ID, ids[1]); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("restored read = %v", err)
	}
	if _, err := s.ScheduleAppDeletion(ctx, ids[0], time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	// Schedule preserves its first deadline: expire this fixture explicitly.
	if _, err := pool.Exec(ctx, "UPDATE apps SET delete_grace_until=now()-interval '1 hour' WHERE id=$1", ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := s.ClaimAppDeletion(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	got, err := s.AccountDeletedAppByID(ctx, acct.ID, ids[0])
	if err != nil || !got.PurgeStarted {
		t.Fatalf("purge claim = %+v, %v", got, err)
	}
}
