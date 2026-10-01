//go:build !no_pg

package migrations_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestEnvironmentGitOpsMigrationsReplayRetainsIdentityAndLeases(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "gitops-replay-identity@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "replay-project", RepoFullName: "example/replay", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{
		RepositoryID: 123, InstallationID: 42, Repository: "example/replay", Ref: "refs/heads/main", ManifestPath: "environments/production.yaml", Mode: "report", ApprovalPolicy: "manual",
	})
	if err != nil {
		t.Fatal(err)
	}
	token := uuid.NewString()
	var nextPoll, leaseUntil time.Time
	if err := pool.QueryRow(ctx, `update environment_git_source_polls set next_poll_at=now()+interval '2 hours',lease_token=$2,lease_until=now()+interval '1 hour'
  where source_id=$1 returning next_poll_at,lease_until`, source.ID, token).Scan(&nextPoll, &leaseUntil); err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "replay-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "original", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationQueue, QueueName: "orders", DeploymentScope: "staging", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.InsertTriggerRecord(ctx, binding.Changes[0].TriggerID, pinned.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	unassigned, err := store.EnqueueInvocation(ctx, state.Invocation{AppID: app.ID, AccountID: account.ID, Source: state.InvocationQueue, QueueName: "review-me", DeploymentScope: "staging", DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	review, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "review-me", QueueName: "review-me", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	var legacyTrigger string
	if err := pool.QueryRow(ctx, `insert into triggers(account_id,app_id,kind,source,slug,enabled,config)
  values($1,$2,'queue','queue','review-me',false,jsonb_build_object('mode','queue','queue_binding_id',$3::text)) returning id`,
		account.ID, app.ID, review.ID).Scan(&legacyTrigger); err != nil {
		t.Fatal(err)
	}
	name := "payments"
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, binding.Binding.ID, state.UpdateQueueBindingParams{QueueName: &name}); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteQueueBinding(ctx, account.ID, app.ID, binding.Binding.ID); err != nil {
		t.Fatal(err)
	}
	// Membership changes cannot reinterpret the environment already accepted.
	if _, err := pool.Exec(ctx, `update apps set project_id=$2 where id=$1`, app.ID, project.ID); err != nil {
		t.Fatal(err)
	}
	scoped, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, DeploymentScope: "production", Name: "scoped", QueueName: "scoped", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	scopedWork, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "scoped", DueAt: time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	scopedClaim, err := store.ClaimQueueTriggerInvocation(ctx, scopedWork.ID, scoped.Changes[0].TriggerID, app.ID, "scoped", 60)
	if err != nil {
		t.Fatal(err)
	}
	versions := []int64{
		20260930183000001, 20260930183000002, 20260930183000003, 20260930183000004, 20260930183000005, 20260930183000006,
		20261001010000001, 20261001020000001, 20261001020000002, 20261001030000001, 20261001040000001, 20261001050000001,
		20261001060000001, 20261001070000001, 20261001070000002, 20261001080000001, 20261001080000002, 20261001080000003, 20261001081007501,
		20261001094704872, 20261001110831601,
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=any($1::bigint[])`, versions); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	retained, err := store.InvocationByID(ctx, scopedWork.ID)
	if err != nil || retained.QueueBindingID != scoped.Binding.ID || retained.DeploymentScope != "production" || retained.State != state.InvocationDispatching || retained.Attempts != scopedClaim.Attempts || retained.LeaseExpiresAt == nil || !retained.LeaseExpiresAt.Equal(*scopedClaim.LeaseExpiresAt) {
		t.Fatalf("full replay changed scoped claim: %+v %v", retained, err)
	}
	scopedConsumer, err := store.TriggerByID(ctx, scoped.Changes[0].TriggerID)
	if err != nil || scopedConsumer.QueueBindingScope != "production" || scopedConsumer.QueueBindingEnvironmentID.String() != scoped.Binding.EnvironmentID {
		t.Fatalf("full replay lost scoped consumer: %+v %v", scopedConsumer, err)
	}
	for _, want := range []state.Invocation{pinned, unassigned} {
		row, err := store.InvocationByID(ctx, want.ID)
		if err != nil || row.QueueBindingID != want.QueueBindingID || row.QueueName != want.QueueName || row.DeploymentScope != "staging" || row.State != state.InvocationPending || row.Attempts != 0 {
			t.Fatalf("replay reinterpreted admission=%+v %v", row, err)
		}
	}
	trigger, err := store.TriggerByID(ctx, legacyTrigger)
	if err != nil || trigger.QueueBindingID.Valid {
		t.Fatalf("replay silently adopted a late marker=%+v %v", trigger, err)
	}
	var actualToken string
	var actualPoll, actualUntil time.Time
	if err := pool.QueryRow(ctx, `select lease_token::text,next_poll_at,lease_until from environment_git_source_polls where source_id=$1`, source.ID).Scan(&actualToken, &actualPoll, &actualUntil); err != nil || actualToken != token || !actualPoll.Equal(nextPoll) || !actualUntil.Equal(leaseUntil) {
		t.Fatalf("replay reset polling lease: token=%s poll=%v until=%v err=%v", actualToken, actualPoll, actualUntil, err)
	}
	if id, err := store.TriggerRecordIDByItemIdentifier(ctx, binding.Changes[0].TriggerID, pinned.ID); err != nil || id != receipt {
		t.Fatalf("replay lost delivery receipt=%q %v", id, err)
	}
	if _, err := store.ClaimInvocationWithCap(ctx, pinned.ID, "", 60, 10); !errors.Is(err, state.ErrQueueBindingRetired) {
		t.Fatalf("replay released retired ownership=%v", err)
	}
}
