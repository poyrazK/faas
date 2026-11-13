//go:build !no_pg

package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Hunt 34 H34-14: two deploys of one app 4 s apart. The first one's build
// creation updated its deployment, then (through the
// lifecycle_successor_deployments trigger) waited for the account row the
// second deploy held while it waited for that deployment. Postgres aborted
// the build creation and the deploy failed with "deadlock detected".
func TestPgCreateBuildWithIDWaitsForADeployHoldingTheAccount(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	acct, err := s.CreateAccount(ctx, "build-lock-order@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "build-lock-order", Status: state.AppActive, RAMMB: 256, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, SourcePath: "/var/spool/faas/lock-order.tar.gz", SourceBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	other, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = other.Rollback(ctx) }()
	if _, err := other.Exec(ctx, `select 1 from accounts where id=$1 for no key update`, acct.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := s.CreateBuildWithID(ctx, uuid.NewString(), dep.ID, state.DeploymentKindTarball, 1, "")
		done <- err
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		var waiting int
		if err := pool.QueryRow(ctx, `select count(*) from pg_stat_activity where datname=current_database() and wait_event_type='Lock'`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("build creation never waited for the account the other deploy holds")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if _, err := other.Exec(ctx, `select 1 from deployments where id=$1 for update`, dep.ID); err != nil {
		t.Fatalf("the other deploy was aborted: %v", err)
	}
	if err := other.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatalf("CreateBuildWithID: %v", err)
	}
	got, err := s.DeploymentByID(ctx, dep.ID)
	if err != nil || got.Status != state.DeployBuilding {
		t.Fatalf("deployment = %s (%v), want building", got.Status, err)
	}
}
