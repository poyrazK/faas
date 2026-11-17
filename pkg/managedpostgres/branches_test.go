// adr: 20261106103654 — a branch is a restore from the latest point apid can
// name, or a chosen one, that may carry a TTL; at its expiry its bindings are
// revoked and it is purged, and its source is never touched.
package managedpostgres

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// branchFixture is a slot fixture with room for a source and its branches,
// and a branch sweeper recording the purges it starts.
type branchFixture struct {
	slotFixture
	branches *BranchExpirySweeper
	purged   *[]Database
}

func newBranchFixture(t *testing.T) branchFixture {
	t.Helper()
	f := newSlotFixture(t)
	*f.limit = 5
	var purged []Database
	sweeper, err := NewBranchExpirySweeper(f.service, f.bindings, BranchExpirySweeperOptions{
		Purged: func(_ context.Context, database Database) { purged = append(purged, database) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return branchFixture{slotFixture: f, branches: sweeper, purged: &purged}
}

func (f branchFixture) source(t *testing.T) Database {
	t.Helper()
	source, err := f.service.Create(context.Background(), CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()})
	if err != nil || source.State != StateReady {
		t.Fatalf("Create = %+v, %v", source, err)
	}
	*f.clock = f.clock.Add(time.Minute)
	return source
}

func TestBranchFromLatestTakesThePointBeforeTheRequestAndAdoptsOnRepeat(t *testing.T) {
	f := newBranchFixture(t)
	ctx := context.Background()
	source := f.source(t)
	requested := *f.clock
	request := RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "pr-42", Latest: true, TTL: 2 * time.Hour}
	branch, err := f.service.Restore(ctx, request)
	if err != nil || branch.State != StateReady {
		t.Fatalf("Restore latest = %+v, %v", branch, err)
	}
	if want := requested.Add(-LatestRestoreLag); !branch.RestorePointInTime.Equal(want) {
		t.Fatalf("point in time = %v, want %v", branch.RestorePointInTime, want)
	}
	if want := requested.Add(2 * time.Hour); !branch.ExpiresAt.Equal(want) || branch.RestoreSourceDatabaseID != source.ID {
		t.Fatalf("branch = expires %v from %q, want expires %v from %q", branch.ExpiresAt, branch.RestoreSourceDatabaseID, want, source.ID)
	}
	// A retry seconds later names a later point; it adopts the same branch.
	*f.clock = f.clock.Add(10 * time.Second)
	again, err := f.service.Restore(ctx, request)
	if err != nil || again.ID != branch.ID {
		t.Fatalf("repeat latest = %+v, %v, want branch %s", again, err, branch.ID)
	}
	other := request
	other.TTL = time.Hour
	if _, err := f.service.Restore(ctx, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeat with another TTL = %v, want conflict", err)
	}
	other = request
	other.TTL = 0
	if _, err := f.service.Restore(ctx, other); !errors.Is(err, ErrConflict) {
		t.Fatalf("repeat without a TTL = %v, want conflict", err)
	}
	// An exact point keeps the exact-match rule.
	exact := RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "pr-42", PointInTime: f.clock.Add(-time.Second), TTL: 2 * time.Hour}
	if _, err := f.service.Restore(ctx, exact); !errors.Is(err, ErrConflict) {
		t.Fatalf("exact point on a latest branch = %v, want conflict", err)
	}
}

func TestBranchRequestsOutsideTheContractAreInvalid(t *testing.T) {
	f := newBranchFixture(t)
	ctx := context.Background()
	source := f.source(t)
	for name, request := range map[string]RestoreDatabaseRequest{
		"latest with a point": {PointInTime: f.clock.Add(-time.Second), Latest: true},
		"TTL under the floor": {Latest: true, TTL: MinBranchTTL - time.Second},
		"TTL over the cap":    {Latest: true, TTL: MaxBranchTTL + time.Second},
		"negative TTL":        {Latest: true, TTL: -time.Hour},
	} {
		request.AccountID, request.SourceDatabaseID, request.Name = "account-a", source.ID, "pr-bad"
		if _, err := f.service.Restore(ctx, request); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: Restore = %v, want invalid", name, err)
		}
	}
	if databases, err := f.service.List(ctx, "account-a"); err != nil || len(databases) != 1 {
		t.Fatalf("an invalid branch reserved a database: %d, %v", len(databases), err)
	}
}

// A database younger than the lag has no latest point yet; the refusal says
// to retry instead of naming a point before its creation.
func TestLatestBranchOfABrandNewDatabaseAsksForARetry(t *testing.T) {
	f := newBranchFixture(t)
	ctx := context.Background()
	source, err := f.service.Create(ctx, CreateRequest{AccountID: "account-a", Name: "orders", Spec: testSpec()})
	if err != nil {
		t.Fatal(err)
	}
	*f.clock = f.clock.Add(time.Second)
	_, err = f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "pr-1", Latest: true})
	if !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "retry in a few seconds") {
		t.Fatalf("latest branch of a new database = %v, want a retry hint", err)
	}
}

func TestBranchExpiryRevokesBindingsThenPurgesAndLeavesTheSource(t *testing.T) {
	f := newBranchFixture(t)
	ctx := context.Background()
	source := f.source(t)
	keep, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "keep", Latest: true})
	if err != nil {
		t.Fatal(err)
	}
	branch, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "pr-7", Latest: true, TTL: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.bindings.Create(ctx, CreateBindingRequest{AccountID: "account-a", DatabaseID: branch.ID, AppID: "app-a", Scope: "default", EnvironmentKey: "DATABASE_URL", Access: CredentialReadWrite}); err != nil {
		t.Fatalf("bind the branch: %v", err)
	}
	*f.clock = branch.ExpiresAt.Add(-time.Second)
	if summary, err := f.branches.Sweep(ctx); err != nil || summary.Expired != 0 {
		t.Fatalf("sweep before the expiry = %+v, %v", summary, err)
	}
	*f.clock = branch.ExpiresAt
	for range 3 {
		if _, err := f.branches.Sweep(ctx); err != nil {
			t.Fatalf("sweep at the expiry: %v", err)
		}
	}
	if len(*f.purged) != 1 || (*f.purged)[0].ID != branch.ID {
		t.Fatalf("purge hook = %+v, want branch %s once", *f.purged, branch.ID)
	}
	gone, err := f.store.Get(ctx, "account-a", branch.ID)
	if err != nil || gone.State != StateDeleted {
		t.Fatalf("expired branch = %+v, %v, want deleted", gone, err)
	}
	bindings, err := f.bindings.List(ctx, "account-a", branch.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range bindings {
		if binding.State != BindingStateDeleted {
			t.Fatalf("the branch's binding survived its purge: %+v", binding)
		}
	}
	for _, id := range []string{source.ID, keep.ID} {
		if now, err := f.service.Get(ctx, "account-a", id); err != nil || now.State != StateReady {
			t.Fatalf("database %s was touched: %+v, %v", id, now, err)
		}
	}
	if summary, err := f.branches.Sweep(ctx); err != nil || summary.Expired != 0 {
		t.Fatalf("sweep after the purge = %+v, %v", summary, err)
	}
}

// The TTL asked for the branch to end, so a customer's scheduled delete does
// not keep it on the plan's count for the undo window.
func TestBranchExpiryPurgesABranchWhoseDeleteIsScheduled(t *testing.T) {
	f := newBranchFixture(t)
	ctx := context.Background()
	source := f.source(t)
	branch, err := f.service.Restore(ctx, RestoreDatabaseRequest{AccountID: "account-a", SourceDatabaseID: source.ID, Name: "pr-8", Latest: true, TTL: MinBranchTTL})
	if err != nil {
		t.Fatal(err)
	}
	if scheduled, err := f.service.ScheduleDelete(ctx, "account-a", branch.ID); err != nil || scheduled.PurgeAfter.IsZero() {
		t.Fatalf("ScheduleDelete = %+v, %v", scheduled, err)
	}
	*f.clock = branch.ExpiresAt.Add(time.Minute)
	if summary, err := f.branches.Sweep(ctx); err != nil || summary.Purged != 1 {
		t.Fatalf("sweep = %+v, %v, want one purge", summary, err)
	}
	if gone, err := f.store.Get(ctx, "account-a", branch.ID); err != nil || gone.State != StateDeleted {
		t.Fatalf("scheduled branch after its expiry = %+v, %v, want deleted", gone, err)
	}
}

func TestReservationsAdmitAnExpiryOnlyOnARestoreInsideTheCap(t *testing.T) {
	created := time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		database Database
		ok       bool
	}{
		"no expiry":             {Database{CreatedAt: created}, true},
		"restore with expiry":   {Database{CreatedAt: created, RestoreSourceDatabaseID: "source", ExpiresAt: created.Add(time.Hour)}, true},
		"expiry on a create":    {Database{CreatedAt: created, ExpiresAt: created.Add(time.Hour)}, false},
		"expiry at creation":    {Database{CreatedAt: created, RestoreSourceDatabaseID: "source", ExpiresAt: created}, false},
		"expiry past the cap":   {Database{CreatedAt: created, RestoreSourceDatabaseID: "source", ExpiresAt: created.Add(MaxBranchTTL + time.Second)}, false},
		"expiry exactly at cap": {Database{CreatedAt: created, RestoreSourceDatabaseID: "source", ExpiresAt: created.Add(MaxBranchTTL)}, true},
	} {
		if got := validReservationExpiry(tc.database); got != tc.ok {
			t.Errorf("%s: validReservationExpiry = %t, want %t", name, got, tc.ok)
		}
	}
}

// The catalog keeps microseconds; a replay compares the TTL within that
// rounding and no further.
func TestBranchExpiryMatchesAllowsOnlyCatalogRounding(t *testing.T) {
	created := time.Date(2026, 11, 6, 10, 0, 0, 123456000, time.UTC)
	database := Database{CreatedAt: created, ExpiresAt: created.Add(time.Hour + 400*time.Microsecond)}
	if !branchExpiryMatches(database, time.Hour) {
		t.Fatal("sub-millisecond rounding was treated as another TTL")
	}
	if branchExpiryMatches(database, time.Hour+time.Second) || branchExpiryMatches(database, 0) || branchExpiryMatches(Database{CreatedAt: created}, time.Hour) {
		t.Fatal("a different TTL matched")
	}
	if !branchExpiryMatches(Database{CreatedAt: created}, 0) {
		t.Fatal("no TTL on both sides did not match")
	}
}
