// adr: 1196
package acceptance_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type operationLimitStore interface {
	operationLifecycleStore
	state.OperationDeadlineStore
	InvocationByID(context.Context, string) (state.Invocation, error)
	CreateInstance(context.Context, string, string, string, int, string, string) (state.Instance, error)
	ComputeNodeByName(context.Context, string) (state.ComputeNode, error)
	ListDeadlineBreachedInvocations(context.Context, time.Time, int) ([]string, error)
	ForceDeadlineBreachedInvocationsWithDetails(context.Context, []string) ([]state.Invocation, error)
}

type operationLimitFixture struct {
	s        operationLimitStore
	acct     state.Account
	tenant   state.PlatformTenant
	instance state.Instance
	def      state.OperationDefinition
}

func operationLimitSetup(t *testing.T, s operationLimitStore, limits api.OperationLimits) operationLimitFixture {
	t.Helper()
	ctx, acct, app, base, alice, _ := operationFixture(t, s)
	spec := operationSpec()
	spec.Name, spec.Path, spec.Limits = "bounded-export", "/bounded-exports", &limits
	def, err := s.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{AppID: app.ID, Scope: base.Scope, DeploymentID: base.DeploymentID, Spec: spec}})
	if err != nil {
		t.Fatal(err)
	}
	if def.Revision == base.Revision {
		t.Fatal("limits did not change the definition revision")
	}
	node := uuid.NewString()
	if n, err := s.ComputeNodeByName(ctx, state.DefaultLocalNodeName); err == nil {
		node = n.ID
	}
	instance, err := s.CreateInstance(ctx, app.ID, def.DeploymentID, "stopped", 128, node, "")
	if err != nil {
		t.Fatal(err)
	}
	return operationLimitFixture{s: s, acct: acct, tenant: alice, instance: instance, def: def}
}

func (f operationLimitFixture) admit(t *testing.T, key string) state.Operation {
	t.Helper()
	op, _, err := f.s.AdmitOperation(t.Context(), state.OperationAdmission{AccountID: f.acct.ID, DefinitionID: f.def.ID, PlatformTenantID: f.tenant.ID, IdempotencyKey: key, Input: []byte(`{"count":1}`)})
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func (f operationLimitFixture) read(t *testing.T, id string) state.Operation {
	t.Helper()
	op, err := f.s.OperationByID(t.Context(), f.acct.ID, f.tenant.ID, id)
	if err != nil {
		t.Fatal(err)
	}
	return op
}

func (f operationLimitFixture) events(t *testing.T, id string) []string {
	t.Helper()
	page, err := f.s.OperationEvents(t.Context(), f.acct.ID, f.tenant.ID, id, 0, api.OperationEventsPageMax)
	if err != nil {
		t.Fatal(err)
	}
	types := []string{}
	for _, e := range page.Events {
		types = append(types, e.Type)
	}
	return types
}

func sleepPast(at time.Time) { time.Sleep(time.Until(at) + 50*time.Millisecond) }

func testOperationHTTPDeadline(t *testing.T, s operationLimitStore) {
	ctx := t.Context()
	f := operationLimitSetup(t, s, api.OperationLimits{DeadlineSeconds: 1, MaxAttempts: 2})
	op := f.admit(t, "deadline")
	if op.DeadlineAt == nil || op.DeadlineAt.Sub(op.CreatedAt) != time.Second || op.Limits == nil || op.Limits.MaxAttempts != 2 || op.Usage == nil {
		t.Fatalf("admitted limits: %+v %v %+v", op.Limits, op.DeadlineAt, op.Usage)
	}
	inv, err := s.InvocationByID(ctx, op.CurrentInvocationID)
	if err != nil || inv.DeadlineAt == nil || !inv.DeadlineAt.Equal(*op.DeadlineAt) || inv.RetryPolicy().MaxAttempts != 2 {
		t.Fatalf("execution inherits limits: deadline=%v policy=%+v %v", inv.DeadlineAt, inv.RetryPolicy(), err)
	}
	sleepPast(*op.DeadlineAt)
	if _, err := s.ClaimInvocationWithCap(ctx, inv.ID, f.instance.ID, 60, 100); err == nil {
		t.Fatal("an execution past its deadline was claimed")
	}
	ids, err := s.ListDeadlineBreachedInvocations(ctx, time.Now(), 10)
	if err != nil || !slices.Contains(ids, inv.ID) {
		t.Fatalf("deadline reaper missed the execution: %v %v", ids, err)
	}
	if _, err := s.ForceDeadlineBreachedInvocationsWithDetails(ctx, []string{inv.ID}); err != nil {
		t.Fatal(err)
	}
	got := f.read(t, op.ID)
	if got.State != api.OperationFailed || got.FailureCode != api.OperationFailureDeadlineExceeded {
		t.Fatalf("settled %s/%s", got.State, got.FailureCode)
	}
	if types := f.events(t, op.ID); types[len(types)-1] != "failed" {
		t.Fatalf("events %v", types)
	}
}

func testOperationHTTPBudget(t *testing.T, s operationLimitStore) {
	ctx := t.Context()
	f := operationLimitSetup(t, s, api.OperationLimits{MaxExecutionSeconds: 1})
	op := f.admit(t, "budget")
	claimed, err := s.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, f.instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if claimed.DeadlineAt == nil || time.Until(*claimed.DeadlineAt) > time.Second {
		t.Fatalf("claim not capped at the remaining budget: %v", claimed.DeadlineAt)
	}
	sleepPast(*claimed.DeadlineAt)
	if err := s.FailInvocation(ctx, claimed.ID, "budget cut the attempt", time.Millisecond, 10, state.WithClaimAttempt(claimed.Attempts), state.WithDispatchNotStarted()); err != nil {
		t.Fatal(err)
	}
	got := f.read(t, op.ID)
	if got.State != api.OperationFailed || got.FailureCode != api.OperationFailureBudgetExhausted || got.Usage == nil || got.Usage.Attempts != 1 || got.Usage.ExecutionMS < 1000 {
		t.Fatalf("spent budget settled %s/%s usage=%+v", got.State, got.FailureCode, got.Usage)
	}
	retry, err := s.InvocationByID(ctx, claimed.ID)
	if err != nil || retry.State != state.InvocationCancelled {
		t.Fatalf("retry left %s: %v", retry.State, err)
	}
	if _, err := s.ClaimInvocationWithCap(ctx, claimed.ID, f.instance.ID, 60, 100); err == nil {
		t.Fatal("a cancelled retry was claimed")
	}
}

func testOperationRecoveryAfterDeadline(t *testing.T, s operationLimitStore) {
	ctx := t.Context()
	f := operationLimitSetup(t, s, api.OperationLimits{DeadlineSeconds: 2})
	op := f.admit(t, "recovery")
	claimed, err := s.ClaimInvocationWithCap(ctx, op.CurrentInvocationID, f.instance.ID, 60, 100)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FailInvocation(ctx, claimed.ID, "response lost", time.Millisecond, 10, state.WithClaimAttempt(claimed.Attempts)); err != nil {
		t.Fatal(err)
	}
	if got := f.read(t, op.ID); got.State != api.OperationRequiresReconciliation {
		t.Fatalf("lost response settled %s", got.State)
	}
	sleepPast(*op.DeadlineAt)
	retry := api.OperationRecoveryRequest{RecoveryID: "retry-after-deadline", ExpectedGeneration: 1, Resolution: "safe_to_retry", Evidence: "provider shows no charge"}
	if _, err := s.RecoverOperation(ctx, f.acct.ID, f.tenant.ID, op.ID, retry); !errors.Is(err, state.ErrOperationLimitExceeded) {
		t.Fatalf("recovery extended the deadline: %v", err)
	}
	record := api.OperationRecoveryRequest{RecoveryID: "record-failure", ExpectedGeneration: 1, Resolution: "failed", Evidence: "provider shows no charge"}
	if got, err := s.RecoverOperation(ctx, f.acct.ID, f.tenant.ID, op.ID, record); err != nil || got.State != api.OperationFailed {
		t.Fatalf("recording an outcome after the deadline: %+v %v", got.State, err)
	}
}

func TestOperationHTTPLimits(t *testing.T) {
	for name, run := range map[string]func(*testing.T, operationLimitStore){
		"deadline": testOperationHTTPDeadline, "budget": testOperationHTTPBudget, "recovery": testOperationRecoveryAfterDeadline,
	} {
		t.Run(name+"/memory", func(t *testing.T) { run(t, state.NewMemStore()) })
		t.Run(name+"/postgres", func(t *testing.T) {
			s, _ := pgStore(t)
			run(t, s)
		})
	}
}

func TestOperationDeadlineEndsApprovalWait(t *testing.T) {
	operationWorkflowStores(t, func(t *testing.T, store state.Store) {
		ctx := context.Background()
		f := waitOperationSetup(t, store, api.OperationApproverAccount, time.Hour, api.OperationLimits{DeadlineSeconds: 1})
		op := f.start(t)
		if op.DeadlineAt == nil {
			t.Fatal("workflow operation has no deadline")
		}
		f.drive(t, op.ID, "approval wait", waitingFor(api.OperationWaitApproval, "review"))
		sweeper := store.(state.OperationDeadlineStore)
		if n, err := sweeper.SettleOperationDeadlines(ctx, time.Now(), 10); err != nil || n != 0 {
			t.Fatalf("swept before the deadline: %d %v", n, err)
		}
		sleepPast(*op.DeadlineAt)
		if n, err := sweeper.SettleOperationDeadlines(ctx, time.Now(), 10); err != nil || n != 1 {
			t.Fatalf("deadline sweep settled %d: %v", n, err)
		}
		got := f.read(ctx, t, op.ID)
		if got.State != api.OperationFailed || got.FailureCode != api.OperationFailureDeadlineExceeded || got.Waiting != nil {
			t.Fatalf("expired wait settled %s/%s waiting=%+v", got.State, got.FailureCode, got.Waiting)
		}
		if n, err := sweeper.SettleOperationDeadlines(ctx, time.Now(), 10); err != nil || n != 0 {
			t.Fatalf("settled twice: %d %v", n, err)
		}
		if _, err := f.decide(ctx, got, api.OperationApproverAccount, api.OperationApprovalApproved); err == nil {
			t.Fatal("approval accepted after the deadline")
		}
		if err := f.orchestrator.DispatchTick(ctx); err != nil || f.finished.Load() != 0 {
			t.Fatalf("work continued after the deadline: %d %v", f.finished.Load(), err)
		}
	})
}
