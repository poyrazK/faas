//go:build !no_pg

// adr: 20261026201726
package state_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// A deploy that removes a cron with a long run history detaches the history
// in batches before its transaction, instead of letting one DELETE detach
// every run inline until the statement times out (H9-165), and cancels the
// cron's runs that have not started (crons audit 2026-11-05).
func TestDeploymentCronRemovalDetachesLongHistoryPG(t *testing.T) {
	store, pool, ctx := pgStoreWithPool(t)
	acct, err := store.CreateAccount(ctx, "cron-removal@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "cron-removal"})
	if err != nil {
		t.Fatal(err)
	}
	cron, err := store.CreateCron(ctx, app.ID, "* * * * *", "/tick", true)
	if err != nil {
		t.Fatal(err)
	}
	cronID := cron.ID
	pending, err := store.EnqueueInvocation(ctx, state.Invocation{
		AppID: app.ID, AccountID: acct.ID, CronID: &cronID, Source: state.InvocationCron,
		Method: "POST", Path: "/tick", DueAt: time.Now().UTC().Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Settled history past the inline bound, copied from the pending run.
	if _, err := pool.Exec(ctx, `
		insert into invocations (app_id, account_id, source, method, path, cron_id, state, due_at, created_at, completed_at)
		select app_id, account_id, source, method, path, cron_id, 'completed', due_at, now(), now()
		  from invocations, generate_series(1, 1100)
		 where id = $1`, pending.ID); err != nil {
		t.Fatal(err)
	}
	snapshot, err := store.DeploymentCronStagingSnapshot(ctx, acct.ID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := store.ApplyDeploymentCronStaging(ctx, app.ID, state.DeploymentCronStagingParams{
		AccountID: acct.ID, Limits: api.MustLimitsFor(api.PlanPro),
		Request: api.DeploymentCronStagingRequest{
			SessionID: strings.Repeat("d", 32), Operation: "stage", ExpectedAppID: app.ID,
			ExpectedDeploymentID: snapshot.LatestDeploymentID, ExpectedRevision: snapshot.Revision,
			Before: snapshot.Crons, Desired: []api.DeploymentCronConfig{},
		},
	})
	if err != nil || !staged.Changed {
		t.Fatalf("stage removal = %+v err=%v", staged, err)
	}
	if _, err := store.CronByID(ctx, cron.ID); err == nil {
		t.Fatal("removed cron still exists")
	}
	var attached, completed int
	if err := pool.QueryRow(ctx, `select count(*) filter (where cron_id is not null), count(*) filter (where state = 'completed')
		from invocations where app_id = $1`, app.ID).Scan(&attached, &completed); err != nil {
		t.Fatal(err)
	}
	if attached != 0 || completed != 1100 {
		t.Fatalf("after removal attached=%d completed=%d, want 0 and 1100", attached, completed)
	}
	if got, err := store.InvocationByID(ctx, pending.ID); err != nil || got.State != state.InvocationCancelled {
		t.Fatalf("removed cron's pending run = %+v err=%v, want cancelled", got, err)
	}
}
