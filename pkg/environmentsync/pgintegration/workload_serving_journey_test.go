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

func registerServingGateway(t *testing.T, store *state.PgStore, name string) {
	t.Helper()
	role, target := "compute-only", "tcp://127.0.0.1:8080"
	if _, err := store.CreateComputeNode(t.Context(), state.ComputeNode{Name: name, Active: true,
		TargetURL: "tcp://127.0.0.1:50051", Role: &role, GatewayTargetURL: &target,
		AdmissionCeilingMB: 4096, MemMB: 8192, VPCPUs: 4, VCPUBudget: 160, MaxConcurrency: 5}); err != nil {
		t.Fatal(err)
	}
}

func completeRequestQualificationReceipts(t *testing.T, store *state.PgStore, request state.EnvironmentWorkloadQualificationRequest) {
	t.Helper()
	claimed, err := store.ClaimEnvironmentWorkloadQualification(t.Context(), request.ID, "scheduler", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	placement := qualificationPlacement(t, store, 4096)
	admitted, err := store.CreateEnvironmentWorkloadQualificationInstance(t.Context(), claimed, placement)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkEnvironmentQualificationDispatched(t.Context(), claimed, admitted.Execution); err != nil {
		t.Fatal(err)
	}
	inputs := state.RuntimeConfigInputs{Scope: "production", Boundary: time.Now().UTC(), Variables: map[string]string{},
		SecretVersions: map[string]int64{}, SecretRefs: map[string]string{}, AllSecrets: true}
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
