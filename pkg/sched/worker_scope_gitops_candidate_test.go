// adr: 568 — a held candidate's instance belongs to its qualification owner.
package sched_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// heldCandidateStore reports one deployment as a held environment-GitOps
// candidate, the shape a qualification instance boots from.
type heldCandidateStore struct {
	MemStore  *state.MemStore
	candidate string
}

func (s *heldCandidateStore) DeploymentByID(ctx context.Context, id string) (state.Deployment, error) {
	dep, err := s.MemStore.DeploymentByID(ctx, id)
	if err == nil && id == s.candidate {
		held := true
		dep.EnvironmentWorkloadRuntime, dep.EnvironmentWorkloadHeldValue = "{}", &held
	}
	return dep, err
}

// Worker scaling must not stop a qualification VM of a worker app: its
// reservation needs attempt-bound native retirement, and a stop mid-boot
// cancelled every native GitOps qualification of a worker on n2.
func TestWorkerPoolLeavesHeldGitOpsCandidateToQualification(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	_, app, _ := sched.SeedWorkerScopes(t, mem, api.PlanPro)
	candidate, err := mem.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default",
		Kind: state.DeploymentKindImage, ImageDigest: "sha256:candidate"})
	if err != nil {
		t.Fatal(err)
	}
	if err := mem.UpdateDeploymentStatus(ctx, candidate.ID, state.DeploySnapshotting, ""); err != nil {
		t.Fatal(err)
	}
	node, err := mem.ComputeNodeByName(ctx, state.DefaultLocalNodeName)
	if err != nil {
		t.Fatal(err)
	}
	qualification, err := mem.CreateInstanceWithMode(ctx, app.ID, candidate.ID, string(state.StateColdBooting),
		app.RAMMB, node.ID, uuid.NewString(), string(state.InstanceModeWorker))
	if err != nil {
		t.Fatal(err)
	}
	engine := sched.XNewEngine(t, statetest.NewMem(&heldCandidateStore{MemStore: mem, candidate: candidate.ID}),
		&sched.FakeVMM{}, &sched.FakeNotifier{}, "1.10.0")
	if err := engine.SeedLedger(ctx); err != nil {
		t.Fatal(err)
	}
	if err := engine.ReconcileWorkerPoolForScope(ctx, app.ID, "default", 1, sched.TriggerWorkerPool); err != nil {
		t.Fatal(err)
	}
	engine.WaitWorkerStops()
	got, err := mem.InstanceByID(ctx, qualification.ID)
	if err != nil || got.State != string(state.StateColdBooting) {
		t.Fatalf("qualification instance state = %q, want %q (err %v)", got.State, state.StateColdBooting, err)
	}
}
