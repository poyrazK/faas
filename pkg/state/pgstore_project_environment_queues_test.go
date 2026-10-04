//go:build !no_pg

// adr: 531
package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgEnvironmentQueueSettingsAreIsolatedAndPinned(t *testing.T) {
	store, _, _ := pgWithPool(t)
	testEnvironmentQueueSettings(t, store)
}

func TestPgCloneQueueConfigurationIsFrozenAndLeased(t *testing.T) {
	store, ctx, pool := pgWithPool(t)
	account, err := store.CreateAccount(ctx, "queue-capture@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "queue-capture"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "queue-capture", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	deploy := func(scope string) {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope, Kind: state.DeploymentKindImage, ImageDigest: "sha256:queue"})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.SetDeploymentRootfs(ctx, d.ID, "/queue.ext4", "layers/queue-"+d.ID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, d.ID); err != nil {
			t.Fatal(err)
		}
	}
	deploy("production")
	prod, err := store.CreateQueueBinding(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: false, MaxConcurrency: 3, RetryPolicyJSON: []byte(`{"max_attempts":4,"base_seconds":2,"max_seconds":20,"jitter_seconds":0.5}`)})
	if err != nil {
		t.Fatal(err)
	}
	request := state.ProjectEnvironmentCloneCaptureRequest{AccountID: account.ID, ProjectID: project.ID, SourceEnvironment: "production", TargetEnvironment: "stage", IdempotencyKey: "queue-capture"}
	op, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimNextProjectEnvironmentClone(ctx, uuid.NewString(), time.Minute)
	if err != nil || lease.Operation.ID != op.ID {
		t.Fatalf("queue capture lease: %+v, %v", lease, err)
	}
	captures, err := store.ProjectEnvironmentCloneQueuesForLease(ctx, lease)
	if err != nil || len(captures) != 1 {
		t.Fatalf("queue captures: %+v, %v", captures, err)
	}
	capture := captures[0]
	if capture.OperationID != op.ID || len(capture.Hash) != 64 || capture.Definitions.EnvironmentOwned || len(capture.Definitions.Bindings) != 1 {
		t.Fatalf("queue catalogue identity: %+v", capture)
	}
	binding := capture.Definitions.Bindings[0]
	if binding.SourceID != prod.ID || binding.Name != "orders" || binding.QueueName != "orders" || binding.Mode != "push" || binding.Enabled || binding.MaxConcurrency != 3 || binding.WorkloadClass != state.WorkloadClassWorker || string(binding.RetryPolicyJSON) != `{"max_attempts":4,"base_seconds":2,"max_seconds":20,"jitter_seconds":0.5}` {
		t.Fatalf("incomplete queue definition: %+v", binding)
	}
	concurrency := 9
	if _, err := store.UpdateQueueBinding(ctx, account.ID, app.ID, prod.ID, state.UpdateQueueBindingParams{MaxConcurrency: &concurrency}); err != nil {
		t.Fatal(err)
	}
	if replay, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, request); err != nil || replay.ID != op.ID || replay.SourceRevisionHash != op.SourceRevisionHash {
		t.Fatalf("queue replay recaptured source: %+v, %v", replay, err)
	}
	if frozen, err := store.ProjectEnvironmentCloneQueuesForLease(ctx, lease); err != nil || !reflect.DeepEqual(frozen, captures) {
		t.Fatalf("lease read live queue configuration: %+v, %v", frozen, err)
	}
	captures[0].Definitions.Bindings[0].RetryPolicyJSON[0] = '!'
	if frozen, err := store.ProjectEnvironmentCloneQueuesForLease(ctx, lease); err != nil || frozen[0].Definitions.Bindings[0].RetryPolicyJSON[0] != '{' {
		t.Fatalf("returned catalogue aliased storage: %+v, %v", frozen, err)
	}
	wrong := lease
	wrong.Token = uuid.NewString()
	if _, err := store.ProjectEnvironmentCloneQueuesForLease(ctx, wrong); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("foreign lease read catalogue: %v", err)
	}
	changed := request
	changed.TargetEnvironment, changed.IdempotencyKey = "changed-queue", "changed-queue"
	if newer, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, changed); err != nil || newer.SourceRevisionHash == op.SourceRevisionHash {
		t.Fatalf("queue edit missing from source revision: %+v, %v", newer, err)
	}
	// A source stage with a complete pinned book ignores current production.
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "source-stage"}); err != nil {
		t.Fatal(err)
	}
	desired, err := state.UpsertEnvironmentQueueBinding(ctx, store, app, "source-stage", nil, state.ProjectEnvironmentQueueDefinition{Name: "isolated", QueueName: "orders", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	deploy("source-stage")
	if _, err := state.DeleteEnvironmentQueueBinding(ctx, store, app, "source-stage", "isolated", &desired.Revision); err != nil {
		t.Fatal(err)
	}
	stageRequest := request
	stageRequest.SourceEnvironment, stageRequest.TargetEnvironment, stageRequest.IdempotencyKey = "source-stage", "from-stage", "from-stage"
	stageOp, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, stageRequest)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := pool.QueryRow(ctx, `SELECT snapshot FROM project_environment_clone_workloads WHERE operation_id=$1 AND app_id=$2`, stageOp.ID, app.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Settings state.ProjectEnvironmentWorkloadSettings `json:"settings"`
		Policies struct {
			Queues state.ProjectEnvironmentCloneQueueDefinitions `json:"queues"`
		} `json:"policies"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if !snapshot.Policies.Queues.EnvironmentOwned || len(snapshot.Policies.Queues.Bindings) != 1 || snapshot.Policies.Queues.Bindings[0].SourceID != "" || !reflect.DeepEqual(snapshot.Settings.QueueBindings, desired.Settings.QueueBindings) {
		t.Fatalf("stage capture used desired/production queues: %+v", snapshot)
	}
	// The next deployment pins the complete empty collection left by deletion.
	// It cannot reacquire a production queue with the same logical name.
	deploy("source-stage")
	emptyRequest := stageRequest
	emptyRequest.TargetEnvironment, emptyRequest.IdempotencyKey = "empty-stage", "empty-stage"
	emptyOp, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, emptyRequest)
	if err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT snapshot FROM project_environment_clone_workloads WHERE operation_id=$1 AND app_id=$2`, emptyOp.ID, app.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil || !snapshot.Policies.Queues.EnvironmentOwned || snapshot.Policies.Queues.Bindings == nil || len(snapshot.Policies.Queues.Bindings) != 0 || snapshot.Settings.QueueBindings.Bindings == nil || len(snapshot.Settings.QueueBindings.Bindings) != 0 {
		t.Fatalf("empty stage inherited production queues: %+v, %v", snapshot, err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: account.ID, ProjectID: project.ID, Slug: "legacy-stage"}); err != nil {
		t.Fatal(err)
	}
	deploy("legacy-stage")
	invalid := stageRequest
	invalid.SourceEnvironment, invalid.TargetEnvironment, invalid.IdempotencyKey = "legacy-stage", "legacy-target", "legacy-target"
	if _, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, invalid); !errors.Is(err, state.ErrProjectEnvironmentQueueCollectionUnavailable) {
		t.Fatalf("uninitialized stage adopted production queues: %v", err)
	}
	if _, err := store.ProjectEnvironmentCloneOperationByIdempotencyKey(ctx, account.ID, project.ID, invalid.IdempotencyKey); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("failed stage queue capture left an operation: %v", err)
	}
	foreign, err := store.CreateAccount(ctx, "foreign-queue-owner@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE queue_bindings SET account_id=$2 WHERE id=$1`, prod.ID, foreign.ID); err != nil {
		t.Fatal(err)
	}
	invalid = request
	invalid.TargetEnvironment, invalid.IdempotencyKey = "foreign-owner", "foreign-owner"
	if _, err := store.CreateCapturedProjectEnvironmentCloneOperation(ctx, invalid); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("foreign queue owner entered capture: %v", err)
	}
	if _, err := store.ProjectEnvironmentCloneOperationByIdempotencyKey(ctx, account.ID, project.ID, invalid.IdempotencyKey); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("foreign queue ownership left an operation: %v", err)
	}
	if frozen, err := store.ProjectEnvironmentCloneQueuesForLease(ctx, lease); err != nil || len(frozen) != 1 || frozen[0].Hash != capture.Hash || frozen[0].Definitions.Bindings[0].MaxConcurrency != 3 {
		t.Fatalf("committed capture depended on current queue ownership: %+v, %v", frozen, err)
	}
	// Damaged capture proof never causes a worker to recapture live rows.
	if err := pool.QueryRow(ctx, `SELECT snapshot FROM project_environment_clone_workloads WHERE operation_id=$1 AND app_id=$2`, op.ID, app.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `UPDATE project_environment_clone_workloads SET snapshot=jsonb_set(snapshot::jsonb,'{policies,queues,bindings,0,max_concurrency}','99'::jsonb)::json WHERE operation_id=$1 AND app_id=$2`, op.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectEnvironmentCloneQueuesForLease(ctx, lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("tampered queue configuration was consumed: %v", err)
	}
	if _, err := pool.Exec(ctx, `UPDATE project_environment_clone_workloads SET snapshot=($3::jsonb #- '{policies,queues}')::json WHERE operation_id=$1 AND app_id=$2`, op.ID, app.ID, raw); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ProjectEnvironmentCloneQueuesForLease(ctx, lease); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("missing queue catalogue fell back to production: %v", err)
	}
}
