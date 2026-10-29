package conformance

import (
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// testLogSavedSearches pins ADR-20261018083536 slice D's saved searches:
// upsert by name, the per-app cap that a replace never counts against,
// tenant-scoped reads, and not-found on a missing name.
func testLogSavedSearches(t *testing.T, fx *Fixture) {
	t.Helper()
	put := func(name, query string, limit int) (state.LogSavedSearch, error) {
		return fx.Store.PutLogSavedSearch(fx.Ctx, state.LogSavedSearch{AccountID: fx.Account.ID, AppID: fx.App.ID, Name: name, Query: query}, limit)
	}
	first, err := put("checkout-5xx", "route=%2Fcheckout&status=5xx", 2)
	if err != nil || first.ID == "" || first.Name != "checkout-5xx" || first.Query != "route=%2Fcheckout&status=5xx" || first.CreatedAt.IsZero() {
		t.Fatalf("create = %+v, %v", first, err)
	}
	if _, err := put("db-errors", "level=error&q=db", 2); err != nil {
		t.Fatalf("second create: %v", err)
	}
	var quota *state.LogSavedSearchQuotaError
	if _, err := put("third", "level=warn", 2); !errors.As(err, &quota) || quota.Limit != 2 || quota.Observed != 2 {
		t.Fatalf("create past the cap = %v, want a quota error 2/2", err)
	}
	replaced, err := put("checkout-5xx", "route=%2Fcheckout&status=502", 2)
	if err != nil || replaced.ID != first.ID || replaced.Query != "route=%2Fcheckout&status=502" || replaced.UpdatedAt.Before(first.UpdatedAt) {
		t.Fatalf("replace at the cap = %+v, %v; want the same row with the new query", replaced, err)
	}
	if _, err := put("Bad Name", "level=warn", 5); err == nil {
		t.Fatal("a name outside the stored shape was accepted")
	}

	list, err := fx.Store.ListLogSavedSearches(fx.Ctx, fx.Account.ID, fx.App.ID)
	if err != nil || len(list) != 2 || list[0].Name != "checkout-5xx" || list[1].Name != "db-errors" {
		t.Fatalf("list = %+v, %v; want both by name", list, err)
	}
	if other, err := fx.Store.ListLogSavedSearches(fx.Ctx, uuid.NewString(), fx.App.ID); err != nil || len(other) != 0 {
		t.Fatalf("another account's list = %+v, %v; want none", other, err)
	}
	got, err := fx.Store.LogSavedSearchByName(fx.Ctx, fx.Account.ID, fx.App.ID, "db-errors")
	if err != nil || got.Query != "level=error&q=db" {
		t.Fatalf("by name = %+v, %v", got, err)
	}
	if _, err := fx.Store.LogSavedSearchByName(fx.Ctx, uuid.NewString(), fx.App.ID, "db-errors"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account's read = %v, want ErrNotFound", err)
	}
	if err := fx.Store.DeleteLogSavedSearch(fx.Ctx, fx.Account.ID, fx.App.ID, "db-errors"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := fx.Store.DeleteLogSavedSearch(fx.Ctx, fx.Account.ID, fx.App.ID, "db-errors"); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
	if _, err := put("third", "level=warn", 2); err != nil {
		t.Fatalf("create after a delete freed a slot: %v", err)
	}
}
