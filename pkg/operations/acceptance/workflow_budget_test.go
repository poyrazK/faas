// adr: 20261016110803
package acceptance_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// budgetStep scripts one HTTP step: how long it works before answering 200,
// or that it works until its attempt deadline cuts it.
type budgetStep struct {
	work time.Duration
	hang bool
}

type workflowBudgetFixture struct {
	workflowOperationFixture
	orchestrator *sched.WorkflowOrchestrator
	mu           *sync.Mutex
	calls        map[string]int
}

// workflowBudgetSetup deploys validate -> cool (one-second timer) -> finish;
// each HTTP step has a five-second timeout.
func workflowBudgetSetup(t *testing.T, store state.Store, limits api.OperationLimits, steps map[string]budgetStep) (workflowBudgetFixture, error) {
	t.Helper()
	ctx := context.Background()
	base := workflowOperationSetup(t, store)
	spec := api.WorkflowSpec{Name: "charge-chain", Steps: []api.WorkflowStepSpec{
		{Name: "validate", Path: "/validate", Timeout: 5 * time.Second},
		{Name: "cool", WaitForDuration: time.Second, DependsOn: []string{"validate"}},
		{Name: "finish", Path: "/finish", Timeout: 5 * time.Second, DependsOn: []string{"cool"}},
	}}
	raw, err := json.Marshal([]api.WorkflowSpec{spec})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: base.app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:operation-budget", Workflows: raw})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	def, err := base.ops.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: base.account.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{
		AppID: base.app.ID, Scope: dep.Scope, DeploymentID: dep.ID, Spec: api.OperationDefinitionSpec{
			Name: "charge", Workflow: spec.Name, Method: "POST", Path: "/charges", Owner: api.OperationOwnerPlatformTenant,
			InputSchema:    []byte(`{"type":"object","required":["count"],"properties":{"count":{"type":"integer","minimum":1}},"additionalProperties":false}`),
			OutputSchema:   []byte(`{"type":"object","required":["file"],"properties":{"file":{"type":"string"}},"additionalProperties":false}`),
			ProgressStages: []string{"validate", "cool", "finish"},
			Limits:         &limits,
		},
	}})
	f := workflowBudgetFixture{workflowOperationFixture: base, mu: &sync.Mutex{}, calls: map[string]int{}}
	if err != nil {
		return f, err
	}
	f.definition = def
	f.admission.DefinitionID, f.admission.IdempotencyKey = def.ID, "charge-1"
	executor := operationWorkflowExecutor{execute: func(ctx context.Context, _ sched.WorkflowStepIdentity, path string, _ map[string]string, _ []byte) (int, []byte, error) {
		f.mu.Lock()
		f.calls[path]++
		f.mu.Unlock()
		step := steps[path]
		if step.hang {
			<-ctx.Done()
			return 0, nil, ctx.Err()
		}
		time.Sleep(step.work)
		if path == "/finish" {
			return 200, []byte(`{"file":"charge.csv"}`), nil
		}
		return 200, []byte(`{"ok":true}`), nil
	}}
	f.orchestrator = sched.NewWorkflowOrchestrator(store, executor, nil, nil, nil)
	return f, nil
}

func (f workflowBudgetFixture) called(path string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[path]
}

// drive runs due work until the Operation reaches want.
func (f workflowBudgetFixture) drive(t *testing.T, id string, want api.OperationState) state.Operation {
	t.Helper()
	ctx := context.Background()
	deadline := time.Now().Add(15 * time.Second)
	for {
		if err := f.orchestrator.DispatchTick(ctx); err != nil {
			t.Fatalf("dispatch: %v", err)
		}
		op := f.read(ctx, t, id)
		if op.State == want {
			return op
		}
		if time.Now().After(deadline) {
			t.Fatalf("operation state %s failure=%q usage=%+v, want %s", op.State, op.FailureCode, op.Usage, want)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestWorkflowOperationBudgetCountsOnlyStepWork(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		f, err := workflowBudgetSetup(t, store, api.OperationLimits{MaxExecutionSeconds: 5}, map[string]budgetStep{"/validate": {work: 100 * time.Millisecond}})
		if err != nil {
			t.Fatal(err)
		}
		op := f.drive(t, f.start(t).ID, api.OperationSucceeded)
		// The one-second timer is not work: two attempts, well under a second.
		if op.Usage == nil || op.Usage.Attempts != 2 || op.Usage.ExecutionMS < 100 || op.Usage.ExecutionMS >= 1000 {
			t.Fatalf("usage %+v", op.Usage)
		}
	})
}

func TestWorkflowOperationBudgetIsSharedAcrossSteps(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		f, err := workflowBudgetSetup(t, store, api.OperationLimits{MaxExecutionSeconds: 1}, map[string]budgetStep{
			"/validate": {work: 600 * time.Millisecond}, "/finish": {hang: true}})
		if err != nil {
			t.Fatal(err)
		}
		started := time.Now()
		op := f.drive(t, f.start(t).ID, api.OperationRequiresReconciliation)
		// finish has a five-second timeout but only the budget's remaining
		// ~400 ms; a cut attempt may have had an effect, so it is uncertain.
		if f.called("/validate") != 1 || f.called("/finish") != 1 || op.FailureCode != "execution_outcome_unknown" || time.Since(started) > 4*time.Second {
			t.Fatalf("validate=%d finish=%d failure=%q after %s", f.called("/validate"), f.called("/finish"), op.FailureCode, time.Since(started))
		}
		if op.Usage == nil || op.Usage.Attempts != 2 || op.Usage.ExecutionMS < 1000 || op.Usage.ExecutionMS > 2500 {
			t.Fatalf("usage %+v", op.Usage)
		}
		retry := api.OperationRecoveryRequest{RecoveryID: "retry-after-budget", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "provider shows no charge"}
		if _, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, retry); !errors.Is(err, state.ErrOperationLimitExceeded) {
			t.Fatalf("recovery after the budget was spent: %v", err)
		}
		record := api.OperationRecoveryRequest{RecoveryID: "record-failure", ExpectedGeneration: 1, Resolution: "failed", Evidence: "provider shows no charge"}
		if got, err := f.ops.RecoverOperation(ctx, f.account.ID, f.tenant.ID, op.ID, record); err != nil || got.State != api.OperationFailed {
			t.Fatalf("recording an outcome after the budget: %s %v", got.State, err)
		}
	})
}

func TestWorkflowOperationRejectsMaxAttempts(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		if _, err := workflowBudgetSetup(t, store, api.OperationLimits{MaxAttempts: 2}, nil); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("workflow max_attempts: %v", err)
		}
	})
}
