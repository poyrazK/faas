//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgAppConfigUpdateRetriesDeadlockVictim reproduces Hunt 31 H31-10: an
// app update lost a deadlock to a concurrent cron create in the same account
// and the customer got 503 "could not update app". The cron insert holds a
// key-share lock on the account's namespace (its foreign key) and then locks
// the account (fence_deployment_cron_intent); the app update locks the account,
// then the namespace. The update waits first, Postgres aborts it, and it must
// start again instead of failing.
func TestPgAppConfigUpdateRetriesDeadlockVictim(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "app-config-deadlock@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "app-config-deadlock", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}

	// The cron side holds the namespace row first, as its foreign key check does.
	cron, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cron.Rollback(ctx) }()
	if _, err := cron.Exec(ctx, `select 1 from resource_namespaces where id = $1 for key share`, acct.ID); err != nil {
		t.Fatal(err)
	}

	idle := 120
	done := make(chan error, 1)
	go func() {
		_, err := s.UpdateApp(ctx, app.ID, state.UpdateAppParams{IdleTimeoutS: &idle, SetIdleTimeout: true})
		done <- err
	}()

	// Wait until the update holds the account row and is blocked on the namespace.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked int
		if err := pool.QueryRow(ctx, `select count(*) from pg_stat_activity
			where wait_event_type = 'Lock' and query like '%LockAppConfigAccount%'`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the app update never blocked on the namespace row")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Close the cycle: the cron trigger now locks the account.
	if _, err := cron.Exec(ctx, `select 1 from accounts where id = $1 for update`, acct.ID); err != nil {
		t.Fatalf("cron side lost the deadlock instead of the app update: %v", err)
	}
	if err := cron.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("app update after a deadlock = %v, want it retried to success", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("app update did not finish")
	}
	got, err := s.AppByID(ctx, app.ID)
	if err != nil || got.IdleTimeoutS != idle {
		t.Fatalf("app idle timeout = %d, %v; want %d", got.IdleTimeoutS, err, idle)
	}
}
