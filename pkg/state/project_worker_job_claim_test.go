package state_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"testing"
	"time"
)

type workerJobEvidenceStore interface {
	BeginProjectWorkerJobEvidence(context.Context, state.Invocation) error
	FinishProjectWorkerJobEvidence(context.Context, state.Invocation, string) error
}

func TestProjectWorkerJobClaimMemory(t *testing.T) { testWorkerJobClaim(t, state.NewMemStore()) }
func testWorkerJobClaim(t *testing.T, store state.Store) {
	ctx := t.Context()
	account, err := store.CreateAccount(ctx, "worker-job-claim@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "job-claim"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, ProjectID: project.ID, Slug: "processor", WorkloadName: "processor", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, RAMMB: 256, MaxConcurrency: 5, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker, RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindTarball, ImageDigest: "sha256:baseline"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetDeploymentRootfs(ctx, dep.ID, "/baseline.ext4", "layers/"+dep.ID, 4096); err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.ProjectReleaseSetStore).PublishProjectReleaseSet(ctx, account.ID, project.ID, "production", 1800, []state.ProjectReleaseMember{{AppID: app.ID, DeploymentID: dep.ID}}); err != nil {
		t.Fatal(err)
	}
	binding, err := store.(state.QueueBindingConsumerStore).CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, DeploymentScope: "production", Name: "orders", QueueName: "orders", Mode: "push", Enabled: true, WorkloadClass: state.WorkloadClassWorker, MaxConcurrency: 2})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{Name: "job-node", TargetURL: "unix:///tmp/job.sock", VPCPUs: 4, MemMB: 4096, MaxConcurrency: 20, AdmissionCeilingMB: 4096, VCPUBudget: 4, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateColdBooting), 256, node.ID, uuid.NewString(), string(state.InstanceModeWorker))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateInstanceState(ctx, instance.ID, string(state.StateRunning)); err != nil {
		t.Fatal(err)
	}
	inv, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: account.ID, AppID: app.ID, Source: state.InvocationQueue, QueueBindingID: binding.Binding.ID, DeploymentScope: "production", QueueName: "orders", Payload: json.RawMessage(`{}`), DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	if claimer, ok := store.(interface {
		ClaimQueueTriggerInvocation(context.Context, string, string, string, string, int) (state.Invocation, error)
	}); ok {
		inv, err = claimer.ClaimQueueTriggerInvocation(ctx, inv.ID, binding.Changes[0].TriggerID, app.ID, "orders", 60)
	} else {
		inv, err = store.ClaimInvocation(ctx, inv.ID, instance.ID, 60)
	}
	if err != nil {
		t.Fatal(err)
	}
	inv.ResolvedDeploymentID = dep.ID
	inv.InstanceID = instance.ID
	evidence := store.(workerJobEvidenceStore)
	stale := inv
	stale.Attempts++
	if err := evidence.BeginProjectWorkerJobEvidence(ctx, stale); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale start: %v", err)
	}
	if err := evidence.BeginProjectWorkerJobEvidence(ctx, inv); err != nil {
		t.Fatal(err)
	}
	if err := evidence.BeginProjectWorkerJobEvidence(ctx, inv); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("duplicate delivery executes: %v", err)
	}
	stale = inv
	stale.ReplayGeneration++
	if err := evidence.FinishProjectWorkerJobEvidence(ctx, stale, "succeeded"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale finish: %v", err)
	}
	if err := evidence.FinishProjectWorkerJobEvidence(ctx, inv, "retry"); err != nil {
		t.Fatal(err)
	}
	if err := evidence.FinishProjectWorkerJobEvidence(ctx, inv, "succeeded"); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("failure overwritten: %v", err)
	}
}
