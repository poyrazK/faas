// adr: 595 Application-wide refresh retains deployed environment capacity.
// adr: 590 Deployed environment settings retain their immutable revision.
package sched

// The native consumer is simulated. These tests exercise reviewed inheritance,
// pinned deployment settings and scheduler publication, not physical KVM effects.

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type standardEnvironmentRefreshStore interface {
	state.Store
	state.ApplicationStandardStore
	state.ApplicationStandardReviewStore
	state.ApplicationStandardOperationStore
	state.ApplicationStandardMaterializationStore
	state.ApplicationStandardAutomaticMaterializationStore
	state.ApplicationStandardEnrollmentStore
	state.ApplicationStandardRuntimeRefreshStore
	state.InstanceApplicationStandardAdmissionStore
	state.ComputeNodeRuntimeIdentityStore
	state.ProjectEnvironmentWorkloadSpecStore
	state.ProjectPromotionDeploymentStore
}

func TestApplicationStandardRefreshRetainsPinnedEnvironmentReplicas(t *testing.T) {
	exerciseStandardEnvironmentRefresh(t, state.NewMemStore())
}

func exerciseStandardEnvironmentRefresh(t *testing.T, s standardEnvironmentRefreshStore) {
	t.Helper()
	ctx := t.Context()
	owner, err := s.CreateAccountWithPersonalOrg(ctx, state.CreateAccountWithPersonalOrgParams{Email: uuid.NewString() + "@example.test", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	definition := json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["8.8.8.0/24"]}}`)
	version, err := s.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: owner.PersonalOrg.ID, ActorID: owner.Account.ID, Slug: "environment-refresh", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: definition}})
	if err != nil {
		t.Fatal(err)
	}
	request := state.ApplicationStandardReviewRequest{Scope: "organization", ScopeID: owner.PersonalOrg.ID, StandardID: version.StandardID, AdmissionVersion: 1, Active: true, BatchSize: 1}
	initial := installStandardEnvironmentRefreshRevision(t, s, owner, request)
	request.AssignmentID, request.ExpectedRevision = initial.AssignmentID, 1
	project, err := s.CreateProject(ctx, state.Project{AccountID: owner.Account.ID, Slug: "environment-refresh"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: owner.Account.ID, OrgID: owner.PersonalOrg.ID, ProjectID: project.ID,
		Slug: "environment-refresh-api", RAMMB: 128, MaxConcurrency: 5,
		Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeService, ServiceReplicas: &state.ServiceReplicas{Min: 1, Max: 5, Desired: 1}}})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimApplicationStandardEnrollment(ctx, "environment-refresh-enrollment")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MaterializeApplicationStandardEnrollment(ctx, claim); err != nil {
		t.Fatal(err)
	}
	app, err = s.AppByID(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := state.WorkloadSettingsFromApp(app)
	if err != nil {
		t.Fatal(err)
	}
	deployments := make(map[string]state.Deployment)
	for scope, replicas := range map[string]int{"production": 2, "staging": 1} {
		if scope != "production" {
			if _, err := s.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: app.AccountID, ProjectID: project.ID, Slug: scope}); err != nil {
				t.Fatal(err)
			}
		}
		settings.Manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 5, Desired: replicas}
		if _, err := s.PutProjectEnvironmentWorkloadSpec(ctx, app.AccountID, project.ID, scope, app.ID, 0, settings); err != nil {
			t.Fatal(err)
		}
		deployment, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: scope,
			Kind: state.DeploymentKindImage, ImageDigest: "sha256:abc"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.MarkDeploymentLive(ctx, deployment.ID); err != nil {
			t.Fatal(err)
		}
		deployments[scope], err = s.DeploymentByID(ctx, deployment.ID)
		if err != nil {
			t.Fatal(err)
		}
	}
	idle, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "production", Kind: state.DeploymentKindImage,
		ImageDigest: "sha256:idle", TrafficPercentExplicit: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.MarkDeploymentLiveDark(ctx, idle.ID); err != nil {
		t.Fatal(err)
	}
	// A newer desired head is not the tested settings of either resident release.
	settings.Manifest.ServiceReplicas = &state.ServiceReplicas{Min: 1, Max: 5, Desired: 4}
	if _, err := s.PutProjectEnvironmentWorkloadSpec(ctx, app.AccountID, project.ID, "production", app.ID, 1, settings); err != nil {
		t.Fatal(err)
	}
	vmm := newStandardNativeTestVMM(t, s, &fakeVMM{}, standardNativeTestNodeID(t, s))
	if err := s.HeartbeatComputeNode(ctx, vmm.identity.NodeID); err != nil {
		t.Fatal(err)
	}
	engine := newEngine(t, s, vmm, &fakeNotifier{}, "1.10.0")
	// Model the production loop's queued recovery while the refresh owns its
	// replacement wave. Run that independent reconciler explicitly afterward.
	engine.serviceReconcileSubmit = func(context.Context, string) {}
	oldIDs := make(map[string]bool)
	for _, scope := range []string{"production", "staging"} {
		count := 1
		if scope == "production" {
			count = 2
		}
		for range count {
			result, err := engine.AdmitInstanceForDeployment(ctx, app.ID, deployments[scope].ID, scope, TriggerAppWake)
			if err != nil || result.InstanceID == "" || result.AtCapacity {
				t.Fatal("initial environment admission", scope, result, err)
			}
			oldIDs[result.InstanceID] = true
		}
	}
	// Adding a governed signature default changes the reviewed revision while
	// keeping this test focused on capacity. Policy replacement across old
	// pinned workload settings has its own separate admission requirements.
	definition = json.RawMessage(`{"egress_cidrs":{"mode":"mandatory","value":["8.8.8.0/24"]},"require_signed":{"mode":"default","value":false}}`)
	if _, err := s.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: app.OrgID, ActorID: app.AccountID, Slug: version.Slug,
		CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{ExpectedVersion: 1, Definition: definition}}); err != nil {
		t.Fatal(err)
	}
	request.AdmissionVersion = 2
	installStandardEnvironmentRefreshRevision(t, s, owner, request)
	refresh, err := s.GetApplicationStandardRuntimeRefresh(ctx, app.OrgID, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := engine.RefreshApplicationStandard(ctx, refresh); err != nil {
			t.Fatal("environment replacement", err)
		}
	}
	for range 2 {
		engine.ReconcileServiceApp(ctx, app.ID)
	}
	rows, err := s.ListInstancesForApp(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	counts := make(map[string]int)
	for _, row := range rows {
		if row.DeploymentID == idle.ID {
			t.Fatal("standards refresh enrolled an idle deployment into residency")
		}
		if oldIDs[row.ID] && row.State != string(state.StateStopped) {
			t.Fatal("serving predecessor survived the refresh", row.ID, row.State)
		}
		if row.State != string(state.StateRunning) {
			continue
		}
		counts[row.DeploymentID]++
		capture, err := s.GetInstanceApplicationStandardAdmission(ctx, row.ID)
		if err != nil || capture.DesiredRevision != refresh.Standard.DesiredRevision || capture.EffectiveHash != refresh.Standard.EffectiveHash || row.Mode != string(state.InstanceModeService) {
			t.Fatal("replacement did not retain current standard and deployed service mode", row, capture, err)
		}
	}
	if counts[deployments["production"].ID] != 2 || counts[deployments["staging"].ID] != 1 || vmm.coldBoots != 6 || vmm.destroys != 3 || vmm.snapshots != 0 {
		t.Fatalf("refresh changed pinned capacity or repeated VM work: production=%d staging=%d boots=%d destroys=%d snapshots=%d",
			counts[deployments["production"].ID], counts[deployments["staging"].ID], vmm.coldBoots, vmm.destroys, vmm.snapshots)
	}
	enrollment, err := s.GetApplicationStandardEnrollment(ctx, app.OrgID, app.ID)
	if err != nil || enrollment.ObservedRevision != 0 {
		t.Fatal("scheduler acknowledgement invented consumer convergence", enrollment, err)
	}
}

func installStandardEnvironmentRefreshRevision(t *testing.T, s standardEnvironmentRefreshStore, owner state.CreateAccountWithPersonalOrgResult, request state.ApplicationStandardReviewRequest) state.ApplicationStandardOperation {
	t.Helper()
	plan, err := s.PreviewApplicationStandardAssignment(t.Context(), owner.PersonalOrg.ID, owner.Account.ID, request)
	if err != nil || len(plan.Blockers) != 0 {
		t.Fatal("review environment refresh", plan.Blockers, err)
	}
	if _, err := s.ApproveApplicationStandardReview(t.Context(), owner.PersonalOrg.ID, owner.Account.ID, plan.ID, plan.ApprovalHash); err != nil {
		t.Fatal(err)
	}
	claim, err := s.ClaimApplicationStandardOperation(t.Context(), "environment-refresh-installer")
	if err != nil {
		t.Fatal(err)
	}
	operation, err := s.MaterializeNextApplicationStandardTarget(t.Context(), claim)
	if err != nil || len(operation.Targets) > 0 && operation.Targets[0].State != "persisted" {
		t.Fatal("install environment refresh", operation, err)
	}
	return operation
}
