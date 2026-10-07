package acceptance_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestOperationWorkflowControlReadOnlyAndResume(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := t.Context()
		f := workflowOperationSetup(t, store)
		op := f.start(t)
		control := store.(state.OperationWorkflowControlStore)
		nodeID := uuid.NewString()
		if node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName); err == nil {
			nodeID = node.ID
		}
		instance, err := store.CreateInstance(ctx, f.app.ID, f.definition.DeploymentID, string(state.StateRunning), 128, nodeID, "")
		if err != nil {
			t.Fatal(err)
		}
		var old state.OperationWorkflowAuthority
		var oldDeadline time.Time
		calls := map[string]int{}
		executor := operationWorkflowExecutor{execute: func(ctx context.Context, _ sched.WorkflowStepIdentity, path string, h map[string]string, _ []byte) (int, []byte, error) {
			calls[path]++
			generation, _ := strconv.Atoi(h[api.OperationGenerationHeader])
			attempt, _ := strconv.Atoi(h[api.OperationAttemptHeader])
			a := state.OperationWorkflowAuthority{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID, RunID: op.WorkflowRunID, StepName: h[api.OperationWorkflowStepHeader], Generation: generation, Attempt: attempt, Capability: h[api.OperationWorkflowCapabilityHeader]}
			before := f.read(t, op.ID)
			runBefore, _ := store.GetWorkflowRun(ctx, a.RunID)
			stepsBefore, _ := store.GetWorkflowSteps(ctx, a.RunID)
			attemptsBefore, _ := store.GetWorkflowStepAttempts(ctx, a.RunID, a.StepName)
			first, err := control.WorkflowOperationExecutionControl(ctx, op.ID, a)
			if err != nil || first.WorkflowRunID != a.RunID || first.WorkflowStep != a.StepName || first.Generation != generation || first.Attempt != attempt || first.CancellationRequested || !first.DeadlineAt.Equal(attemptsBefore[len(attemptsBefore)-1].StartedAt.Add(api.WorkflowStepDefaultTimeout)) || first.LeaseExpiresAt.After(first.DeadlineAt) {
				t.Fatalf("native observation=%+v %v", first, err)
			}
			second, err := control.WorkflowOperationExecutionControl(ctx, op.ID, a)
			if err != nil || !second.DeadlineAt.Equal(first.DeadlineAt) || !second.LeaseExpiresAt.Equal(first.LeaseExpiresAt) || second.ObservedAt.Before(first.ObservedAt) {
				t.Fatal("read extended or replaced time bounds", err)
			}
			runAfter, _ := store.GetWorkflowRun(ctx, a.RunID)
			stepsAfter, _ := store.GetWorkflowSteps(ctx, a.RunID)
			attemptsAfter, _ := store.GetWorkflowStepAttempts(ctx, a.RunID, a.StepName)
			if !reflect.DeepEqual(before, f.read(t, op.ID)) || !reflect.DeepEqual(runBefore, runAfter) || !reflect.DeepEqual(stepsBefore, stepsAfter) || !reflect.DeepEqual(attemptsBefore, attemptsAfter) {
				t.Fatal("control read changed execution, operation, report quota or events")
			}
			for _, mutate := range []func(*state.OperationWorkflowAuthority){
				func(p *state.OperationWorkflowAuthority) { p.AccountID = uuid.NewString() },
				func(p *state.OperationWorkflowAuthority) { p.AppID = uuid.NewString() },
				func(p *state.OperationWorkflowAuthority) { p.InstanceID = uuid.NewString() },
				func(p *state.OperationWorkflowAuthority) { p.RunID = uuid.NewString() },
				func(p *state.OperationWorkflowAuthority) { p.StepName = "foreign" },
				func(p *state.OperationWorkflowAuthority) { p.Generation++ },
				func(p *state.OperationWorkflowAuthority) { p.Attempt++ },
				func(p *state.OperationWorkflowAuthority) { p.Capability = uuid.NewString() },
			} {
				invalid := a
				mutate(&invalid)
				if _, err := control.WorkflowOperationExecutionControl(ctx, op.ID, invalid); err == nil {
					t.Fatal("substituted native authority accepted")
				}
			}
			if path == "/transform" {
				if calls[path] == 1 {
					old, oldDeadline = a, first.DeadlineAt
					return 503, nil, nil
				}
				if _, err := control.WorkflowOperationExecutionControl(ctx, op.ID, old); !errors.Is(err, state.ErrOperationStaleAttempt) {
					t.Fatal("old recovery generation retained authority", err)
				}
				if !first.DeadlineAt.After(oldDeadline) || attemptsAfter[0].StartedAt.Equal(attemptsAfter[1].StartedAt) {
					t.Fatal("resume reused the earlier attempt's time budget")
				}
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
		if _, err := control.WorkflowOperationExecutionControl(ctx, op.ID, old); !errors.Is(err, state.ErrOperationStaleAttempt) {
			t.Fatal("unconfirmed outcome retained authority", err)
		}
		if _, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, api.OperationRecoveryRequest{RecoveryID: "fresh-budget", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "the failed action can safely resume"}); err != nil {
			t.Fatal(err)
		}
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		if f.read(t, op.ID).State != api.OperationSucceeded || calls["/collect"] != 1 || calls["/transform"] != 2 || calls["/finish"] != 1 {
			t.Fatal("control changed native recovery semantics", calls)
		}
	})
}

func TestOperationWorkflowControlDeadlineFencesArtifactsAndLateSuccess(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := t.Context()
		f := workflowOperationSetupWithTimeout(t, store, 400*time.Millisecond)
		op := f.start(t)
		nodeID := uuid.NewString()
		if node, err := store.ComputeNodeByName(ctx, state.DefaultLocalNodeName); err == nil {
			nodeID = node.ID
		}
		instance, err := store.CreateInstance(ctx, f.app.ID, f.definition.DeploymentID, string(state.StateRunning), 128, nodeID, "")
		if err != nil {
			t.Fatal(err)
		}
		executor := operationWorkflowExecutor{execute: func(execCtx context.Context, _ sched.WorkflowStepIdentity, path string, h map[string]string, _ []byte) (int, []byte, error) {
			if path != "/finish" {
				return 200, []byte(`{"rows":[1]}`), nil
			}
			a := state.OperationWorkflowAuthority{AccountID: f.account.ID, AppID: f.app.ID, InstanceID: instance.ID, RunID: op.WorkflowRunID, StepName: "finish", Generation: 1, Attempt: 1, Capability: h[api.OperationWorkflowCapabilityHeader]}
			control := store.(state.OperationWorkflowControlStore)
			value, err := control.WorkflowOperationExecutionControl(ctx, op.ID, a)
			if err != nil {
				t.Fatal(err)
			}
			// Keep the scheduler lease live beyond the fixed attempt deadline.
			if err := store.(state.WorkflowRunLeaseStore).ExtendWorkflowRunLease(ctx, a.RunID, time.Minute); err != nil {
				t.Fatal(err)
			}
			timer := time.NewTimer(time.Until(value.DeadlineAt) + 10*time.Millisecond)
			defer timer.Stop()
			<-timer.C
			if execCtx.Err() == nil {
				t.Fatal("scheduler did not stop at the captured deadline")
			}
			if _, err := control.WorkflowOperationExecutionControl(ctx, op.ID, a); !errors.Is(err, state.ErrOperationStaleAttempt) {
				t.Fatal("live lease extended expired attempt authority", err)
			}
			req := api.OperationArtifactRequest{ReportID: "csv", Name: "export.csv", URI: fmt.Sprintf("obj://%s/%s/export.csv", f.app.ID, uuid.NewString()), SizeBytes: 0, SHA256: "sha256:e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"}
			if _, err := store.(state.OperationWorkflowArtifactStore).ReuseWorkflowOperationArtifact(ctx, op.ID, a, req); !errors.Is(err, state.ErrOperationStaleAttempt) {
				t.Fatal("artifact preflight granted expired authority", err)
			}
			if err := store.MarkWorkflowStepAttemptStatus(ctx, a.RunID, a.StepName, state.WorkflowStepStatusSucceeded, a.Attempt, nil, []byte(`{"file":"late.csv"}`), nil); !errors.Is(err, state.ErrOperationStaleAttempt) {
				t.Fatal("expired attempt committed a successful output", err)
			}
			return 200, []byte(`{"file":"late.csv"}`), nil
		}}
		if err := sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil).DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		final := f.read(t, op.ID)
		if final.State != api.OperationRequiresReconciliation || len(final.Result) != 0 || final.CompletionDelivery.State != "awaiting_outcome" {
			t.Fatalf("deadline expiry accepted late success or authorized retry: %+v", final)
		}
	})
}
