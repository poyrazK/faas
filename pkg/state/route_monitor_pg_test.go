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

func TestRoutePolicyMonitoringRollbackAndRestart(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, app, request := monitorFixture(t, store)
	hook := monitorHook(t, store, account.ID, app.ID, "restart", []string{"routes.requirements.violated"}, true)
	rule := monitorBudgetRule(t, store, app.ID)
	for _, commit := range []bool{false, true} {
		if err := store.QueueAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID); err != nil {
			t.Fatal(err)
		}
		claim, err := store.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
		if err != nil {
			t.Fatal(err)
		}
		check, err := store.CheckRouteRequirements(t.Context(), account.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: request.DeploymentID}, testSavedRequirementsChecker(request.DeploymentID))
		if err != nil {
			t.Fatal(err)
		}
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
		if _, err := tx.Exec(t.Context(), "DELETE FROM edge_rules WHERE id = $1", rule.ID); err != nil {
			t.Fatal(err)
		}
		type completion struct {
			done bool
			err  error
		}
		finished := make(chan completion, 1)
		go func() {
			done, err := store.CompleteAutomaticRouteCheck(t.Context(), claim, check, check.Report.Coverage.SHA256, false)
			finished <- completion{done, err}
		}()
		select {
		case result := <-finished:
			t.Fatalf("completion crossed an uncommitted policy edit: %+v", result)
		case <-time.After(100 * time.Millisecond):
		}
		if commit {
			err = tx.Commit(t.Context())
		} else {
			err = tx.Rollback(t.Context())
		}
		if err != nil {
			t.Fatal(err)
		}
		select {
		case result := <-finished:
			if result.err != nil || result.done == commit {
				t.Fatalf("transaction fencing commit=%v: %+v", commit, result)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("completion stalled after policy transaction")
		}
	}
	monitorComplete(t, store, "violated")
	monitorPendingEvents(t, store, 1)
	restarted := state.NewPgStore(pool)
	if n, err := restarted.DrainAppWebhookEventOutbox(t.Context(), 32); err != nil || n != 1 {
		t.Fatalf("restart lost notification: %d %v", n, err)
	}
	rows, _, err := restarted.ListAppWebhookDeliveries(t.Context(), app.ID, hook.ID, 100, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("restart delivery: %+v %v", rows, err)
	}
}

func TestRoutePolicyMonitoringCompletionAndChildInsert(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, app, request := monitorFixture(t, store)
	if err := store.QueueAutomaticRouteCheck(t.Context(), account.ID, app.ID, request.DeploymentID); err != nil {
		t.Fatal(err)
	}
	claim, err := store.ClaimAutomaticRouteCheck(t.Context(), api.RouteCheckClaimLease)
	if err != nil {
		t.Fatal(err)
	}
	check, err := store.CheckRouteRequirements(t.Context(), account.ID, app.ID, api.CheckRouteRequirementsRequest{DeploymentID: request.DeploymentID}, testSavedRequirementsChecker(request.DeploymentID))
	if err != nil {
		t.Fatal(err)
	}
	tx, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tx.Rollback(context.Background()) })
	// Configuration writes fence the account before the app so successor
	// invalidation and route-check completion share one lock order.
	if _, err := tx.Exec(t.Context(), "SELECT id FROM accounts WHERE id = $1 FOR UPDATE", account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(t.Context(), "SELECT id FROM apps WHERE id = $1 FOR UPDATE", app.ID); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() {
		done, err := store.CompleteAutomaticRouteCheck(t.Context(), claim, check, check.Report.Coverage.SHA256, false)
		if err == nil && done {
			err = state.ErrInvalidArgument
		}
		finished <- err
	}()
	// Wait for completion to reach the account fence before changing rules.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%LockRouteCheckCompletionAccount%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("completion did not wait on its account fence")
		}
		time.Sleep(10 * time.Millisecond)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	_, err = tx.Exec(ctx, `INSERT INTO edge_rules(account_id,app_id,match_host,match_path,match_methods,priority,kind,enabled,action)
		VALUES ($1,$2,$3,'/health',ARRAY['GET'],0,'budget',true,'{"budget":{"budget_ms":100}}')`, account.ID, app.ID, app.Slug+".gregale.dev")
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("completion/child insert: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("completion deadlocked with a child insert")
	}
}
