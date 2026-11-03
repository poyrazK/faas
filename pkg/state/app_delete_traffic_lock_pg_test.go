//go:build !no_pg

package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgAppDeleteWaitsBehindTheAccountTrafficLock reproduces Hunt 31 H31-13:
// a delete waits for the account's traffic binding lock while a deploy in the
// same account holds it. Under apid's 5 s default budget it gave up and the
// customer got 503 "could not delete app"; delete now runs under the 30 s
// budget deploys already have.
func TestPgAppDeleteWaitsBehindTheAccountTrafficLock(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "delete-traffic-lock@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "delete-traffic-lock", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 1, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	holder, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	key := "gregale.traffic.account.v1:" + acct.ID
	var locked bool
	if err := holder.QueryRow(ctx, `select pg_try_advisory_lock(hashtextextended($1, 0))`, key).Scan(&locked); err != nil || !locked {
		t.Fatalf("hold account traffic lock = %v, %v", locked, err)
	}

	short, cancel := context.WithTimeout(ctx, api.RequestBudgetApidDefault)
	_, err = s.ScheduleAppDeletion(short, app.ID, time.Time{})
	cancel()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("delete under the 5 s budget behind a held account lock = %v, want the deadline (production's 503)", err)
	}

	go func() {
		time.Sleep(time.Second)
		_, _ = holder.Exec(context.WithoutCancel(ctx), `select pg_advisory_unlock(hashtextextended($1, 0))`, key)
	}()
	long, cancel := context.WithTimeout(ctx, api.RequestBudgetMax)
	defer cancel()
	deleted, err := s.ScheduleAppDeletion(long, app.ID, time.Time{})
	if err != nil || deleted.Status != state.AppDeleted {
		t.Fatalf("delete under the 30 s budget once the lock frees = %s, %v; want deleted", deleted.Status, err)
	}
}
