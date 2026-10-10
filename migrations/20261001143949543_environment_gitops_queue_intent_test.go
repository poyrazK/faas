//go:build !no_pg

package migrations_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMigrationEnvironmentGitOpsQueueIntentReplayRetainsIdentity(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	account, err := store.CreateAccount(ctx, "git-queue-replay@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "queue-replay", RepoFullName: "example/queue", ProductionBranch: "main", InstallID: 42})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "queue-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", DeploymentScope: "production", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.CreateEnvironmentGitSource(ctx, account.ID, project.ID, "production", state.EnvironmentGitSourceSpec{RepositoryID: 123, InstallationID: 42, Repository: "example/queue", Ref: "refs/heads/main", ManifestPath: "production.yaml", Mode: "enforce", ApprovalPolicy: "manual"})
	if err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: project.Slug, Environment: "production", Workloads: map[string]api.EnvironmentWorkload{
		"worker": {App: app.Slug, QueueBindings: map[string]api.EnvironmentQueueBinding{"orders": {QueueName: "orders", Mode: "push", WorkloadClass: "worker"}}, QueueSmoke: map[string]api.EnvironmentQueueSmoke{"orders": {Payload: json.RawMessage(`{"id":"queue-replay-qualification"}`)}}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	source, _, err = store.ApproveEnvironmentDesiredRevision(ctx, state.ApproveEnvironmentRevision{AccountID: account.ID, SourceID: source.ID, ExpectedGeneration: source.Generation, Desired: desired, CommitSHA: strings.Repeat("a", 40), ApprovedBy: "owner"})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := store.PreviewEnvironmentGitOpsAdoption(ctx, account.ID, source.ID)
	if err != nil || !preview.CanApply() {
		t.Fatalf("preview: %+v %v", preview, err)
	}
	if err := store.AdoptEnvironmentGitOps(ctx, account.ID, source.ID, preview.Hash); err != nil {
		t.Fatal(err)
	}
	invocation, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueName: "orders", QueueBindingID: binding.Binding.ID, DeploymentScope: binding.Binding.DeploymentScope, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := store.InsertTriggerRecord(ctx, binding.Changes[0].TriggerID, invocation.ID, []byte(`{}`), []byte(`{}`), []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(ctx, "queue-replay", time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `delete from goose_db_version where version_id=20261001143949543`); err != nil {
		t.Fatal(err)
	}
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var pinned string
	if err := pool.QueryRow(ctx, `select binding_id from environment_gitops_queue_bindings where source_id=$1`, source.ID).Scan(&pinned); err != nil || pinned != binding.Binding.ID {
		t.Fatalf("ownership identity changed: %s %v", pinned, err)
	}
	if inv, err := store.InvocationByID(ctx, invocation.ID); err != nil || inv.QueueBindingID != pinned || inv.State != state.InvocationPending {
		t.Fatalf("accepted work changed: %+v %v", inv, err)
	}
	if id, err := store.TriggerRecordIDByItemIdentifier(ctx, binding.Changes[0].TriggerID, invocation.ID); err != nil || id != receipt {
		t.Fatalf("receipt changed: %s %v", id, err)
	}
	if err := store.RenewEnvironmentGitOps(ctx, lease, time.Now(), time.Minute); err != nil {
		t.Fatalf("replay fenced current lease: %v", err)
	}
	cap := 4
	if _, err := store.UpdateQueueBindingWithConsumer(ctx, account.ID, app.ID, pinned, state.UpdateQueueBindingParams{MaxConcurrency: &cap}); !errors.Is(err, state.ErrEnvironmentGitManaged) {
		t.Fatalf("replay released ownership: %v", err)
	}
}
