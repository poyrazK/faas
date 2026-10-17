// adr: 1331
package state

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func retirementMemoryFixture(t *testing.T) (*MemStore, EnvironmentGitOpsLease, EnvironmentWorkloadRetirement) {
	t.Helper()
	source := EnvironmentGitSource{ID: newID(), AccountID: newID(), ProjectID: newID(), EnvironmentID: newID(), EnvironmentSlug: "staging", Generation: 3, ApprovedRevisionID: newID(), Spec: EnvironmentGitSourceSpec{Mode: "enforce"}}
	lease := EnvironmentGitOpsLease{Source: source, Revision: EnvironmentDesiredRevision{ID: source.ApprovedRevisionID}, LeaseToken: newID(), LeaseUntil: time.Now().Add(time.Minute)}
	r := EnvironmentWorkloadRetirement{ID: newID(), SourceID: source.ID, AccountID: source.AccountID, ProjectID: source.ProjectID, EnvironmentID: source.EnvironmentID, AppID: newID(), Resource: "workload/api", Generation: 3, RevisionID: lease.Revision.ID, Phase: "retiring", RequestedAt: time.Now()}
	r.DeploymentIDs = []string{newID()}
	r.RouteDeploymentID = r.DeploymentIDs[0]
	role, url := "compute-node", "http://gateway:8080"
	m := &MemStore{environmentGitOps: map[string]*environmentGitOpsMemory{source.ID: {source: source, lease: &lease, retirements: map[string]EnvironmentWorkloadRetirement{r.Resource: r}}},
		projectEnvironments: map[string]ProjectEnvironment{source.EnvironmentID: {ID: source.EnvironmentID, AccountID: source.AccountID, ProjectID: source.ProjectID, Slug: source.EnvironmentSlug}},
		computeNodes:        map[string]ComputeNode{"node": {ID: "node", Name: "edge-a", Active: true, Role: &role, GatewayTargetURL: &url}},
		deployments:         map[string]Deployment{r.RouteDeploymentID: {ID: r.RouteDeploymentID, AppID: r.AppID, Scope: source.EnvironmentSlug, Status: DeployLive}},
		instances:           map[string]Instance{"instance": {ID: "instance", AppID: r.AppID, DeploymentID: r.RouteDeploymentID, State: "running"}}}
	return m, lease, r
}

func TestWorkloadRetirementWaitsForExactRosterThenSchedulerDrain(t *testing.T) {
	m, lease, r := retirementMemoryFixture(t)
	ctx := t.Context()
	pending, err := m.PendingEnvironmentGitOpsWorkloadRetirements(ctx, lease, []string{"edge-a"})
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending: %v %+v", err, pending)
	}
	r = pending[0]
	if ready, err := m.ReconcileEnvironmentGitOpsWorkloadRetirements(ctx, lease); err != nil || ready {
		t.Fatalf("unacknowledged retirement: %t %v", ready, err)
	}
	if got := m.deployments[r.RouteDeploymentID].Status; got != DeployLive {
		t.Fatalf("drained before withdrawal: %s", got)
	}
	for _, ack := range []struct {
		generation int64
		node       string
	}{{r.RouteGeneration - 1, "edge-a"}, {r.RouteGeneration, "foreign"}} {
		if err := m.AcknowledgeEnvironmentGitOpsWorkloadRetirement(ctx, lease, r.ID, ack.generation, ack.node); !errors.Is(err, ErrConflict) {
			t.Fatalf("accepted stale/foreign ack: %v", err)
		}
	}
	if err := m.AcknowledgeEnvironmentGitOpsWorkloadRetirement(ctx, lease, r.ID, r.RouteGeneration, "edge-a"); err != nil {
		t.Fatal(err)
	}
	if ready, err := m.ReconcileEnvironmentGitOpsWorkloadRetirements(ctx, lease); err != nil || ready {
		t.Fatalf("running instance completed retirement: %t %v", ready, err)
	}
	if m.instances["instance"].State != "running" {
		t.Fatal("apid changed scheduler instance state")
	}
	if m.deployments[r.RouteDeploymentID].Status != DeploySuperseded {
		t.Fatal("withdrawn deployment not handed to scheduler drain")
	}
	i := m.instances["instance"]
	i.State = "parked"
	m.instances[i.ID] = i
	m.invocations = map[string]Invocation{"inflight": {ID: "inflight", AppID: r.AppID, DeploymentScope: "staging", State: InvocationDispatching}}
	if ready, err := m.ReconcileEnvironmentGitOpsWorkloadRetirements(ctx, lease); err != nil || ready {
		t.Fatalf("legacy scoped in-flight work was omitted from drain: %t %v", ready, err)
	}
	inv := m.invocations["inflight"]
	inv.State = InvocationPending
	m.invocations[inv.ID] = inv
	if ready, err := m.ReconcileEnvironmentGitOpsWorkloadRetirements(ctx, lease); err != nil || !ready {
		t.Fatalf("drained retirement: %t %v", ready, err)
	}
	records, err := m.ListEnvironmentGitOpsWorkloadRetirements(ctx, lease.Source.AccountID, lease.Source.ID)
	if err != nil || records[0].Phase != "retired" || records[0].RetiredAt == nil {
		t.Fatalf("journal: %+v %v", records, err)
	}
	if !slices.Equal(records[0].DeploymentIDs, r.DeploymentIDs) {
		t.Fatal("lost original deployment identities")
	}
}
func TestWorkloadRetirementFenceSurvivesLeaseExpiryAndIsScoped(t *testing.T) {
	m, lease, r := retirementMemoryFixture(t)
	source := m.environmentGitOps[lease.Source.ID]
	source.source.Suspended = true
	source.lease.LeaseUntil = time.Now().Add(-time.Minute)
	if err := CheckEnvironmentWorkloadAdmission(t.Context(), m, r.AppID, "staging"); !errors.Is(err, ErrEnvironmentWorkloadRetired) {
		t.Fatalf("expired controller reopened admission: %v", err)
	}
	for _, scope := range []string{"production", "preview-other"} {
		if err := CheckEnvironmentWorkloadAdmission(t.Context(), m, r.AppID, scope); err != nil {
			t.Fatalf("neighbor scope blocked: %v", err)
		}
	}
	if err := CheckEnvironmentWorkloadAdmission(t.Context(), m, newID(), "staging"); err != nil {
		t.Fatal("neighbor workload blocked", err)
	}
	if _, err := m.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), lease); !errors.Is(err, ErrConflict) {
		t.Fatalf("expired controller advanced journal: %v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := m.EnvironmentWorkloadAdmissionRetired(cancelled, r.AppID, "staging"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestWorkloadRetirementGatewayReplacementInvalidatesOldAcks(t *testing.T) {
	m, lease, r := retirementMemoryFixture(t)
	ctx := t.Context()
	pending, err := m.PendingEnvironmentGitOpsWorkloadRetirements(ctx, lease, []string{"edge-a"})
	if err != nil {
		t.Fatal(err)
	}
	old := pending[0]
	if err := m.AcknowledgeEnvironmentGitOpsWorkloadRetirement(ctx, lease, r.ID, old.RouteGeneration, "edge-a"); err != nil {
		t.Fatal(err)
	}
	node := m.computeNodes["node"]
	node.Name = "edge-b"
	m.computeNodes["node"] = node
	if ready, err := m.ReconcileEnvironmentGitOpsWorkloadRetirements(ctx, lease); err != nil || ready {
		t.Fatalf("same-size roster replacement accepted: %t %v", ready, err)
	}
	pending, err = m.PendingEnvironmentGitOpsWorkloadRetirements(ctx, lease, []string{"edge-b"})
	if err != nil {
		t.Fatal(err)
	}
	if pending[0].RouteGeneration <= old.RouteGeneration || len(pending[0].AcknowledgedGateways) != 0 {
		t.Fatalf("stale generation retained: %+v", pending[0])
	}
}

func TestWorkloadRetirementProjectionKeepsProgressOutOfQualifiedPlanIdentity(t *testing.T) {
	sourceID, appID, envID := newID(), newID(), newID()
	desired, err := environmentsync.Compile(api.EnvironmentDefinition{APIVersion: environmentsync.APIVersion, Project: "shop", Environment: "staging", WorkloadPruningPolicy: "retain", Workloads: map[string]api.EnvironmentWorkload{}})
	if err != nil {
		t.Fatal(err)
	}
	snapshot := gitOpsIntentSnapshot{SourceID: sourceID, Project: "shop", Environment: "staging", EnvironmentID: envID, Plan: api.PlanPro, Prune: true,
		Resources: []gitOpsIntentResource{{Resource: "workload/api", AppID: appID}}, Apps: []gitOpsIntentApp{{ID: appID, Slug: "shop-api", Variables: map[string]string{"MODE": "retained"}, PendingInvocations: 3}},
		Owners: []environmentsync.Ownership{{Field: environmentsync.Field{Resource: "workload/api", Path: "presence", Value: json.RawMessage(`true`)}, Manager: sourceID}, {Field: environmentsync.Field{Resource: "workload/api", Path: "variables/MODE", Value: json.RawMessage(`"retained"`)}, Manager: sourceID}}}
	options := environmentsync.PlanOptions{Manager: sourceID, Revision: newID(), Generation: 4, Prune: true, Now: time.Now()}
	observePlan := func() environmentsync.Plan {
		t.Helper()
		observation, err := compileGitOpsObservation(snapshot, desired)
		if err != nil {
			t.Fatal(err)
		}
		plan, err := environmentsync.BuildPlan(desired, observation.State, observation.Owners, options)
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	before := observePlan()
	if !before.CanApply() || len(before.RetirementImpacts) != 1 || before.RetirementImpacts[0].PendingInvocations != 3 {
		t.Fatalf("review inventory: %+v", before)
	}
	snapshot.Apps[0].PendingInvocations = 9
	busy := observePlan()
	if busy.Hash != before.Hash || busy.RetirementImpacts[0].PendingInvocations != 9 {
		t.Fatal("informational work counts invalidated reviewed identity")
	}
	snapshot.Retirements = []EnvironmentWorkloadRetirement{{ID: newID(), Resource: "workload/api", AppID: appID, Phase: "planned"}}
	planned := observePlan()
	if planned.HasDrift() || !planned.CanApply() || planned.Hash == before.Hash {
		t.Fatalf("retirement reservation not represented: %+v", planned)
	}
	snapshot.Retirements[0].Phase = "retiring"
	snapshot.Retirements[0].RouteGeneration = 7
	snapshot.Retirements[0].AcknowledgedGateways = []string{"edge-a"}
	if got := observePlan(); got.Hash != planned.Hash {
		t.Fatal("acknowledgement invalidated already reviewed graph")
	}
	if len(snapshot.Owners) != 2 || len(snapshot.Apps[0].Variables) != 1 {
		t.Fatal("projection destroyed retained configuration or field ownership")
	}
}

func TestWorkloadRetirementWaitsForOriginalConsumerClaimLease(t *testing.T) {
	m, lease, r := retirementMemoryFixture(t)
	i := m.instances["instance"]
	i.State = "parked"
	m.instances[i.ID] = i
	binding, trigger := newID(), newID()
	stored := m.environmentGitOps[lease.Source.ID].retirements[r.Resource]
	stored.QueueBindingIDs = []string{binding}
	m.environmentGitOps[lease.Source.ID].retirements[r.Resource] = stored
	retiredAt := time.Now()
	m.queueBindings = map[string]QueueBinding{binding: {ID: binding, AppID: r.AppID, EnvironmentID: r.EnvironmentID, RetiredAt: &retiredAt}}
	triggerUUID := mustPgUUID(trigger)
	m.triggers = map[string]sqlc.Trigger{triggerUUID.String(): {ID: triggerUUID, QueueBindingID: mustPgUUID(binding)}}
	m.records = map[string]sqlc.TriggerRecord{"original": {TriggerID: triggerUUID, State: "claimed", ClaimExpiresAt: gitOpsTime(time.Now().Add(time.Minute))}}
	rows, err := m.PendingEnvironmentGitOpsWorkloadRetirements(t.Context(), lease, []string{"edge-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AcknowledgeEnvironmentGitOpsWorkloadRetirement(t.Context(), lease, r.ID, rows[0].RouteGeneration, "edge-a"); err != nil {
		t.Fatal(err)
	}
	if ready, err := m.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), lease); err != nil || ready {
		t.Fatalf("live consumer claim was omitted: %t %v", ready, err)
	}
	receipt := m.records["original"]
	receipt.ClaimExpiresAt = gitOpsTime(time.Now().Add(-time.Second))
	m.records["original"] = receipt
	if ready, err := m.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), lease); err != nil || !ready {
		t.Fatalf("expired original claim retained execution authority: %t %v", ready, err)
	}
	if m.records["original"].State != "claimed" {
		t.Fatal("retirement rewrote retained delivery receipts")
	}
}

func TestWorkloadRetirementRetainsOriginalLayerAfterReleaseExpiry(t *testing.T) {
	m, lease, r := retirementMemoryFixture(t)
	m.instances = nil
	m.apps = map[string]App{r.AppID: {ID: r.AppID, AccountID: r.AccountID, Status: AppActive}}
	d := m.deployments[r.RouteDeploymentID]
	d.Status = DeploySuperseded
	m.deployments[d.ID] = d
	if !m.durableWorkRetainsDeploymentLocked(d.ID) || !m.deploymentLayerArtifactReferencedLocked(d) {
		t.Fatal("retired original code lost its retention reference")
	}
	record := m.environmentGitOps[lease.Source.ID].retirements[r.Resource]
	record.Phase = "recovered"
	m.environmentGitOps[lease.Source.ID].retirements[r.Resource] = record
	if m.durableWorkRetainsDeploymentLocked(d.ID) || m.deploymentLayerArtifactReferencedLocked(d) {
		t.Fatal("recovered historical code acquired a permanent retirement pin")
	}
}

func TestWorkloadRetirementRequiresOriginalNativeOwnerRetirementProof(t *testing.T) {
	m, lease, r := retirementMemoryFixture(t)
	i := m.instances["instance"]
	i.State = "stopped"
	m.instances[i.ID] = i
	m.qualificationExecutions = map[string]EnvironmentQualificationExecutionStatus{"original": {Execution: EnvironmentQualificationExecution{DeploymentID: r.RouteDeploymentID}, DispatchStarted: true}}
	rows, err := m.PendingEnvironmentGitOpsWorkloadRetirements(t.Context(), lease, []string{"edge-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.AcknowledgeEnvironmentGitOpsWorkloadRetirement(t.Context(), lease, r.ID, rows[0].RouteGeneration, "edge-a"); err != nil {
		t.Fatal(err)
	}
	if ready, err := m.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), lease); err != nil || ready {
		t.Fatalf("terminal instance replaced native retirement proof: %t %v", ready, err)
	}
	native := m.qualificationExecutions["original"]
	now := time.Now()
	native.RetiredAt = &now
	m.qualificationExecutions["original"] = native
	if ready, err := m.ReconcileEnvironmentGitOpsWorkloadRetirements(t.Context(), lease); err != nil || !ready {
		t.Fatalf("original native retirement did not close execution: %t %v", ready, err)
	}
}

// A failed formerly activated deployment can still have retained revision
// routes or cached customer responses. It also needs an acknowledged fence.
func TestWorkloadRetirementWithdrawsPreviouslyActivatedFailedRoutes(t *testing.T) {
	m, lease, r := retirementMemoryFixture(t)
	memory := m.environmentGitOps[lease.Source.ID]
	memory.resources = map[string]string{r.Resource: r.AppID}
	deployment := m.deployments[r.RouteDeploymentID]
	deployment.Status = DeployFailed
	held := false
	deployment.EnvironmentWorkloadHeldValue = &held
	raw, err := json.Marshal(EnvironmentWorkloadRuntime{SourceID: lease.Source.ID})
	if err != nil {
		t.Fatal(err)
	}
	deployment.EnvironmentWorkloadRuntime = string(raw)
	m.deployments[deployment.ID] = deployment
	inventory := m.retirementInventoryLocked(memory, r.Resource)
	if inventory.RouteDeploymentID != deployment.ID {
		t.Fatal("failed original route escaped withdrawal acknowledgement")
	}
	held = true
	m.deployments[deployment.ID] = deployment
	if inventory := m.retirementInventoryLocked(memory, r.Resource); inventory.RouteDeploymentID != "" {
		t.Fatal("never activated candidate acquired a serving withdrawal")
	}
}
