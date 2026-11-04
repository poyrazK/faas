//go:build !no_pg

package state_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgBackgroundWorkUsesItsOwnPool pins the apid pool split (Hunt 31):
// queries and transactions under a WithBackgroundWork context run on the
// background pool, and request work stays on the primary pool.
func TestPgBackgroundWorkUsesItsOwnPool(t *testing.T) {
	_, primary, ctx := pgStoreWithPool(t)
	background, err := pgxpool.NewWithConfig(ctx, primary.Config())
	if err != nil {
		t.Fatal(err)
	}
	defer background.Close()
	s := state.NewPgStore(primary, state.WithBackgroundPool(background))
	acct, err := s.CreateAccount(ctx, "background-pool@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}

	acquires := func(p *pgxpool.Pool) int64 { return p.Stat().AcquireCount() }
	beforePrimary, beforeBackground := acquires(primary), acquires(background)
	if _, err := s.AccountByID(state.WithBackgroundWork(ctx), acct.ID); err != nil {
		t.Fatal(err)
	}
	if got := acquires(background) - beforeBackground; got == 0 {
		t.Fatal("background read did not use the background pool")
	}
	if got := acquires(primary) - beforePrimary; got != 0 {
		t.Fatalf("background read took %d primary connections, want 0", got)
	}

	beforePrimary, beforeBackground = acquires(primary), acquires(background)
	if _, err := s.AccountByID(context.WithoutCancel(ctx), acct.ID); err != nil {
		t.Fatal(err)
	}
	if got := acquires(primary) - beforePrimary; got == 0 {
		t.Fatal("request read did not use the primary pool")
	}
	if got := acquires(background) - beforeBackground; got != 0 {
		t.Fatalf("request read took %d background connections, want 0", got)
	}

	// A transaction follows its context too.
	beforeBackground = acquires(background)
	if err := s.CheckOrgAccess(state.WithBackgroundWork(ctx)); err != nil {
		t.Fatal(err)
	}
	if acquires(background) == beforeBackground {
		t.Fatal("background transaction did not use the background pool")
	}
}
