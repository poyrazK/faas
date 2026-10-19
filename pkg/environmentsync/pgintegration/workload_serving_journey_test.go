package pgintegration_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/state"
)

// This covers the durable control-plane handoff across the whole request graph.
// Native capture, restore, and guest probes have their own metal acceptance;
// this fixture submits their receipts through the same state APIs as schedd.
func TestPgEnvironmentGitOpsGraphActivationServingAndRosterRecovery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, requests := preparedRequestModeQualificationFixture(t, store)
	var definition api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &definition); err != nil {
		t.Fatal(err)
	}
	desired, err := environmentsync.Compile(definition)
	if err != nil {
		t.Fatal(err)
	}
	plan := claimedIntentPlan(t, store, lease, desired)
	if managed, err := store.EnvironmentGitOpsHasServingManagedWorkload(t.Context(), lease); err != nil || managed {
		t.Fatalf("held, unactivated candidates were treated as serving: managed=%t err=%v", managed, err)
	}
	if _, activated, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || activated {
		t.Fatalf("unqualified graph activated: %t %v", activated, err)
	}
	for _, request := range requests {
		completeRequestQualificationReceipts(t, store, request)
	}
	qualified, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
	if err != nil || !qualified.Qualified || qualified.Activated || qualified.Serving {
		t.Fatalf("qualification crossed the activation boundary: %+v %v", qualified, err)
	}
	release, activated, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan)
	if err != nil || !activated || len(release.Members) != len(requests) {
		t.Fatalf("whole-graph activation: %+v %t %v", release, activated, err)
	}
	if managed, err := store.EnvironmentGitOpsHasServingManagedWorkload(t.Context(), lease); err != nil || !managed {
		t.Fatalf("active Git-managed release was omitted by empty-graph guard: managed=%t err=%v", managed, err)
	}
	activatedEvidence, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
	if err != nil || !activatedEvidence.Activated || activatedEvidence.Serving {
		t.Fatalf("activation claimed serving: %+v %v", activatedEvidence, err)
	}
	var sqlSupported bool
	if err := pool.QueryRow(t.Context(), `SELECT environment_workload_graph_serving_supported($1::uuid)`, requests[0].GraphID).Scan(&sqlSupported); err != nil || !sqlSupported {
		t.Fatalf("SQL serving adapter rejected graph %s: supported=%t err=%v", requests[0].GraphID, sqlSupported, err)
	}

	gateways := []string{"edge-a", "edge-b"}
	for _, gateway := range gateways {
		registerServingGateway(t, store, gateway)
	}
	receipt, serving, err := store.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, gateways)
	if err != nil || serving || len(receipt.Routes) != len(requests) || receipt.ReleaseSetID != release.ID {
		t.Fatalf("route cutover preparation: %+v %t %v", receipt, serving, err)
	}
	currentSource, err := store.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
	if err != nil || currentSource.IntentVersion == lease.Source.IntentVersion || receipt.IntentVersion != currentSource.IntentVersion {
		t.Fatalf("receipt did not bind the advanced graph intent: lease=%d source=%d receipt=%d err=%v",
			lease.Source.IntentVersion, currentSource.IntentVersion, receipt.IntentVersion, err)
	}
	for _, route := range receipt.Routes {
		deployment, err := store.DeploymentByID(t.Context(), route.DeploymentID)
		if err != nil || !route.Cutover || deployment.TrafficPercent != 100 || deployment.EnvironmentWorkloadHeld() {
			t.Fatalf("candidate route not live at cutover: %+v %+v %v", route, deployment, err)
		}
	}
	firstGeneration := receipt.Routes[0].Generation
	if _, complete, err := store.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, receipt.GraphID, firstGeneration, gateways[0]); err != nil || complete {
		t.Fatalf("one route acknowledgement completed graph: %t %v", complete, err)
	}

	// A new store instance represents controller restart. The pending receipt
	// and its acknowledgement must survive, then a changed gateway roster must
	// require fresh generations from every current gateway.
	restarted := state.NewPgStore(pool)
	recovered, serving, err := restarted.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, gateways)
	if err != nil || serving || recovered.Routes[0].Generation != firstGeneration ||
		!slices.Equal(recovered.Acknowledgements[firstGeneration], []string{gateways[0]}) {
		t.Fatalf("restart lost pending route evidence: %+v %t %v", recovered, serving, err)
	}
	gateways = append(gateways, "edge-c")
	registerServingGateway(t, restarted, gateways[2])
	reset, serving, err := restarted.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, gateways)
	if err != nil || serving || len(reset.Routes) != len(requests) || len(reset.ExpectedGateways) != len(gateways) {
		t.Fatalf("gateway-roster reset: %+v %t %v", reset, serving, err)
	}
	if _, _, err := restarted.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, reset.GraphID, firstGeneration, gateways[0]); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("old route generation acknowledged after roster change: %v", err)
	}
	for _, route := range reset.Routes {
		if route.Generation == firstGeneration {
			t.Fatal("roster change reused an acknowledged generation")
		}
		for _, gateway := range gateways {
			updated, complete, err := restarted.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, reset.GraphID, route.Generation, gateway)
			if err != nil {
				t.Fatalf("%s route acknowledgement: %v", gateway, err)
			}
			if complete && (route.Generation != reset.Routes[len(reset.Routes)-1].Generation || gateway != gateways[len(gateways)-1]) {
				t.Fatalf("graph served before all routes and gateways acknowledged: %+v", updated)
			}
		}
	}
	served, err := restarted.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
	if err != nil || !served.Qualified || !served.Activated || !served.Serving || slices.Contains(served.BlockingReasons, "environment_serving_evidence_missing") {
		t.Fatalf("complete gateway cohort did not serve: %+v %v", served, err)
	}
}

func TestPgEnvironmentGitOpsMixedGraphServingWaitsForQueueDelivery(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, plan, requests := preparedQualificationFixture(t, store)
	var worker state.EnvironmentWorkloadQualificationRequest
	for _, request := range requests {
		if request.Resource == "workload/worker" {
			worker = request
		}
	}
	if worker.ID == "" {
		t.Fatal("prepared graph has no worker")
	}
	var bindingID, triggerID string
	if err := pool.QueryRow(t.Context(), `SELECT binding.id::text,queue_trigger.id::text
		FROM queue_bindings binding JOIN triggers queue_trigger ON queue_trigger.queue_binding_id=binding.id
		WHERE binding.app_id=$1::uuid AND binding.name='orders' AND queue_trigger.enabled`, worker.AppID).
		Scan(&bindingID, &triggerID); err != nil {
		t.Fatal(err)
	}
	staleInvocationID := completePushServingTestDelivery(t, store, lease.Source.AccountID, worker.AppID,
		lease.Source.EnvironmentSlug, bindingID, triggerID, worker.DeploymentID, true)
	for _, request := range requests {
		completeRequestQualificationReceipts(t, store, request)
	}
	if _, activated, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || !activated {
		t.Fatalf("mixed graph activation: %t %v", activated, err)
	}
	registerServingGateway(t, store, "edge-a")
	receipt, serving, err := store.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"edge-a"})
	if err != nil || serving || len(receipt.Routes) != 1 || len(receipt.ExpectedQueueConsumers) != 1 {
		t.Fatalf("mixed graph serving preparation: %+v %t %v", receipt, serving, err)
	}
	if _, serving, err := store.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, receipt.GraphID,
		receipt.Routes[0].Generation, "edge-a"); err != nil || serving {
		t.Fatalf("route acknowledgement bypassed queue evidence: %t %v", serving, err)
	}
	restarted := state.NewPgStore(pool)
	pending, serving, err := restarted.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"edge-a"})
	if err != nil || serving || len(pending.ExpectedQueueConsumers) != 1 ||
		!slices.Equal(pending.Acknowledgements[receipt.Routes[0].Generation], []string{"edge-a"}) {
		t.Fatalf("restart lost pending route or queue expectation: %+v %t %v", pending, serving, err)
	}
	consumer := receipt.ExpectedQueueConsumers[0]
	if consumer.BindingID != bindingID || consumer.TriggerID != triggerID {
		t.Fatalf("active consumer differs from reviewed pre-activation binding: %+v", consumer)
	}
	err = restarted.RecordEnvironmentGitOpsQueueServingAcknowledgement(t.Context(), state.EnvironmentWorkloadQueueServingAcknowledgement{
		Mode: "push", AppID: worker.AppID, Scope: lease.Source.EnvironmentSlug, BindingID: bindingID,
		TriggerID: triggerID, DeploymentID: worker.DeploymentID, InvocationID: staleInvocationID,
	})
	if err == nil {
		t.Fatal("pre-activation queue delivery acknowledged the newly active graph")
	}
	evidence, err := restarted.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
	if err != nil || evidence.Serving {
		t.Fatalf("pre-activation queue delivery established serving: %+v %v", evidence, err)
	}
	freshInvocationID := completePushServingTestDelivery(t, restarted, lease.Source.AccountID, worker.AppID,
		lease.Source.EnvironmentSlug, bindingID, triggerID, worker.DeploymentID, true)
	if err := restarted.RecordEnvironmentGitOpsQueueServingAcknowledgement(t.Context(), state.EnvironmentWorkloadQueueServingAcknowledgement{
		Mode: "push", AppID: worker.AppID, Scope: lease.Source.EnvironmentSlug, BindingID: bindingID,
		TriggerID: triggerID, DeploymentID: worker.DeploymentID, InvocationID: freshInvocationID,
	}); err != nil {
		t.Fatalf("replaying current queue serving proof: %v", err)
	}
	served, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan)
	if err != nil || !served.Serving || !served.Activated || !served.Qualified {
		t.Fatalf("complete mixed graph not serving: %+v %v", served, err)
	}
}

func TestPgEnvironmentGitOpsServingSurvivesSourceOutageAndReviewedRollback(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	store := state.NewPgStore(pool)
	lease, plan, requests := preparedQualificationFixture(t, store)
	var worker state.EnvironmentWorkloadQualificationRequest
	for _, request := range requests {
		completeRequestQualificationReceipts(t, store, request)
		if request.Resource == "workload/worker" {
			worker = request
		}
	}
	if worker.ID == "" {
		t.Fatal("mixed graph has no worker")
	}
	var bindingID, triggerID string
	if err := pool.QueryRow(t.Context(), `SELECT binding.id::text,queue_trigger.id::text
		FROM queue_bindings binding JOIN triggers queue_trigger ON queue_trigger.queue_binding_id=binding.id
		WHERE binding.app_id=$1::uuid AND binding.name='orders' AND queue_trigger.enabled`, worker.AppID).
		Scan(&bindingID, &triggerID); err != nil {
		t.Fatal(err)
	}
	if _, activated, err := store.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), lease, plan); err != nil || !activated {
		t.Fatalf("mixed graph activation: %t %v", activated, err)
	}
	registerServingGateway(t, store, "edge-a")
	receipt, serving, err := store.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), lease, plan, []string{"edge-a"})
	if err != nil || serving || len(receipt.Routes) != 1 {
		t.Fatalf("mixed graph route preparation: %+v %t %v", receipt, serving, err)
	}
	if _, serving, err := store.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), lease, receipt.GraphID,
		receipt.Routes[0].Generation, "edge-a"); err != nil || serving {
		t.Fatalf("route alone served mixed graph: %t %v", serving, err)
	}
	completePushServingTestDelivery(t, store, lease.Source.AccountID, worker.AppID,
		lease.Source.EnvironmentSlug, bindingID, triggerID, worker.DeploymentID, true)
	if evidence, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan); err != nil || !evidence.Serving {
		t.Fatalf("mixed graph never served: %+v %v", evidence, err)
	}

	polls := state.EnvironmentGitSourcePollStore(store)
	now := time.Now().UTC()
	poll, err := polls.ClaimEnvironmentGitSourcePoll(t.Context(), uuid.NewString(), now, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := polls.FinishEnvironmentGitSourcePoll(t.Context(), poll, state.EnvironmentGitSourcePollResult{
		ErrorCode: "environment_git_source_unavailable"}, now, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	stale, err := store.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
	if err != nil || stale.ApprovedRevisionID != lease.Revision.ID || stale.Generation != lease.Source.Generation ||
		stale.SourceErrorCode != "environment_git_source_unavailable" {
		t.Fatalf("outage changed approved authority: %+v %v", stale, err)
	}
	if evidence, err := state.NewPgStore(pool).EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan); err != nil || !evidence.Serving {
		t.Fatalf("source outage stopped the serving graph: %+v %v", evidence, err)
	}

	accepted, err := store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: lease.Source.AccountID,
		AppID: worker.AppID, Source: state.InvocationQueue, QueueName: "orders", QueueBindingID: bindingID,
		DeploymentScope: lease.Source.EnvironmentSlug, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	var changedDefinition, servingDefinition api.EnvironmentDefinition
	if err := json.Unmarshal(lease.Revision.Definition, &changedDefinition); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(lease.Revision.Definition, &servingDefinition); err != nil {
		t.Fatal(err)
	}
	servingDesired, err := environmentsync.Compile(servingDefinition)
	if err != nil {
		t.Fatal(err)
	}
	changedWorker := changedDefinition.Workloads["worker"]
	changedQueue := changedWorker.QueueBindings["orders"]
	changedQueue.MaxConcurrency = 2
	changedWorker.QueueBindings["orders"] = changedQueue
	changedDefinition.Workloads["worker"] = changedWorker
	changedDesired, err := environmentsync.Compile(changedDefinition)
	if err != nil {
		t.Fatal(err)
	}
	changedSource, changedRevision, err := store.ApproveEnvironmentDesiredRevision(t.Context(),
		approval(stale, changedDesired, strings.Repeat("c", 40)))
	if err != nil || changedSource.Generation <= stale.Generation || changedRevision.ID == lease.Revision.ID {
		t.Fatalf("reviewed change did not supersede the serving revision: %+v %+v %v", changedSource, changedRevision, err)
	}
	if _, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), lease, plan); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("superseded serving lease remained authoritative: %v", err)
	}
	changedLease, err := state.NewPgStore(pool).ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), 3*time.Minute)
	if err != nil || changedLease.Revision.ID != changedRevision.ID {
		t.Fatalf("reviewed change was not executable: %+v %v", changedLease, err)
	}
	changedPlan := claimedIntentPlan(t, store, changedLease, changedDesired)
	if !changedPlan.CanApply() {
		t.Fatalf("reviewed change is blocked: %+v", changedPlan)
	}
	if _, err := store.ApplyEnvironmentGitOps(t.Context(), changedLease, changedPlan); err != nil {
		t.Fatal(err)
	}
	changedBinding, err := store.QueueBindingByID(t.Context(), lease.Source.AccountID, worker.AppID, bindingID)
	if err != nil || changedBinding.MaxConcurrency != 2 {
		t.Fatalf("reviewed queue change did not apply: %+v %v", changedBinding, err)
	}
	current, err := store.EnvironmentGitSource(t.Context(), lease.Source.AccountID, lease.Source.ProjectID, lease.Source.EnvironmentSlug)
	if err != nil {
		t.Fatal(err)
	}
	rolledBack, previousRevision, err := store.ApproveEnvironmentDesiredRevision(t.Context(),
		approval(current, servingDesired, lease.Revision.CommitSHA))
	if err != nil || rolledBack.Generation <= changedSource.Generation || previousRevision.ID != lease.Revision.ID {
		t.Fatalf("reviewed rollback did not restore the prior revision: %+v %+v %v", rolledBack, previousRevision, err)
	}
	if _, err := store.EnvironmentGitOpsActivationEvidence(t.Context(), changedLease, changedPlan); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("abandoned change lease remained authoritative after rollback: %v", err)
	}

	restarted := state.NewPgStore(pool)
	rollbackLease, err := restarted.ClaimEnvironmentGitOps(t.Context(), uuid.NewString(), time.Now(), 3*time.Minute)
	if err != nil || rollbackLease.Revision.ID != previousRevision.ID {
		t.Fatalf("reviewed rollback was not executable: %+v %v", rollbackLease, err)
	}
	rollbackPlan := claimedIntentPlan(t, restarted, rollbackLease, servingDesired)
	if !rollbackPlan.CanApply() {
		t.Fatalf("reviewed rollback is blocked: %+v", rollbackPlan)
	}
	if _, err := restarted.ApplyEnvironmentGitOps(t.Context(), rollbackLease, rollbackPlan); err != nil {
		t.Fatal(err)
	}
	restoredBinding, err := restarted.QueueBindingByID(t.Context(), lease.Source.AccountID, worker.AppID, bindingID)
	if err != nil || restoredBinding.MaxConcurrency != 1 {
		t.Fatalf("rollback did not restore the reviewed queue contract: %+v %v", restoredBinding, err)
	}
	if pending, err := restarted.InvocationByID(t.Context(), accepted.ID); err != nil || pending.State != state.InvocationPending ||
		pending.QueueBindingID != bindingID {
		t.Fatalf("rollback reconciliation discarded accepted queue work: %+v %v", pending, err)
	}
	rollbackPlan = claimedIntentPlan(t, restarted, rollbackLease, servingDesired)
	candidates, err := restarted.PrepareEnvironmentGitOpsImageCandidates(t.Context(), rollbackLease, rollbackPlan)
	if err != nil || len(candidates) != 2 {
		t.Fatalf("rollback candidate cohort: %+v %v", candidates, err)
	}
	for _, candidate := range candidates {
		if err := restarted.SetDeploymentRootfs(t.Context(), candidate.DeploymentID, "/reviewed.ext4", "rollback-"+candidate.Resource, 4096); err != nil {
			t.Fatal(err)
		}
		if err := restarted.UpdateDeploymentStatus(t.Context(), candidate.DeploymentID, state.DeploySnapshotting, ""); err != nil {
			t.Fatal(err)
		}
	}
	if graph, err := restarted.ReconcileEnvironmentGitOpsPreparation(t.Context(), rollbackLease, rollbackPlan); err != nil || graph.Phase != "prepared" {
		t.Fatalf("rollback graph preparation: %+v %v", graph, err)
	}
	rollbackRequests, err := restarted.QueueEnvironmentGitOpsQualification(t.Context(), rollbackLease, rollbackPlan)
	if err != nil || len(rollbackRequests) != 2 {
		t.Fatalf("rollback qualification cohort: %+v %v", rollbackRequests, err)
	}
	var rollbackWorker state.EnvironmentWorkloadQualificationRequest
	for _, request := range rollbackRequests {
		completeRequestQualificationReceipts(t, restarted, request)
		if request.Resource == "workload/worker" {
			rollbackWorker = request
		}
	}
	if rollbackWorker.ID == "" {
		t.Fatal("rollback graph has no worker")
	}
	if _, activated, err := restarted.ActivateEnvironmentGitOpsWorkloadGraph(t.Context(), rollbackLease, rollbackPlan); err != nil || !activated {
		t.Fatalf("rollback graph activation: %t %v", activated, err)
	}
	rollbackReceipt, serving, err := restarted.PrepareEnvironmentGitOpsWorkloadServing(t.Context(), rollbackLease, rollbackPlan, []string{"edge-a"})
	if err != nil || serving || len(rollbackReceipt.Routes) != 1 {
		t.Fatalf("rollback route preparation: %+v %t %v", rollbackReceipt, serving, err)
	}
	if _, serving, err := restarted.AcknowledgeEnvironmentGitOpsWorkloadServing(t.Context(), rollbackLease,
		rollbackReceipt.GraphID, rollbackReceipt.Routes[0].Generation, "edge-a"); err != nil || serving {
		t.Fatalf("rollback route alone served mixed graph: %t %v", serving, err)
	}
	completePushServingTestDelivery(t, restarted, rollbackLease.Source.AccountID, rollbackWorker.AppID,
		rollbackLease.Source.EnvironmentSlug, bindingID, triggerID, rollbackWorker.DeploymentID, true)
	if evidence, err := restarted.EnvironmentGitOpsActivationEvidence(t.Context(), rollbackLease, rollbackPlan); err != nil || !evidence.Serving {
		t.Fatalf("rollback graph did not converge on new serving evidence: %+v %v", evidence, err)
	}
	if pending, err := restarted.InvocationByID(t.Context(), accepted.ID); err != nil || pending.State != state.InvocationPending ||
		pending.QueueBindingID != bindingID {
		t.Fatalf("rollback serving discarded accepted queue work: %+v %v", pending, err)
	}
}

func completePushServingTestDelivery(t *testing.T, store *state.PgStore, accountID, appID, scope,
	bindingID, triggerID, deploymentID string, acknowledge bool) string {
	t.Helper()
	invocation, err := store.EnqueueInvocation(t.Context(), state.Invocation{AccountID: accountID,
		AppID: appID, Source: state.InvocationQueue, QueueName: "orders", QueueBindingID: bindingID,
		DeploymentScope: scope, DueAt: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	claimedInvocation, err := store.ClaimQueueTriggerInvocation(t.Context(), invocation.ID, triggerID, appID, "orders", 600)
	if err != nil {
		t.Fatal(err)
	}
	triggerRecord, err := store.InsertTriggerRecord(t.Context(), triggerID, invocation.ID, []byte(`{}`), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := store.ClaimTriggerRecords(t.Context(), triggerID, 1)
	if err != nil || len(claimed) != 1 || claimed[0].ID.String() != triggerRecord {
		t.Fatalf("trigger claim: %+v %v", claimed, err)
	}
	var acknowledgements []state.EnvironmentWorkloadQueueServingAcknowledgement
	if acknowledge {
		acknowledgements = []state.EnvironmentWorkloadQueueServingAcknowledgement{{
			Mode: "push", AppID: appID, Scope: scope, BindingID: bindingID,
			TriggerID: triggerID, DeploymentID: deploymentID, InvocationID: invocation.ID,
		}}
	}
	finalized, err := store.CompleteEnvironmentQueueDeliveryClaims(t.Context(), appID, triggerID, "queue",
		[]string{invocation.ID}, []int32{int32(claimedInvocation.Attempts)}, []int64{claimedInvocation.ReplayGeneration},
		acknowledgements)
	if err != nil || !slices.Equal(finalized, []string{invocation.ID}) {
		t.Fatalf("successful reviewed queue delivery did not finalize: %+v %v", finalized, err)
	}
	return invocation.ID
}

func registerServingGateway(t *testing.T, store state.Store, name string) {
	t.Helper()
	role, target := "compute-only", "tcp://127.0.0.1:8080"
	if _, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: name, Active: true,
		TargetURL: "tcp://127.0.0.1:50051", Role: &role, GatewayTargetURL: &target,
		AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5}); err != nil {
		t.Fatal(err)
	}
}

type requestQualificationReceiptStore interface {
	state.Store
	state.EnvironmentGitOpsQualificationStore
	state.EnvironmentGitOpsQualificationInstanceStore
	state.EnvironmentQualificationExecutionStore
	state.EnvironmentGitOpsQualificationRuntimeStore
	state.EnvironmentQualificationConfigReceiptStore
	state.EnvironmentQualificationSnapshotStore
	state.EnvironmentQualificationRestoreStore
	state.EnvironmentQualificationRestoreRuntimeStore
	state.EnvironmentQualificationFrameworkReadyReceiptStore
	state.EnvironmentQualificationRestoreReceiptStore
	state.EnvironmentQualificationSmokeReceiptStore
}

func completeRequestQualificationReceipts(t *testing.T, store requestQualificationReceiptStore, request state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	claimed, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), request.ID, "scheduler", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	placement := qualificationPlacement(t, store, 4096)
	completeClaimedQualificationReceipts(t, store, claimed, placement)
}

func completeClaimedQualificationReceipts(t *testing.T, store *state.PgStore, claimed state.EnvironmentWorkloadQualificationRequest, placement state.EnvironmentWorkloadQualificationPlacement) {
	t.Helper()
	admitted, err := store.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkEnvironmentQualificationDispatched(t.Context(), claimed, admitted.Execution); err != nil {
		t.Fatal(err)
	}
	inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC(), Variables: map[string]string{},
		SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}
	for key, value := range claimed.FrozenInputs.Variables {
		inputs.Variables[key] = value
	}
	runtime := state.EnvironmentWorkloadQualificationRuntime{NodeID: placement.NodeID, WakeID: placement.WakeID,
		Netns: "qualification-source", HostIP: "10.0.0.1", GuestUID: 20001, Inputs: inputs}
	if _, err := store.PublishEnvironmentWorkloadQualificationRuntime(t.Context(), claimed, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordEnvironmentQualificationConfigReceipt(t.Context(), claimed, admitted.Execution, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	proof := captureProof(admitted.Execution)
	if _, err := store.RecordEnvironmentQualificationSnapshot(t.Context(), claimed, admitted.Execution, proof); err != nil {
		t.Fatal(err)
	}
	if err := store.RetireEnvironmentQualificationExecution(t.Context(), admitted.Execution, state.EnvironmentQualificationRetirement{
		Kind: state.QualificationNativeRetired, ReceiptID: proof.CaptureID, NativeGeneration: proof.NativeGeneration,
		KernelBootID: proof.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}); err != nil {
		t.Fatal(err)
	}
	restorePlacement := placement
	restorePlacement.WakeID = uuid.NewString()
	restored, err := store.CreateEnvironmentQualificationRestore(t.Context(), claimed, restorePlacement)
	if err != nil || !restored.Created {
		t.Fatalf("restore admission: %+v %v", restored, err)
	}
	if err := store.MarkEnvironmentQualificationRestoreDispatched(t.Context(), claimed, restored.Execution); err != nil {
		t.Fatal(err)
	}
	runtime.NodeID, runtime.WakeID = restorePlacement.NodeID, restorePlacement.WakeID
	runtime.Inputs.Boundary = time.Now().UTC()
	if _, err := store.PublishEnvironmentQualificationRestoreRuntime(t.Context(), claimed, restored.Execution, runtime); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordEnvironmentQualificationConfigReceipt(t.Context(), claimed, restored.Execution, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	runtimeLabel := claimed.FrozenInputs.RuntimeBase
	if runtimeLabel == "" {
		runtimeLabel = "node22"
	}
	if _, err := store.RecordEnvironmentQualificationFrameworkReadyReceipt(t.Context(), restored.Execution, runtimeLabel, 42); err != nil {
		t.Fatal(err)
	}
	if err := store.RetireEnvironmentQualificationExecution(t.Context(), restored.Execution, state.EnvironmentQualificationRetirement{
		Kind: state.QualificationNativeRetired, ReceiptID: uuid.NewString(), NativeGeneration: uuid.NewString(),
		KernelBootID: proof.KernelBootID, ProcessesExited: true, ResourcesRemoved: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordEnvironmentQualificationRestoreReceipt(t.Context(), claimed, restored.Execution, runtime.Inputs); err != nil {
		t.Fatal(err)
	}
	policy, digest, err := state.EnvironmentQualificationSmokePolicyFor(claimed)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.RecordEnvironmentQualificationSmokeReceipt(t.Context(), claimed, state.EnvironmentQualificationSmokeEvidence{
		Resource: claimed.Resource, InstanceID: restored.Execution.InstanceID, PolicyID: policy.ID,
		PolicySHA256: digest, ResultSHA256: strings.Repeat("c", 64), Passed: true}); err != nil {
		t.Fatal(err)
	}
}
