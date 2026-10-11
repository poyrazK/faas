package sched

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestQualificationSmokeProbeFrameMatchesExactFrozenTarget(t *testing.T) {
	_, _, requests := queuedQualificationExecutionFixture(t, api.ExecutionModeRequest)
	request := requests[0]
	instance := state.Instance{ID: "restored-instance", WakeID: "wake", NodeID: "node", AppID: request.AppID,
		DeploymentID: request.DeploymentID, State: string(state.StateRunning), RAMMB: 512}
	frame := state.EnvironmentQualificationExecution{InstanceID: instance.ID, CaptureInstanceID: request.ReservedInstanceID,
		RequestID: request.ID, GraphID: request.GraphID, AppID: request.AppID, DeploymentID: request.DeploymentID,
		NodeID: instance.NodeID, WakeID: instance.WakeID, SourceID: request.FrozenInputs.SourceID,
		EnvironmentID: request.FrozenInputs.EnvironmentID, RevisionID: request.FrozenInputs.RevisionID,
		Resource: request.Resource, Scope: request.FrozenInputs.Scope, PlanHash: request.FrozenInputs.PlanHash,
		Generation: request.FrozenInputs.Generation, IntentVersion: request.FrozenInputs.IntentVersion,
		Attempt: request.Attempt, RAMMB: instance.RAMMB, Artifact: request.Artifact}
	if !qualificationSmokeProbeFrameMatches(frame, request, instance) {
		t.Fatal("exact frozen target frame was rejected")
	}
	source := instance
	source.ID = request.ReservedInstanceID
	sourceFrame := frame
	sourceFrame.InstanceID = source.ID
	sourceFrame.CaptureInstanceID = ""
	if !qualificationSmokeProbeFrameMatches(sourceFrame, request, source) {
		t.Fatal("exact live source frame was rejected")
	}
	mutations := map[string]func(*state.EnvironmentQualificationExecution){
		"source":      func(frame *state.EnvironmentQualificationExecution) { frame.SourceID += "-other" },
		"environment": func(frame *state.EnvironmentQualificationExecution) { frame.EnvironmentID += "-other" },
		"revision":    func(frame *state.EnvironmentQualificationExecution) { frame.RevisionID += "-other" },
		"scope":       func(frame *state.EnvironmentQualificationExecution) { frame.Scope += "-other" },
		"plan":        func(frame *state.EnvironmentQualificationExecution) { frame.PlanHash += "-other" },
		"generation":  func(frame *state.EnvironmentQualificationExecution) { frame.Generation++ },
		"intent":      func(frame *state.EnvironmentQualificationExecution) { frame.IntentVersion++ },
		"artifact":    func(frame *state.EnvironmentQualificationExecution) { frame.Artifact.RootfsKey += "-other" },
		"ram":         func(frame *state.EnvironmentQualificationExecution) { frame.RAMMB++ },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			altered := frame
			mutate(&altered)
			if qualificationSmokeProbeFrameMatches(altered, request, instance) {
				t.Fatal("mismatched frozen target frame was accepted")
			}
		})
	}
}

type qualificationHTTPProbeRuntimeVMM struct {
	*qualificationRestoreRuntimeVMM
	statusCode int
	probed     []string
}

func (v *qualificationHTTPProbeRuntimeVMM) ProbeEnvironmentQualificationHTTP(_ context.Context,
	frame state.EnvironmentQualificationExecution, request state.EnvironmentWorkloadQualificationRequest,
	instance state.Instance) (EnvironmentQualificationHTTPProbeResult, error) {
	policy, _, err := state.EnvironmentQualificationSmokePolicyFor(request)
	if err != nil {
		return EnvironmentQualificationHTTPProbeResult{}, err
	}
	if !qualificationSmokeProbeFrameMatches(frame, request, instance) || policy.Method != "GET" ||
		policy.Path != "/reviewed-ready" || policy.Port != 8087 {
		return EnvironmentQualificationHTTPProbeResult{}, state.ErrConflict
	}
	v.probed = append(v.probed, instance.ID)
	return EnvironmentQualificationHTTPProbeResult{StatusCode: v.statusCode, Completed: true}, nil
}

func TestEnvironmentQualificationGraphSmokeVisitorExecutesFrozenProbeForEveryRestoredMember(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}},
		api.ExecutionModeRequest, api.ExecutionModeService)
	vmm := &qualificationHTTPProbeRuntimeVMM{qualificationRestoreRuntimeVMM: &qualificationRestoreRuntimeVMM{
		qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}),
	}, statusCode: 204}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	page, err := engine.DispatchEnvironmentWorkloadQualificationGraphsWithHTTPHealth(t.Context(), engine.defaultLocalNodeID,
		"graph-smoke", "", 1)
	if err != nil || page.Restored != 1 || page.Executed != 1 || len(vmm.probed) != 2*len(requests) {
		t.Fatalf("graph smoke dispatch = %+v, probes=%v, err=%v", page, vmm.probed, err)
	}
	seen := make(map[string]bool, len(requests))
	sourceProbes := make(map[string]bool, len(requests))
	for _, instanceID := range vmm.probed {
		status, err := store.EnvironmentQualificationExecution(t.Context(), instanceID)
		if err != nil {
			t.Fatalf("probed qualification execution %s is missing: %v", instanceID, err)
		}
		if status.CaptureInstanceID == "" {
			if instanceID != status.Execution.InstanceID || sourceProbes[status.Execution.RequestID] {
				t.Fatalf("source probe did not use the unique capture execution: %+v", status)
			}
			sourceProbes[status.Execution.RequestID] = true
			continue
		}
		if status.RetiredAt == nil || status.Retirement == nil || seen[status.Execution.RequestID] {
			t.Fatalf("smoke did not use a distinct restore execution: %+v %v", status, err)
		}
		seen[status.Execution.RequestID] = true
		smoke, err := store.EnvironmentQualificationSmokeReceipt(t.Context(), status.Execution.RequestID, status.Execution.Attempt)
		if err != nil || smoke.InstanceID != instanceID || smoke.CaptureInstanceID != status.Execution.CaptureInstanceID ||
			smoke.PolicyID != "http-healthz-v1" || len(smoke.ResultSHA256) != 64 {
			t.Fatalf("restored smoke receipt for %s = %+v, %v", status.Execution.Resource, smoke, err)
		}
	}
	if len(seen) != len(requests) || len(sourceProbes) != len(requests) {
		t.Fatalf("health probes/receipts cover source=%d restore=%d requests=%d", len(sourceProbes), len(seen), len(requests))
	}
}

func TestEnvironmentQualificationGraphSourceProbeRunsBeforeCaptureWithoutWritingSmokeEvidence(t *testing.T) {
	store, requests := claimedQualificationGraphFixture(t)
	vmm := &qualificationHTTPProbeRuntimeVMM{qualificationRestoreRuntimeVMM: &qualificationRestoreRuntimeVMM{
		qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}),
	}, statusCode: 204}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	err := engine.WithEnvironmentQualificationGraphRuntimes(t.Context(), requests, func(ctx context.Context, instances map[string]state.Instance) error {
		if err := engine.validateQualificationGraphServiceRoutes(ctx, instances); err != nil {
			return err
		}
		return engine.ProbeEnvironmentQualificationSourceGraph(ctx, instances)
	})
	if err != nil || len(vmm.probed) != len(requests) {
		t.Fatalf("source graph health probe = %v, probes=%v", err, vmm.probed)
	}
	probed := make(map[string]bool, len(vmm.probed))
	for _, instanceID := range vmm.probed {
		probed[instanceID] = true
	}
	for _, request := range requests {
		if !probed[request.ReservedInstanceID] {
			t.Fatalf("source probe did not use reserved instance %s: %v", request.ReservedInstanceID, vmm.probed)
		}
		if _, err := store.EnvironmentQualificationSmokeReceipt(t.Context(), request.ID, request.Attempt); err == nil {
			t.Fatalf("source health check incorrectly created a restored smoke receipt for %s", request.Resource)
		}
		if _, err := store.EnvironmentQualificationSnapshotReceipt(t.Context(), request.ReservedInstanceID); err == nil {
			t.Fatalf("source health check unexpectedly captured %s", request.Resource)
		}
	}
}

func TestEnvironmentQualificationGraphSmokeVisitorRejectsNon2xxWithoutSmokeReceipts(t *testing.T) {
	store, _, requests := queuedQualificationExecutionFixtureWithBindings(t,
		map[string]api.EnvironmentServiceBinding{"backend": {Workload: "api2", EnvKey: "BACKEND_URL"}},
		api.ExecutionModeRequest, api.ExecutionModeService)
	vmm := &qualificationHTTPProbeRuntimeVMM{qualificationRestoreRuntimeVMM: &qualificationRestoreRuntimeVMM{
		qualificationRuntimeVMM: newQualificationRuntimeVMM(&fakeVMM{}),
	}, statusCode: 302}
	engine := newEngine(t, store, vmm, &fakeNotifier{}, "test-fc").WithEnvironmentQualificationServiceProxy(
		func(context.Context, string) (string, error) { return "http://10.100.0.1:10081", nil })
	page, err := engine.DispatchEnvironmentWorkloadQualificationGraphsWithRestore(t.Context(), engine.defaultLocalNodeID,
		"graph-smoke", "", 1, func(context.Context, map[string]state.Instance) error { return nil }, engine.ProbeEnvironmentQualificationGraph)
	if err == nil || page.Restored != 0 || page.Executed != 0 || len(vmm.probed) != 1 {
		t.Fatalf("redirect response was accepted as smoke: page=%+v probes=%v err=%v", page, vmm.probed, err)
	}
	for _, request := range requests {
		if _, err := store.EnvironmentQualificationSmokeReceipt(t.Context(), request.ID, request.Attempt); err == nil {
			t.Fatalf("failed graph smoke recorded a receipt for %s", request.Resource)
		}
	}
}
