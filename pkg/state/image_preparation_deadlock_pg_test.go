//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPgImagePreparationTransitionRetriesDeadlockVictim reproduces Hunt 31
// H31-2: two deploys of one app failed one of them with "transition image
// preparation: deadlock detected". The transition holds the deployment row and
// waits for the account row (the lifecycle successor trigger), while a wake
// admission holds the account row and waits for the deployment row. Postgres
// aborts the transition, which must start again instead of failing the
// deployment.
func TestPgImagePreparationTransitionRetriesDeadlockVictim(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	var images state.DeploymentImagePreparationStore = s
	acct, err := s.CreateAccount(ctx, "image-deadlock@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "image-deadlock", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployBuilding})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDeploymentRootfs(ctx, dep.ID, "/source/image.tar", "builder/source", 11); err != nil {
		t.Fatal(err)
	}
	claim, err := images.BeginImagePreparation(ctx, dep.ID, "node-a")
	if err != nil {
		t.Fatal(err)
	}

	// The wake side holds the account row first.
	wake, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = wake.Rollback(ctx) }()
	if _, err := wake.Exec(ctx, `select 1 from accounts where id = $1 for no key update`, acct.ID); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() { done <- images.TransitionImagePreparation(ctx, dep.ID, claim.ClaimToken, state.DeployImaging) }()

	// Wait until the transition holds the deployment row and is blocked on the
	// account row, so it waits first and Postgres picks it as the victim.
	deadline := time.Now().Add(10 * time.Second)
	for {
		var blocked int
		if err := pool.QueryRow(ctx, `select count(*) from pg_stat_activity
			where wait_event_type = 'Lock' and query like '%TransitionImagePreparation%'`).Scan(&blocked); err != nil {
			t.Fatal(err)
		}
		if blocked > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the transition never blocked on the account row")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Close the cycle: the wake side now needs the deployment row, as the
	// instance insert's foreign key check does.
	if _, err := wake.Exec(ctx, `select 1 from deployments where id = $1 for key share`, dep.ID); err != nil {
		t.Fatalf("wake side lost the deadlock instead of the transition: %v", err)
	}
	if err := wake.Commit(ctx); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("transition after a deadlock = %v, want it retried to success", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("transition did not finish")
	}
	got, err := s.DeploymentByID(ctx, dep.ID)
	if err != nil || got.Status != state.DeployImaging {
		t.Fatalf("deployment = %+v, %v; want imaging", got.Status, err)
	}
}
