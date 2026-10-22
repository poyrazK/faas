package pgintegration_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// This is durable admission coverage, not native qualification evidence. The
// existing control-plane fixture supplies receipts through the owner APIs.
func TestPgManagedWorkerProbePinsActiveReleaseAndRejectsStaleAdmission(t *testing.T) {
	store := state.NewPgStore(pgtest.OpenMigrated(t))
	lease, plan, _ := workloadGraphFixtureWithWorkerTransport(t, store, true, api.ServiceBindingTransportHTTPS)
	lease, plan, requests := prepareQualificationFixture(t, store, lease, plan)
	for _, request := range requests {
		if request.Resource == "workload/worker" {
			if _, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), request.ID, "individual-worker", time.Minute); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("service-bound worker bypassed atomic graph admission: %v", err)
			}
		}
	}
	placement := qualificationPlacement(t, store, 4096)
	requests, err := store.ClaimEnvironmentWorkloadQualificationGraphForNode(t.Context(), requests[0].GraphID, placement.NodeID, "worker-probe-fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	var worker state.EnvironmentWorkloadQualificationRequest
	for _, request := range requests {
		placement.WakeID = uuid.NewString()
		completeClaimedQualificationReceipts(t, store, request, placement)
		if request.Resource == "workload/worker" {
			worker = request
		}
	}
	if worker.ID == "" {
		t.Fatal("worker fixture missing")
	}
	release, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
	if err != nil || !active {
		t.Fatalf("activate fixture: %v", err)
	}
	spec, err := state.ResolveManagedServiceProbe(t.Context(), store, worker.AppID, worker.DeploymentID, "api")
	if err != nil || !spec.Valid() || spec.ReleaseID != release.ID || spec.CallerDeploymentID != worker.DeploymentID || spec.Binding != "API_URL" {
		t.Fatalf("worker probe context: %+v %v", spec, err)
	}
	params := state.CreateAppTaskParams{AccountID: lease.Source.AccountID, AppID: worker.AppID, DeploymentID: worker.DeploymentID,
		Kind: state.AppTaskKindManual, Command: []string{api.AppTaskServiceBindingTargetProbeCommand, spec.Service, spec.TargetDeploymentID},
		RequireLiveDeployment: true, CreatedAt: time.Now().UTC(), TimeoutSeconds: 15, MaxOutputBytes: 4096,
		BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypeService, Binding: spec.Service,
			TargetDeploymentID: spec.TargetDeploymentID, Revision: spec.ContextRevision, ManagedService: &spec}}
	task, err := store.CreateAppTask(t.Context(), params)
	if err != nil || task.BindingVerification == nil || task.BindingVerification.ManagedService == nil || *task.BindingVerification.ManagedService != spec {
		t.Fatalf("durable worker admission lost context: %+v %v", task, err)
	}
	rows, err := store.ListBindingVerificationTasks(t.Context(), lease.Source.AccountID, worker.AppID, []string{api.BindingTypeService},
		state.BindingVerificationSelection{DeploymentID: worker.DeploymentID, ServiceTargets: map[string]string{spec.Service: spec.TargetDeploymentID}, ManagedService: true})
	if err != nil || len(rows) != 1 || rows[0].TaskID != task.ID || rows[0].Pin.ManagedService == nil || *rows[0].Pin.ManagedService != spec {
		t.Fatalf("durable worker receipt selection: %+v %v", rows, err)
	}
	// Source suspension must invalidate both dispatch and a second transaction's
	// admission, even when the release and deployment IDs themselves still match.
	source, err := store.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
	if err != nil {
		t.Fatal(err)
	}
	suspended := true
	if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Suspended: &suspended}); err != nil {
		t.Fatal(err)
	}
	if err := state.ManagedServiceProbeCurrent(t.Context(), store, task); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("suspended source still dispatchable: %v", err)
	}
	params.CreatedAt = params.CreatedAt.Add(time.Millisecond)
	if _, err := store.CreateAppTask(t.Context(), params); !errors.Is(err, state.ErrAppTaskDeploymentUnavailable) {
		t.Fatalf("stale worker context admitted: %v", err)
	}
}
