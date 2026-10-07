// adr: 641
package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationRecoveryPreviewMatchesNativeResume(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := t.Context()
		f := workflowOperationSetup(t, store)
		op := f.start(t)
		calls := map[string]int{}
		executor := operationWorkflowExecutor{execute: func(_ context.Context, _ sched.WorkflowStepIdentity, path string, _ map[string]string, _ []byte) (int, []byte, error) {
			calls[path]++
			if path == "/transform" && calls[path] == 1 {
				return 503, []byte(`{"error":"unknown external effect"}`), nil
			}
			if path == "/finish" {
				return 200, []byte(`{"file":"export.csv"}`), nil
			}
			return 200, []byte(`{"rows":[1]}`), nil
		}}
		orchestrator := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		reader := store.(state.OperationRecoveryInspectionStore)
		before := f.read(ctx, t, op.ID)
		inspection, err := reader.InspectOperationRecovery(ctx, f.account.ID, op.ID)
		if err != nil || len(inspection.Steps) != 3 || !inspection.Steps[0].Confirmed || !inspection.Steps[1].OutcomeUnknown || inspection.Steps[2].OutcomeUnknown || len(inspection.RetryBlockers) != 0 {
			t.Fatalf("inspection=%+v %v", inspection, err)
		}
		assertOperationRecoveryPreviewReadOnly(t, f, op.ID)
		assertOperationRecoveryPreviewAdmissionBlockers(t, f, op.ID)
		preview, err := reader.PreviewOperationRecovery(ctx, f.account.ID, op.ID, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "safe_to_retry"})
		if err != nil || !preview.Eligible || !preview.EvidenceRequired || preview.StartsNewExecution || preview.ClearsArtifactReferences || !reflect.DeepEqual(preview.ReopenedSteps, []string{"transform"}) || !reflect.DeepEqual(preview.ReusedSteps, []string{"collect"}) {
			t.Fatalf("preview=%+v %v", preview, err)
		}
		if !before.EventExpiresAt.Before(before.ExpiresAt) {
			t.Fatal("fixture needs independently prunable progress history")
		}
		if _, err := store.(state.OperationRetentionStore).PruneOperationState(ctx, before.EventExpiresAt.Add(time.Second), api.OperationRetentionPageMax); err != nil {
			t.Fatal(err)
		}
		pruned, err := reader.InspectOperationRecovery(ctx, f.account.ID, op.ID)
		if err != nil || !reflect.DeepEqual(pruned.Steps, preview.Inspection.Steps) || pruned.InspectionRevision != preview.Inspection.InspectionRevision {
			t.Fatal("recovery inspection depended on prunable events", err)
		}
		if _, err := reader.InspectOperationRecovery(ctx, uuid.NewString(), op.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign inspection", err)
		}
		if _, err := reader.PreviewOperationRecovery(ctx, uuid.NewString(), op.ID, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "failed"}); !errors.Is(err, state.ErrNotFound) {
			t.Fatal("foreign preview", err)
		}
		for _, req := range []api.OperationRecoveryPreviewRequest{{Resolution: "failed"}, {ExpectedGeneration: 1, Resolution: "automatic"}, {ExpectedGeneration: 1, Resolution: "safe_to_retry", Result: []byte(`{}`)}} {
			if _, err := reader.PreviewOperationRecovery(ctx, f.account.ID, op.ID, req); !errors.Is(err, state.ErrInvalidArgument) {
				t.Fatal("malformed preview", err)
			}
		}
		request := api.OperationRecoveryRequest{RecoveryID: "previewed-recovery", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "provider ledger checked; uncertain action has no external effect", ExpectedInspectionRevision: preview.Inspection.InspectionRevision}
		badRevision := request
		badRevision.ExpectedInspectionRevision = "sha256:" + strings.Repeat("0", 64)
		if _, err := f.ops.RecoverOperation(ctx, f.account.ID, "", op.ID, badRevision); !errors.Is(err, state.ErrConflict) {
			t.Fatal("stale inspection applied", err)
		}
		if !reflect.DeepEqual(before, f.read(ctx, t, op.ID)) {
			t.Fatal("rejected revision changed operation")
		}
		decision, err := store.(state.OperationRecoveryReceiptStore).RecoverOperationWithReceipt(ctx, f.account.ID, op.ID, request)
		applied := f.read(ctx, t, op.ID)
		if err != nil || applied.Generation != 2 {
			t.Fatal("preview could not apply", err)
		}
		resumes, err := store.(state.WorkflowResumeStore).ListWorkflowResumes(ctx, op.WorkflowRunID)
		if err != nil || len(resumes) != 1 || !reflect.DeepEqual(resumes[0].ResumedSteps, preview.ReopenedSteps) {
			t.Fatalf("preview diverged from native planner: %+v %v", resumes, err)
		}
		if _, err := reader.PreviewOperationRecovery(ctx, f.account.ID, op.ID, api.OperationRecoveryPreviewRequest{ExpectedGeneration: 1, Resolution: "safe_to_retry"}); !errors.Is(err, state.ErrConflict) {
			t.Fatal("stale preview accepted", err)
		}
		if replay, err := f.ops.RecoverOperation(ctx, f.account.ID, "", op.ID, request); err != nil || replay.Generation != applied.Generation || replay.RecoveryCount != 1 {
			t.Fatal("receipt replay rejected old inspection or consumed recovery", err)
		}
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		if done := f.read(ctx, t, op.ID); done.State != api.OperationSucceeded || calls["/collect"] != 1 || calls["/transform"] != 2 || calls["/finish"] != 1 {
			t.Fatalf("previewed resume=%+v calls=%v", done, calls)
		}
		replay, err := store.(state.OperationRecoveryReceiptStore).RecoverOperationWithReceipt(ctx, f.account.ID, op.ID, request)
		if err != nil || replay != decision || replay.State != api.OperationAccepted || replay.WorkflowRunID != op.WorkflowRunID || replay.InvocationID != "" {
			t.Fatal("native decision changed after confirmed-step reuse", replay, err)
		}
		resumes, err = store.(state.WorkflowResumeStore).ListWorkflowResumes(ctx, op.WorkflowRunID)
		if err != nil || len(resumes) != 1 {
			t.Fatal("receipt replay resumed native work twice", resumes, err)
		}

	})
}

func assertOperationRecoveryPreviewAdmissionBlockers(t *testing.T, f workflowOperationFixture, id string) {
	t.Helper()
	ctx := t.Context()
	reader := f.store.(state.OperationRecoveryInspectionStore)
	before := f.read(ctx, t, id)
	check := func(blocker string) {
		t.Helper()
		p, err := reader.PreviewOperationRecovery(ctx, f.account.ID, id, api.OperationRecoveryPreviewRequest{ExpectedGeneration: before.Generation, Resolution: "safe_to_retry"})
		if err != nil || p.Eligible || !slices.Contains(p.Blockers, blocker) {
			t.Fatalf("missing current blocker %s: %+v %v", blocker, p, err)
		}
	}
	if err := f.store.UpdateAccountPlan(ctx, f.account.ID, api.PlanFree); err != nil {
		t.Fatal(err)
	}
	check("plan_admission")
	if err := f.store.UpdateAccountPlan(ctx, f.account.ID, f.account.Plan); err != nil {
		t.Fatal(err)
	}
	tenants := f.store.(state.PlatformTenantStore)
	if _, err := tenants.SetPlatformTenantStatus(ctx, f.account.ID, f.tenant.ID, state.PlatformTenantSuspended); err != nil {
		t.Fatal(err)
	}
	check("customer_suspended")
	if _, err := tenants.SetPlatformTenantStatus(ctx, f.account.ID, f.tenant.ID, state.PlatformTenantActive); err != nil {
		t.Fatal(err)
	}
	var queued []string
	for range f.account.Plan.WorkflowMaxConcurrentRuns() {
		run := &state.WorkflowRun{AppID: f.app.ID, WorkflowName: "quota", DefinitionSnapshot: []byte(`{"name":"quota","steps":[{"name":"work","run":"work"}]}`)}
		if err := f.store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		queued = append(queued, run.ID)
	}
	check("workflow_concurrency_limit")
	for _, runID := range queued {
		if _, err := f.store.CancelWorkflowRun(ctx, runID, "release preview test capacity"); err != nil {
			t.Fatal(err)
		}
	}
	if !reflect.DeepEqual(before, f.read(ctx, t, id)) {
		t.Fatal("blocked previews consumed an operation recovery or changed its projection")
	}
}

func assertOperationRecoveryPreviewReadOnly(t *testing.T, f workflowOperationFixture, id string) {
	t.Helper()
	ctx := t.Context()
	reader := f.store.(state.OperationRecoveryInspectionStore)
	before := f.read(ctx, t, id)
	runBefore, _ := f.store.GetWorkflowRun(ctx, before.WorkflowRunID)
	stepsBefore, _ := f.store.GetWorkflowSteps(ctx, before.WorkflowRunID)
	eventsBefore, _ := f.ops.OperationEvents(ctx, f.account.ID, "", id, 0, 100)
	executionsBefore, _ := f.ops.OperationExecutions(ctx, f.account.ID, id, 0, 100)
	var revision string
	for _, req := range []api.OperationRecoveryPreviewRequest{
		{ExpectedGeneration: before.Generation, Resolution: "safe_to_retry"},
		{ExpectedGeneration: before.Generation, Resolution: "failed"},
		{ExpectedGeneration: before.Generation, Resolution: "cancelled"},
		{ExpectedGeneration: before.Generation, Resolution: "succeeded", Result: []byte(`{"file":"verified.csv"}`)},
		{ExpectedGeneration: before.Generation, Resolution: "succeeded", Result: []byte(`{"secret":"invalid"}`)},
	} {
		p, err := reader.PreviewOperationRecovery(ctx, f.account.ID, id, req)
		if err != nil {
			t.Fatal(err)
		}
		if revision != "" && revision != p.Inspection.InspectionRevision {
			t.Fatal("preview changed durable inspection revision")
		}
		revision = p.Inspection.InspectionRevision
		if string(req.Result) == `{"secret":"invalid"}` && (p.Eligible || !reflect.DeepEqual(p.Blockers, []string{"result_contract_invalid"})) {
			t.Fatal("invalid typed result eligible", p)
		}
		raw, _ := json.Marshal(p)
		for _, secret := range []string{"unknown external effect", "operation-results/", "obj://", "artifact_storage_keys", "execution_capability", "input_schema"} {
			if strings.Contains(string(raw), secret) {
				t.Fatal("inspection leaked private execution data", secret)
			}
		}
	}
	runAfter, _ := f.store.GetWorkflowRun(ctx, before.WorkflowRunID)
	stepsAfter, _ := f.store.GetWorkflowSteps(ctx, before.WorkflowRunID)
	eventsAfter, _ := f.ops.OperationEvents(ctx, f.account.ID, "", id, 0, 100)
	executionsAfter, _ := f.ops.OperationExecutions(ctx, f.account.ID, id, 0, 100)
	if !reflect.DeepEqual(before, f.read(ctx, t, id)) || !reflect.DeepEqual(runBefore, runAfter) || !reflect.DeepEqual(stepsBefore, stepsAfter) || !reflect.DeepEqual(eventsBefore, eventsAfter) || !reflect.DeepEqual(executionsBefore, executionsAfter) {
		t.Fatal("preview mutated execution, files, quota, delivery or evidence")
	}
}
