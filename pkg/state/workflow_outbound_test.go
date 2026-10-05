package state

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/onebox-faas/faas/pkg/api"
	"testing"
	"time"
)

func TestWorkflowOutboundPublicationAndAttemptFencing(t *testing.T) {
	workflowOutboundRecoveryStores(t, func(t *testing.T, store Store, recoverRun func(string)) {
		ctx := context.Background()
		app, _ := seedWorkflowSchedule(t, store, "allow")
		bindingStore := store.(OutboundBindingStore)
		offer := memCustomerOutboundOffer(app.AccountID, "crm")
		if _, err := bindingStore.CreateOutboundIntegration(ctx, offer); err != nil {
			t.Fatal(err)
		}
		spec := api.WorkflowSpec{Name: "crm", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: offer.ID, Method: "POST", Path: "/v1/contacts", IdempotencySupported: true}, Retry: &api.WorkflowRetrySpec{MaxAttempts: 3}}}}
		definition, _ := json.Marshal(spec)
		author := store.(AutomationStore)
		draft, err := author.MutateAutomation(ctx, app.ID, spec.Name, AutomationMutation{Action: "save", Draft: definition})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := author.MutateAutomation(ctx, app.ID, spec.Name, AutomationMutation{Action: "publish", ExpectedVersion: draft.Version}); !errors.Is(err, ErrAutomationInvalid) {
			t.Fatalf("published unbound integration: %v", err)
		}
		if err := bindingStore.SetOutboundCredential(ctx, app.AccountID, offer.ID, []byte("sealed-placeholder")); err != nil {
			t.Fatal(err)
		}
		if _, err := bindingStore.BindOutboundIntegration(ctx, app.AccountID, app.ID, offer.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := author.MutateAutomation(ctx, app.ID, spec.Name, AutomationMutation{Action: "publish", ExpectedVersion: draft.Version}); err != nil {
			t.Fatal(err)
		}
		if err := bindingStore.UpdateOutboundBindingPolicy(ctx, app.AccountID, app.ID, offer.ID, []string{"GET"}, []string{"/v1"}); err != nil {
			t.Fatal(err)
		}
		if err := store.(WorkflowOutboundStore).ValidateWorkflowOutboundBindings(ctx, app.ID, spec); !errors.Is(err, ErrAutomationInvalid) {
			t.Fatalf("ignored narrowed route: %v", err)
		}
		if err := bindingStore.UpdateOutboundBindingPolicy(ctx, app.AccountID, app.ID, offer.ID, []string{"POST"}, []string{"/v1"}); err != nil {
			t.Fatal(err)
		}
		run := &WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, DefinitionSnapshot: definition}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(ctx, run.ID, []*WorkflowStep{{StepName: "send"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, json.RawMessage(`{"id":1}`)); err != nil {
			t.Fatal(err)
		}
		outboundStore := store.(WorkflowOutboundStore)
		first, err := outboundStore.GetWorkflowOutboundAttempt(ctx, run.ID, "send", 1)
		if err != nil {
			t.Fatal(err)
		}
		if first.Token == "" {
			t.Fatal("missing private attempt token")
		}
		if _, err := outboundStore.GetWorkflowOutboundAttempt(ctx, run.ID, "send", 2); err == nil {
			t.Fatal("accepted foreign attempt")
		}
		// Expire the private lease, then recover with a new attempt and token.
		recoverRun(run.ID)
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, "send")
		if err != nil || len(attempts) != 1 || attempts[0].Status != WorkflowAttemptStatusFailed {
			t.Fatalf("recovered attempt remains active: %#v %v", attempts, err)
		}
		steps, _ := store.GetWorkflowSteps(ctx, run.ID)
		if steps[0].Attempt != 1 || steps[0].Status != WorkflowStepStatusPending {
			t.Fatalf("recovery reused outbound attempt: %+v", steps[0])
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, json.RawMessage(`{}`)); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("stale worker rolled back the attempt count: %v", err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 2, json.RawMessage(`{"id":1}`)); err != nil {
			t.Fatal(err)
		}
		second, err := outboundStore.GetWorkflowOutboundAttempt(ctx, run.ID, "send", 2)
		if err != nil || second.Token == first.Token {
			t.Fatalf("attempt token reused: %v", err)
		}
		if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "send", WorkflowStepStatusSucceeded, 1, nil, json.RawMessage(`{"stale":true}`), nil); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("stale completion overwrote current attempt: %v", err)
		}
		if err := store.ScheduleWorkflowStepRetryWithHTTPStatus(ctx, run.ID, "send", 1, time.Now().Add(time.Minute), nil, "stale retry"); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("stale failure parked current attempt: %v", err)
		}
		if current, err := outboundStore.GetWorkflowOutboundAttempt(ctx, run.ID, "send", 2); err != nil || current.Token != second.Token {
			t.Fatalf("stale worker disturbed active attempt: %v", err)
		}
		if _, err := store.CancelWorkflowRun(ctx, run.ID, "cancelled"); err != nil {
			t.Fatal(err)
		}
		if _, err := outboundStore.GetWorkflowOutboundAttempt(ctx, run.ID, "send", 2); err == nil {
			t.Fatal("cancelled attempt remained active")
		}
		if err := store.MarkWorkflowStepAttemptStatus(ctx, run.ID, "send", WorkflowStepStatusSucceeded, 2, nil, json.RawMessage(`{}`), nil); !errors.Is(err, ErrWorkflowOutboundAttemptExpired) {
			t.Fatalf("cancelled completion changed the run: %v", err)
		}
		attempts, err = store.GetWorkflowStepAttempts(ctx, run.ID, "send")
		if err != nil || len(attempts) != 2 || attempts[1].Status != WorkflowAttemptStatusFailed || attempts[1].FinishedAt == nil {
			t.Fatalf("cancelled attempt ledger remains running: %+v %v", attempts, err)
		}
	})
}

func expireOutboundLease(t *testing.T, store Store, runID string) {
	t.Helper()
	switch store := store.(type) {
	case *MemStore:
		store.mu.Lock()
		store.workflowRunLeases[runID] = time.Now().Add(-time.Minute)
		store.mu.Unlock()
	case *PgStore:
		if _, err := store.pool.Exec(context.Background(), `UPDATE workflow_runs SET lease_until=now()-interval '1 minute' WHERE id=$1`, runID); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkflowOutboundUnknownMutationIsNotRepeated(t *testing.T) {
	workflowOutboundRecoveryStores(t, func(t *testing.T, store Store, recoverRun func(string)) {
		ctx := context.Background()
		app, _ := seedWorkflowSchedule(t, store, "allow")
		spec := api.WorkflowSpec{Name: "unsafe", Steps: []api.WorkflowStepSpec{{Name: "send", Outbound: &api.WorkflowOutboundSpec{IntegrationID: "00000000-0000-0000-0000-000000000001", Method: "POST", Path: "/send"}}}}
		snapshot, _ := json.Marshal(spec)
		run := &WorkflowRun{AppID: app.ID, WorkflowName: spec.Name, DefinitionSnapshot: snapshot}
		if err := store.CreateWorkflowRun(ctx, run); err != nil {
			t.Fatal(err)
		}
		if err := store.CreateWorkflowSteps(ctx, run.ID, []*WorkflowStep{{StepName: "send"}}); err != nil {
			t.Fatal(err)
		}
		if _, err := store.ClaimNextDueWorkflowRun(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := store.StartWorkflowStep(ctx, run.ID, "send", 1, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
		recoverRun(run.ID)
		steps, err := store.GetWorkflowSteps(ctx, run.ID)
		if err != nil || steps[0].Status != WorkflowStepStatusDead || steps[0].Error == nil {
			t.Fatalf("unsafe mutation retried: %#v %v", steps, err)
		}
		attempts, err := store.GetWorkflowStepAttempts(ctx, run.ID, "send")
		if err != nil || len(attempts) != 1 || attempts[0].Status != WorkflowAttemptStatusFailed {
			t.Fatalf("uncertain attempt ledger: %#v %v", attempts, err)
		}
	})
}

func workflowOutboundRecoveryStores(t *testing.T, check func(*testing.T, Store, func(string))) {
	t.Helper()
	for _, explicit := range []bool{false, true} {
		name := "lease expiry"
		if explicit {
			name = "explicit recovery"
		}
		t.Run(name, func(t *testing.T) {
			workflowScheduleStores(t, func(t *testing.T, store Store) {
				check(t, store, func(runID string) {
					t.Helper()
					if explicit {
						if err := store.RecoverWorkflowRun(context.Background(), runID); err != nil {
							t.Fatal(err)
						}
					} else {
						expireOutboundLease(t, store, runID)
					}
					if _, err := store.ClaimNextDueWorkflowRun(context.Background()); err != nil {
						t.Fatal(err)
					}
				})
			})
		})
	}
}
