package pgintegration_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/bindingcheck"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// Owner receipts below are control-plane fixtures, not native build/VM proof.
func prepareSourceBindingGraph(t *testing.T, store *state.PgStore, lease state.EnvironmentGitOpsLease, plan environmentsync.Plan) []state.EnvironmentWorkloadQualificationRequest {
	t.Helper()
	requests, err := store.EnvironmentGitOpsSourceRequests(t.Context(), lease, plan)
	if err != nil || len(requests) != 2 {
		t.Fatalf("source requests: %d %v", len(requests), err)
	}
	artifacts := map[string]state.EnvironmentWorkloadSourceArtifact{}
	for _, request := range requests {
		artifacts[request.Resource] = sourceArtifact(t, request)
	}
	candidates, err := store.PrepareEnvironmentGitOpsCandidates(t.Context(), lease, plan, artifacts)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("source candidates: %d %v", len(candidates), err)
	}
	for _, candidate := range candidates {
		if err := store.SetDeploymentRootfs(t.Context(), candidate.DeploymentID, "/reviewed.ext4", "reviewed/"+candidate.DeploymentID, 4096); err != nil {
			t.Fatal(err)
		}
		if err := store.UpdateDeploymentStatus(t.Context(), candidate.DeploymentID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
	}
	if graph, err := store.ReconcileEnvironmentGitOpsPreparation(t.Context(), lease, plan); err != nil || graph.Phase != "prepared" {
		t.Fatalf("source graph: %+v %v", graph, err)
	}
	cohort, err := store.QueueEnvironmentGitOpsQualification(t.Context(), lease, plan)
	if err != nil || len(cohort) != 2 {
		t.Fatalf("qualification: %d %v", len(cohort), err)
	}
	placement := qualificationPlacement(t, store, 4096)
	claimed, err := store.ClaimEnvironmentWorkloadQualificationGraphForNode(t.Context(), cohort[0].GraphID, placement.NodeID, "source-binding-fixture", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range claimed {
		placement.WakeID = uuid.NewString()
		completeClaimedQualificationReceipts(t, store, request, placement)
	}
	return claimed
}

func TestReviewedSourceBindingVerificationLifecycle(t *testing.T) {
	for _, kind := range []string{"source", "dockerfile"} {
		t.Run(kind, func(t *testing.T) {
			pool := pgtest.OpenMigrated(t)
			store := state.NewPgStore(pool)
			lease, plan, queue := workloadGraphFixtureWithWorkerSources(t, store, true, api.ServiceBindingTransportHTTPS, map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`}, kind)
			requests := prepareSourceBindingGraph(t, store, lease, plan)
			release, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
			if err != nil || !active {
				t.Fatalf("activation: %v", err)
			}
			var worker state.EnvironmentWorkloadQualificationRequest
			for _, r := range requests {
				if r.Resource == "workload/worker" {
					worker = r
				}
			}
			dep, err := store.DeploymentByID(t.Context(), worker.DeploymentID)
			if err != nil || dep.ImageDigest != "" || dep.Kind != state.DeploymentKindGitHub {
				t.Fatalf("source build replaced by image: %+v %v", dep, err)
			}
			spec, err := state.ResolveManagedServiceProbe(t.Context(), store, worker.AppID, dep.ID, "api")
			if err != nil || spec.ReleaseID != release.ID {
				t.Fatalf("source HTTPS eligibility: %+v %v", spec, err)
			}
			inventory := api.AppBindingInventory{Complete: true, VerificationDeploymentID: dep.ID, VerificationScope: dep.Scope, Bindings: []api.AppBindingInventoryItem{{Type: api.BindingTypeQueue, Binding: "orders", Scope: dep.Scope}}}
			bindings.ApplyQueueCanaryInventory(t.Context(), store, lease.Source.AccountID, worker.AppID, &inventory)
			if !inventory.Complete || !inventory.Bindings[0].QueueCanarySupported {
				t.Fatalf("source queue eligibility: %+v", inventory)
			}
			params := &state.CreateAppTaskParams{AccountID: lease.Source.AccountID, AppID: worker.AppID, DeploymentID: dep.ID, Kind: state.AppTaskKindManual, Command: []string{api.AppTaskServiceBindingTargetProbeCommand, spec.Service, spec.TargetDeploymentID}, RequireLiveDeployment: true, CreatedAt: time.Now().UTC(), TimeoutSeconds: api.BindingVerificationRunProbeTimeoutSeconds, MaxOutputBytes: api.BindingVerificationRunProbeOutputBytes, BindingVerification: &state.BindingVerificationPin{Type: api.BindingTypeService, Binding: spec.Service, TargetDeploymentID: spec.TargetDeploymentID, Revision: spec.ContextRevision, ManagedService: &spec}}
			task, err := store.CreateAppTask(t.Context(), *params)
			if err != nil || task.ImageDigest != "" || task.ArtifactKey != dep.RootfsKey {
				t.Fatalf("source task admission: %+v %v", task, err)
			}
			sourceProbeConstraintGuards(t, pool, task)
			policy, err := store.SetBindingReleasePolicy(t.Context(), lease.Source.AccountID, worker.AppID, dep.Scope, api.SetBindingReleasePolicyRequest{ExpectedRevision: new(int64), Mode: "enforce", QueueRequirements: map[string]string{"orders": api.QueueEvidenceHandlerCompletion}})
			if err != nil {
				t.Fatal(err)
			}
			if err := store.MarkAppRuntimeConfigChanged(t.Context(), worker.AppID); err != nil {
				t.Fatal(err)
			}
			stamp, _, err := store.AppRuntimeConfigChangedAt(t.Context(), worker.AppID)
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Microsecond)
			inventory.App = "shop-worker"
			inventory.RequestedDeploymentID = dep.ID
			inventory.RuntimeFreshness = &api.BindingRuntimeFreshness{ConfigChangedAt: &stamp}
			report := api.BindingVerificationRun{ID: uuid.NewString(), App: inventory.App, DeploymentID: dep.ID, Scope: dep.Scope, CreatedAt: now, ExpiresAt: now.Add(time.Minute), Status: "pending", Policy: policy, Inventory: inventory, QueuePublish: true, ReviewDigest: bindingcheck.VerificationReviewDigest(inventory, policy), Steps: []api.BindingVerificationRunStep{
				{Plan: api.BindingVerificationPlanStep{Type: api.BindingTypeService, Binding: spec.Service, Name: spec.Service, Action: "probe"}},
				{Plan: api.BindingVerificationPlanStep{Type: api.BindingTypeQueue, Binding: "orders", Name: "orders", Action: "queue-canary", RequiredEvidence: api.QueueEvidenceHandlerCompletion}},
			}}
			run, err := store.CreateBindingVerificationRun(t.Context(), lease.Source.AccountID, worker.AppID, state.VerificationRunCreate{Report: report, Admissions: []state.VerificationRunAdmission{{Task: params}, {}}})
			if err != nil {
				t.Fatalf("source durable run: %v", err)
			}
			first, err := store.AdmitBindingVerificationRunStep(t.Context(), lease.Source.AccountID, worker.AppID, run.ID, 0)
			if err != nil || first.ResourceKind != "task" {
				t.Fatalf("durable probe: %+v %v", first, err)
			}
			step, err := store.AdmitBindingVerificationRunStep(t.Context(), lease.Source.AccountID, worker.AppID, run.ID, 1)
			if err != nil || step.ResourceKind != "queue" {
				t.Fatalf("durable canary: %+v %v", step, err)
			}
			retry, err := store.AdmitBindingVerificationRunStep(t.Context(), lease.Source.AccountID, worker.AppID, run.ID, 1)
			if err != nil || retry.ResourceID != step.ResourceID {
				t.Fatalf("source replay: %+v %v", retry, err)
			}
			trigger := queue.Changes[0].TriggerID
			claimed, err := store.ClaimQueueTriggerInvocation(t.Context(), step.ResourceID, trigger, worker.AppID, "orders", 30)
			if err != nil {
				t.Fatal(err)
			}
			_, version, err := state.ResolveInvocationVersion(t.Context(), store, claimed)
			if err != nil || version.ReleaseID != release.ID || version.DeploymentID != dep.ID {
				t.Fatalf("source canary dispatch: %+v %v", version, err)
			}
			ack := state.EnvironmentWorkloadQueueServingAcknowledgement{AppID: worker.AppID, BindingID: queue.Binding.ID, TriggerID: trigger, InvocationID: step.ResourceID, DeploymentID: dep.ID, Mode: "push", Scope: dep.Scope}
			if ids, err := store.CompleteEnvironmentQueueDeliveryClaims(t.Context(), worker.AppID, trigger, "queue", []string{step.ResourceID}, []int32{1}, []int64{0}, []state.EnvironmentWorkloadQueueServingAcknowledgement{ack}); err != nil || len(ids) != 1 {
				t.Fatalf("source handler completion: %v %v", ids, err)
			}
			got, err := store.GetQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, step.ResourceID)
			if err != nil || got.Status != "succeeded" {
				t.Fatalf("source proof: %+v %v", got, err)
			}
			// JSONB formatting must not change any identity on repeated reads.
			if err := store.CheckBindingVerificationRun(t.Context(), lease.Source.AccountID, worker.AppID, run.ID); err != nil {
				t.Fatalf("stable SQL artifact identity: %v", err)
			}
			if err := store.SetDeploymentRootfs(t.Context(), dep.ID, "/rebuilt.ext4", "rebuilt-worker", 8192); !errors.Is(err, state.ErrInvalidStateTransition) {
				t.Fatalf("active reviewed artifact was mutable: %v", err)
			}
			source, err := store.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
			if err != nil {
				t.Fatal(err)
			}
			suspended := true
			if _, err := store.UpdateEnvironmentGitSource(t.Context(), source.AccountID, source.ID, state.EnvironmentGitSourceUpdate{ExpectedGeneration: source.Generation, Suspended: &suspended}); err != nil {
				t.Fatal(err)
			}
			if err := state.ManagedServiceProbeCurrent(t.Context(), store, task); !errors.Is(err, state.ErrConflict) {
				t.Fatalf("suspended source retained HTTPS evidence: %v", err)
			}
			if err := store.CheckBindingVerificationRun(t.Context(), lease.Source.AccountID, worker.AppID, run.ID); err == nil {
				t.Fatalf("suspended source retained durable authority: %v", err)
			}
			got, err = store.GetQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, step.ResourceID)
			if err != nil || got.Status != "blocked" {
				t.Fatalf("suspended source retained queue proof: %+v %v", got, err)
			}
			if got, err = store.CancelQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, step.ResourceID); err != nil || got.Status != "cancelled" {
				t.Fatalf("stale cancellation: %+v %v", got, err)
			}
		})
	}
}

func sourceProbeConstraintGuards(t *testing.T, pool *pgxpool.Pool, task state.AppTask) {
	t.Helper()
	for _, field := range []string{"missing-context", "null-context", "release", "scope", "caller", "revision"} {
		pin := *task.BindingVerification
		spec := *pin.ManagedService
		pin.ManagedService = &spec
		switch field {
		case "missing-context":
			pin.ManagedService = nil
		case "null-context":
			pin.ManagedService = nil
		case "release":
			spec.ReleaseID = ""
		case "scope":
			spec.Scope = "other"
		case "caller":
			spec.CallerDeploymentID = uuid.NewString()
		case "revision":
			spec.ContextRevision = strings.Repeat("f", 64)
		}
		raw, _ := json.Marshal(pin)
		if field == "null-context" {
			var obj map[string]any
			_ = json.Unmarshal(raw, &obj)
			obj["managed_service"] = nil
			raw, _ = json.Marshal(obj)
		}
		if _, err := pool.Exec(t.Context(), "UPDATE app_tasks SET binding_verification=$1 WHERE id=$2", raw, task.ID); err == nil {
			t.Fatalf("empty-digest task accepted %s", field)
		}
	}
}

// A rebuild replaces the reviewed graph through its normal owner APIs; live
// artifacts remain immutable and no database guard is disabled for this test.
func rebuildSourceBindingGraph(t *testing.T, store *state.PgStore, old state.EnvironmentGitOpsLease, worker state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(old.Revision.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	w := definition.Workloads["worker"]
	w.Variables["SOURCE_REVISION"] = "next-reviewed-build"
	definition.Workloads["worker"] = w
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	source, err := store.EnvironmentGitSource(t.Context(), old.Source.AccountID, old.Source.ProjectID, old.Source.EnvironmentSlug)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.ApproveEnvironmentDesiredRevision(t.Context(), approval(source, desired, strings.Repeat("c", 40))); err != nil {
		t.Fatal(err)
	}
	lease, err := store.ClaimEnvironmentGitOps(t.Context(), "source-rebuild-fixture", time.Now(), 3*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), lease, plan); err != nil {
		t.Fatal(err)
	}
	plan = claimedIntentPlan(t, store, lease, desired)
	requests := prepareSourceBindingGraph(t, store, lease, plan)
	next, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
	if err != nil || !active {
		t.Fatalf("source replacement: %v", err)
	}
	found := false
	for _, request := range requests {
		if request.Resource != "workload/worker" {
			continue
		}
		found = true
		if request.DeploymentID == worker.DeploymentID || request.Artifact.BuildID == worker.Artifact.BuildID || request.Artifact.RootfsKey == worker.Artifact.RootfsKey {
			t.Fatal("source rebuild reused old artifact identity")
		}
		current, err := state.ResolveManagedServiceProbe(t.Context(), store, request.AppID, request.DeploymentID, "api")
		if err != nil || current.ReleaseID != next.ID {
			t.Fatalf("rebuilt source context: %+v %v", current, err)
		}
	}
	if !found {
		t.Fatal("source rebuild omitted worker")
	}
}

func TestReviewedSourceRebuildInvalidatesBindingEvidence(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, plan, queue := workloadGraphFixtureWithWorkerSources(t, store, true, api.ServiceBindingTransportHTTPS, map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`}, "dockerfile")
	requests := prepareSourceBindingGraph(t, store, lease, plan)
	if _, active, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || !active {
		t.Fatalf("initial graph: %v", err)
	}
	var worker state.EnvironmentWorkloadQualificationRequest
	for _, request := range requests {
		if request.Resource == "workload/worker" {
			worker = request
		}
	}
	spec, err := state.ResolveManagedServiceProbe(t.Context(), store, worker.AppID, worker.DeploymentID, "api")
	if err != nil {
		t.Fatal(err)
	}
	canary, err := store.CreateQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, api.CreateQueueBindingCanaryRequest{DeploymentID: worker.DeploymentID, Binding: "orders", PublishConsent: true, TTLSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	trigger := queue.Changes[0].TriggerID
	if _, err := store.ClaimQueueTriggerInvocation(t.Context(), canary.ID, trigger, worker.AppID, "orders", 30); err != nil {
		t.Fatal(err)
	}
	ack := state.EnvironmentWorkloadQueueServingAcknowledgement{AppID: worker.AppID, BindingID: queue.Binding.ID, TriggerID: trigger, InvocationID: canary.ID, DeploymentID: worker.DeploymentID, Mode: "push", Scope: lease.Source.EnvironmentSlug}
	if ids, err := store.CompleteEnvironmentQueueDeliveryClaims(t.Context(), worker.AppID, trigger, "queue", []string{canary.ID}, []int32{1}, []int64{0}, []state.EnvironmentWorkloadQueueServingAcknowledgement{ack}); err != nil || len(ids) != 1 {
		t.Fatalf("completion: %v %v", ids, err)
	}
	if got, err := store.GetQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, canary.ID); err != nil || got.Status != "succeeded" {
		t.Fatalf("initial proof: %+v %v", got, err)
	}
	// No enforced app binding release policy is bypassed: that separate gate
	// requires checked candidate evidence and is outside this graph fixture.
	rebuildSourceBindingGraph(t, store, lease, worker)
	task := state.AppTask{AppID: worker.AppID, DeploymentID: worker.DeploymentID, BindingVerification: &state.BindingVerificationPin{ManagedService: &spec}}
	if err := state.ManagedServiceProbeCurrent(t.Context(), store, task); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("source rebuild retained HTTPS context: %v", err)
	}
	if got, err := store.GetQueueBindingCanary(t.Context(), lease.Source.AccountID, worker.AppID, canary.ID); err != nil || got.Status != "blocked" {
		t.Fatalf("source rebuild retained queue proof: %+v %v", got, err)
	}
}
