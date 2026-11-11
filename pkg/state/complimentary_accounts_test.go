// adr: 20261111140000 — complimentary accounts: a paid plan without a
// subscription, optionally until an end date.
package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMemStoreComplimentaryAccounts(t *testing.T) {
	exerciseComplimentaryAccounts(t, state.NewMemStore())
}

func TestPgStoreComplimentaryAccounts(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(context.Background(), pool); err != nil {
		t.Fatalf("db.MigrateUp: %v", err)
	}
	exerciseComplimentaryAccounts(t, state.NewPgStore(pool))
}

func exerciseComplimentaryAccounts(t *testing.T, store contractTestStore) {
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, "comp@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateAccount(ctx, "comp-forever@example.com", api.PlanFree)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)
	for _, bad := range []state.ComplimentaryAccount{
		{AccountID: acct.ID, Plan: api.PlanFree, Reason: "partner", GrantedBy: other.ID, GrantedAt: at},
		{AccountID: acct.ID, Plan: api.PlanPro, Reason: "Partner!", GrantedBy: other.ID, GrantedAt: at},
		{AccountID: acct.ID, Plan: api.PlanPro, Reason: "partner", GrantedBy: other.ID, GrantedAt: at, ExpiresAt: at},
		{AccountID: "missing", Plan: api.PlanPro, Reason: "partner", GrantedBy: other.ID, GrantedAt: at},
	} {
		if err := store.SetComplimentaryAccount(ctx, bad); err == nil {
			t.Fatalf("SetComplimentaryAccount(%+v) accepted", bad)
		}
	}
	if c, err := store.ComplimentaryAccount(ctx, acct.ID); err != nil || c != nil {
		t.Fatalf("before set: %+v, %v", c, err)
	}
	until := at.Add(30 * 24 * time.Hour)
	if err := store.SetComplimentaryAccount(ctx, state.ComplimentaryAccount{AccountID: acct.ID, Plan: api.PlanPro,
		Reason: "partner", GrantedBy: other.ID, GrantedAt: at, ExpiresAt: until}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetComplimentaryAccount(ctx, state.ComplimentaryAccount{AccountID: other.ID, Plan: api.PlanScale,
		Reason: "owner", GrantedBy: acct.ID, GrantedAt: at}); err != nil {
		t.Fatal(err)
	}
	c, err := store.ComplimentaryAccount(ctx, acct.ID)
	if err != nil || c == nil || c.Plan != api.PlanPro || !c.ExpiresAt.Equal(until) {
		t.Fatalf("read back %+v, %v", c, err)
	}
	ids, err := store.ComplimentaryAccountIDs(ctx)
	if _, ok := ids[acct.ID]; err != nil || len(ids) != 2 || !ok {
		t.Fatalf("ids %v, %v", ids, err)
	}
	if expired, err := store.ExpiredComplimentaryAccounts(ctx, until.Add(-time.Second), 10); err != nil || len(expired) != 0 {
		t.Fatalf("before the end date: %+v, %v", expired, err)
	}
	expired, err := store.ExpiredComplimentaryAccounts(ctx, until, 10)
	if err != nil || len(expired) != 1 || expired[0].AccountID != acct.ID {
		t.Fatalf("at the end date: %+v, %v; want only the dated account", expired, err)
	}
	if err := store.ClearComplimentaryAccount(ctx, acct.ID); err != nil {
		t.Fatal(err)
	}
	if c, _ := store.ComplimentaryAccount(ctx, acct.ID); c != nil {
		t.Fatalf("after clear: %+v", c)
	}
}
