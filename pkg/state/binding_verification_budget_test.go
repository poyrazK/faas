package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"sync"
	"testing"
)

func TestVerificationBudgetAtomicAdmissionAndBorrowedWork(t *testing.T) {
	m, app, _, in := state.VerificationReceiptFixture(t)
	ctx := context.Background()
	in.Report.MaxConcurrency = 1
	for range 7 {
		in.Report.Steps = append(in.Report.Steps, in.Report.Steps[0])
		in.Admissions = append(in.Admissions, in.Admissions[0])
	}
	run, err := m.CreateBindingVerificationRun(ctx, app.AccountID, app.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	winner, resource, admitted, blocked := -1, "", 0, 0
	for index := range in.Report.Steps {
		wg.Go(func() {
			step, err := m.AdmitBindingVerificationRunStep(ctx, app.AccountID, app.ID, run.ID, index)
			mu.Lock()
			defer mu.Unlock()
			var full *state.VerificationRunBudgetError
			if errors.As(err, &full) {
				blocked++
				if full.Limit != 1 || full.Observed != 1 {
					t.Errorf("budget %+v", full)
				}
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			winner, resource = index, step.ResourceID
			admitted++
		})
	}
	wg.Wait()
	if admitted != 1 || blocked != 7 || len((*m.Invocations())) != 1 {
		t.Fatalf("admitted=%d blocked=%d resources=%d", admitted, blocked, len((*m.Invocations())))
	}
	// A lost response can be replayed at capacity, returning the same identity.
	step, err := m.AdmitBindingVerificationRunStep(ctx, app.AccountID, app.ID, run.ID, winner)
	if err != nil || step.ResourceID != resource {
		t.Fatalf("replay %+v %v", step, err)
	}
	changed := in
	changed.Report.MaxConcurrency = 2
	if _, err = m.CreateBindingVerificationRun(ctx, app.AccountID, app.ID, changed); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("budget mutation accepted: %v", err)
	}
	// A second receipt borrows the same unfinished resource twice; count it once.
	borrowed := in
	borrowed.Report.ID = uuid.NewString()
	borrowed.Report.Steps = []api.BindingVerificationRunStep{{Plan: api.BindingVerificationPlanStep{Action: "wait"}, ResourceID: resource, ResourceKind: "queue"}, {Plan: api.BindingVerificationPlanStep{Action: "wait"}, ResourceID: resource, ResourceKind: "queue"}, in.Report.Steps[0]}
	borrowed.Admissions = make([]state.VerificationRunAdmission, 3)
	second, err := m.CreateBindingVerificationRun(ctx, app.AccountID, app.ID, borrowed)
	if err != nil {
		t.Fatal(err)
	}
	budget, err := m.BindingVerificationRunBudget(ctx, app.AccountID, app.ID, second.ID)
	if err != nil || budget.ActiveResources != 1 || budget.WaitingSteps != 1 || !budget.Full {
		t.Fatalf("borrowed budget %+v %v", budget, err)
	}
	if _, err = m.AdmitBindingVerificationRunStep(ctx, app.AccountID, app.ID, second.ID, 2); err == nil {
		t.Fatal("borrowed work bypassed budget")
	}
	(*
	// An in-flight cancellation flag alone does not free the slot.
	m.Mu()).Lock()
	invocation := (*m.Invocations())[resource]
	invocation.State = state.InvocationDispatching
	(*m.Invocations())[resource] = invocation
	(*m.Mu()).Unlock()
	if _, err = m.CancelQueueBindingCanary(ctx, app.AccountID, app.ID, resource); err != nil {
		t.Fatal(err)
	}
	budget, err = m.BindingVerificationRunBudget(ctx, app.AccountID, app.ID, second.ID)
	if err != nil || budget.ActiveResources != 1 {
		t.Fatalf("cancellation freed running work: %+v %v", budget, err)
	}
	(*m.Mu()).Lock()
	invocation = (*m.Invocations())[resource]
	invocation.State = state.InvocationCompleted
	(*m.Invocations())[resource] = invocation
	(*m.Mu()).Unlock()
	if _, err = m.AdmitBindingVerificationRunStep(ctx, app.AccountID, app.ID, second.ID, 2); err != nil {
		t.Fatalf("settled work did not release budget: %v", err)
	}
	budget, err = m.BindingVerificationRunBudget(ctx, app.AccountID, app.ID, second.ID)
	if err != nil || budget.ActiveResources != 1 || budget.WaitingSteps != 0 {
		t.Fatalf("budget %+v %v", budget, err)
	}
}

func TestVerificationBudgetDefaultAndInvalidLimits(t *testing.T) {
	for _, limit := range []int{-1, 9} {
		m, app, _, in := state.VerificationReceiptFixture(t)
		in.Report.MaxConcurrency = limit
		if _, err := m.CreateBindingVerificationRun(context.Background(), app.AccountID, app.ID, in); !errors.Is(err, state.ErrInvalidArgument) {
			t.Fatalf("limit %d: %v", limit, err)
		}
	}
	m, app, _, in := state.VerificationReceiptFixture(t)
	run, err := m.CreateBindingVerificationRun(context.Background(), app.AccountID, app.ID, in)
	if err != nil || run.MaxConcurrency != api.BindingVerificationRunnerDefaultConcurrency {
		t.Fatalf("legacy default %+v %v", run, err)
	}
}
