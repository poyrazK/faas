package state

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/exclusivework"
)

func seedWebhookAutomation(t *testing.T, store Store) (App, InboundWebhookEndpoint, WebhookAutomationBinding) {
	t.Helper()
	ctx := context.Background()
	app, _ := seedEventWorkflow(t, store)
	endpoint, err := store.(InboundWebhookStore).CreateInboundWebhookEndpointIfUnderQuota(ctx, InboundWebhookEndpoint{AppID: app.ID, AccountID: app.AccountID, Name: "automation", Provider: InboundWebhookProviderStripe, DeliveryPath: "/", TokenHash: bytes.Repeat([]byte{1}, 32), SigningSecretSealed: []byte("sealed"), Enabled: true}, api.MustLimitsFor(api.PlanHobby))
	if err != nil {
		t.Fatal(err)
	}
	binding, err := store.(WebhookAutomationStore).SaveWebhookAutomationBinding(ctx, WebhookAutomationBindingOptions{EndpointID: endpoint.ID, AppID: app.ID, AccountID: app.AccountID, WorkflowName: "paid", EventType: "invoice.*"})
	if err != nil {
		t.Fatal(err)
	}
	return app, endpoint, binding
}

func TestWebhookAutomationQuotaSnapshotsAndRoutingModes(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, endpoint, binding := seedWebhookAutomation(t, store)
		ctx := t.Context()
		starts := store.(WebhookAutomationStore)
		receipt, handled, err := starts.AcceptWebhookAutomation(ctx, endpoint, webhookTestBody("evt_quota"), true)
		if err != nil || !handled {
			t.Fatal(receipt, err)
		}
		spec := api.WorkflowSpec{Name: "paid", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/replacement"}}}
		snapshot, _ := json.Marshal(spec)
		draft, err := store.(AutomationStore).MutateAutomation(ctx, app.ID, "paid", AutomationMutation{Action: "save", Draft: snapshot})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.(AutomationStore).MutateAutomation(ctx, app.ID, "paid", AutomationMutation{Action: "publish", ExpectedVersion: draft.Version, TakeOverManifest: true}); err != nil {
			t.Fatal(err)
		}
		var active []*WorkflowRun
		for range api.PlanHobby.WorkflowMaxConcurrentRuns() {
			run := &WorkflowRun{AppID: app.ID, WorkflowName: "quota", DefinitionSnapshot: json.RawMessage(`{"name":"quota","steps":[{"name":"main","path":"/quota"}]}`)}
			if err := store.CreateWorkflowRun(ctx, run); err != nil {
				t.Fatal(err)
			}
			active = append(active, run)
		}
		work, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		captured := work.RecipientSnapshot[0]
		if _, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, captured.ID); !errors.Is(err, ErrWorkflowRunQuotaExceeded) {
			t.Fatalf("quota=%v", err)
		}
		if _, err := store.CancelWorkflowRun(ctx, active[0].ID, "free a slot"); err != nil {
			t.Fatal(err)
		}
		runID, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, captured.ID)
		if err != nil {
			t.Fatal(err)
		}
		run, err := store.GetWorkflowRun(ctx, runID)
		if err != nil || !bytes.Contains(run.DefinitionSnapshot, []byte(`"/paid"`)) {
			t.Fatalf("published edit changed captured definition: %s %v", run.DefinitionSnapshot, err)
		}
		callbackRun := &WorkflowRun{AppID: app.ID, WorkflowName: "callback", DefinitionSnapshot: json.RawMessage(`{"name":"callback","steps":[{"name":"wait","wait_for_callback":true,"timeout":"1m"}]}`)}
		if err := store.CreateWorkflowRun(ctx, callbackRun); err != nil {
			t.Fatal(err)
		}
		callback := WorkflowCallbackWebhookBinding{ID: api.WorkflowCallbackID(callbackRun.ID, "wait"), RunID: callbackRun.ID, StepName: "wait", EndpointID: endpoint.ID, EventType: "invoice.paid", ObjectID: "in_1"}
		if _, err := store.(WorkflowCallbackWebhookBindingStore).CreateWorkflowCallbackWebhookBinding(ctx, callback); !errors.Is(err, ErrConflict) {
			t.Fatalf("callback competing with automation=%v", err)
		}
		policy := exclusivework.Policy{Name: "webhooks", Scope: "account", MemberAppIDs: []string{app.ID}, Contention: "queue", LeaseSeconds: 5, MaxAttemptSeconds: 60}
		if _, err := store.(ExclusiveWorkStore).UpsertExclusiveWorkPolicy(ctx, app.AccountID, policy); err != nil {
			t.Fatal(err)
		}
		operationBinding := ExclusiveTriggerBinding{Source: "inbound_webhook", TriggerID: endpoint.ID, AccountID: app.AccountID, PolicyName: policy.Name, Key: json.RawMessage(`"invoice"`)}
		if _, err := store.(ExclusiveTriggerBindingStore).UpsertExclusiveTriggerBinding(ctx, operationBinding); !errors.Is(err, ErrConflict) {
			t.Fatalf("operation competing with automation=%v", err)
		}
		opts := WebhookAutomationBindingOptions{EndpointID: endpoint.ID, AppID: app.ID, AccountID: app.AccountID, ExpectedVersion: binding.Version, WorkflowName: "paid", EventType: "*"}
		if err := starts.DeleteWebhookAutomationBinding(ctx, opts); err != nil {
			t.Fatal(err)
		}
		if _, err := store.(WorkflowCallbackWebhookBindingStore).CreateWorkflowCallbackWebhookBinding(ctx, callback); err != nil {
			t.Fatal(err)
		}
		opts.ExpectedVersion = 0
		if _, err := starts.SaveWebhookAutomationBinding(ctx, opts); !errors.Is(err, ErrWebhookAutomationConflict) {
			t.Fatalf("automation competing with callback=%v", err)
		}
		if err := store.(WorkflowCallbackWebhookBindingStore).DeleteWorkflowCallbackWebhookBinding(ctx, callback.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := store.(ExclusiveTriggerBindingStore).UpsertExclusiveTriggerBinding(ctx, operationBinding); err != nil {
			t.Fatal(err)
		}
		if _, err := starts.SaveWebhookAutomationBinding(ctx, opts); !errors.Is(err, ErrWebhookAutomationConflict) {
			t.Fatalf("automation competing with operation=%v", err)
		}
		if err := store.(ExclusiveTriggerBindingStore).DeleteExclusiveTriggerBinding(ctx, app.AccountID, "inbound_webhook", endpoint.ID); err != nil {
			t.Fatal(err)
		}
		replacement, err := starts.SaveWebhookAutomationBinding(ctx, opts)
		if err != nil || replacement.Version <= binding.Version {
			t.Fatalf("revision after recreation=%+v %v", replacement, err)
		}
	})
}

func TestGenericWebhookAutomationCapturesSignedEventMetadata(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, _ := seedEventWorkflow(t, store)
		ctx := t.Context()
		endpoint, err := store.(InboundWebhookStore).CreateInboundWebhookEndpointIfUnderQuota(ctx, InboundWebhookEndpoint{
			AppID: app.ID, AccountID: app.AccountID, Name: "generic-automation", Provider: InboundWebhookProviderGeneric,
			DeliveryPath: "/", TokenHash: bytes.Repeat([]byte{7}, 32), SigningSecretSealed: []byte("sealed"), Enabled: true,
		}, api.MustLimitsFor(api.PlanHobby))
		if err != nil {
			t.Fatal(err)
		}
		starts := store.(WebhookAutomationStore)
		if _, err := starts.SaveWebhookAutomationBinding(ctx, WebhookAutomationBindingOptions{
			EndpointID: endpoint.ID, AppID: app.ID, AccountID: app.AccountID, WorkflowName: "paid", EventType: "order.*",
		}); err != nil {
			t.Fatal(err)
		}
		body := json.RawMessage(`{"order_id":"ord_1","amount":150}`)
		receipt, handled, err := starts.AcceptVerifiedWebhookAutomation(ctx, endpoint, "evt_generic_1", "order.paid", body, true)
		if err != nil || !handled || receipt.Status != "accepted" || receipt.EventSource != webhookAutomationSource(InboundWebhookProviderGeneric, endpoint.ID) {
			t.Fatalf("accept=%+v handled=%v err=%v", receipt, handled, err)
		}
		duplicate, handled, err := starts.AcceptVerifiedWebhookAutomation(ctx, endpoint, "evt_generic_1", "order.paid", body, true)
		if err != nil || !handled || !duplicate.Duplicate || duplicate.ReceiptID != receipt.ReceiptID {
			t.Fatalf("duplicate=%+v handled=%v err=%v", duplicate, handled, err)
		}
		if _, _, err := starts.AcceptVerifiedWebhookAutomation(ctx, endpoint, "evt_generic_1", "order.updated", body, true); !errors.Is(err, ErrWebhookAutomationConflict) {
			t.Fatalf("event type reused with same ID: %v", err)
		}
		work, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if len(work.RecipientSnapshot) != 1 || work.RecipientSnapshot[0].WebhookProvider != InboundWebhookProviderGeneric || work.RecipientSnapshot[0].Source != receipt.EventSource {
			t.Fatalf("recipient snapshot=%+v", work.RecipientSnapshot)
		}
		runID, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		run, err := store.GetWorkflowRun(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		var input webhookAutomationEnvelope
		if err := json.Unmarshal(run.Input, &input); err != nil || input.ID != "evt_generic_1" || input.Type != "order.paid" || input.Source != receipt.EventSource || !equalWorkflowJSON(input.Data, body) {
			t.Fatalf("workflow input=%s err=%v", run.Input, err)
		}
	})
}

func TestWebhookAutomationMigrationRollbackAndReapply(t *testing.T) {
	store := NewPgStore(pgtest.OpenMigrated(t))
	_, endpoint, _ := seedWebhookAutomation(t, store)
	receipt, _, err := store.AcceptWebhookAutomation(t.Context(), endpoint, webhookTestBody("evt_rollback"), true)
	if err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../../migrations/20261003200000001_workflow_webhook_starts.sql")
	if err != nil {
		t.Fatal(err)
	}
	up, down, ok := strings.Cut(string(migration), "-- +goose Down")
	if !ok {
		t.Fatal("missing rollback")
	}
	if _, err := store.pool.Exec(t.Context(), down); err != nil {
		t.Fatal(err)
	}
	if _, err := store.pool.Exec(t.Context(), up); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetWebhookAutomationReceipt(t.Context(), endpoint.ID, receipt.ProviderEventID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("receipt remains=%v", err)
	}
	work, err := store.ClaimDuePublishedEvent(t.Context(), time.Now().UTC())
	if err != nil || len(work.RecipientSnapshot) != 1 {
		t.Fatalf("outbox was lost=%+v %v", work, err)
	}
	if _, err := store.AdmitEventWorkflow(t.Context(), work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID); err != nil {
		t.Fatal(err)
	}
}
func webhookTestBody(id string) json.RawMessage {
	return json.RawMessage(`{"id":"` + id + `","type":"invoice.paid","data":{"object":{"id":"in_1","amount_paid":150}}}`)
}
func TestWebhookAutomationPlanDowngradeIsRetryable(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, endpoint, _ := seedWebhookAutomation(t, store)
		ctx := t.Context()
		if err := store.UpdateAccountPlan(ctx, app.AccountID, api.PlanPro); err != nil {
			t.Fatal(err)
		}
		author := store.(AutomationStore)
		raw := json.RawMessage(`{"name":"paid","steps":[{"name":"main","path":"/paid","timeout":"20m"}]}`)
		draft := mutateForTest(t, author, app.ID, "paid", "save", 0, raw, false)
		mutateForTest(t, author, app.ID, "paid", "publish", draft.Version, nil, true)
		if err := store.UpdateAccountPlan(ctx, app.AccountID, api.PlanHobby); err != nil {
			t.Fatal(err)
		}
		starts := store.(WebhookAutomationStore)
		if _, _, err := starts.AcceptWebhookAutomation(ctx, endpoint, webhookTestBody("evt_downgrade"), true); !errors.Is(err, ErrWebhookAutomationUnavailable) {
			t.Fatalf("downgrade should permit provider retry: %v", err)
		}
		if _, err := starts.GetWebhookAutomationReceipt(ctx, endpoint.ID, "evt_downgrade"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ineligible target was accepted: %v", err)
		}
		if err := store.UpdateAccountPlan(ctx, app.AccountID, api.PlanPro); err != nil {
			t.Fatal(err)
		}
		receipt, handled, err := starts.AcceptWebhookAutomation(ctx, endpoint, webhookTestBody("evt_downgrade"), true)
		if err != nil || !handled || receipt.Status != "accepted" || receipt.Duplicate {
			t.Fatalf("retry after eligibility restored: %+v %v", receipt, err)
		}
	})
}
func TestWebhookAutomationDurableAdmissionAndProviderRetry(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, endpoint, binding := seedWebhookAutomation(t, store)
		ctx := context.Background()
		starts := store.(WebhookAutomationStore)
		body := webhookTestBody("evt_atomic")
		var accepted atomic.Int32
		var wg sync.WaitGroup
		for range 8 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				receipt, handled, err := starts.AcceptWebhookAutomation(ctx, endpoint, body, true)
				if err != nil || !handled || receipt.Status != "accepted" {
					t.Errorf("accept=%+v handled=%v err=%v", receipt, handled, err)
					return
				}
				if !receipt.Duplicate {
					accepted.Add(1)
				}
			}()
		}
		wg.Wait()
		if accepted.Load() != 1 {
			t.Fatalf("accepted=%d", accepted.Load())
		}
		if _, _, err := starts.AcceptWebhookAutomation(ctx, endpoint, json.RawMessage(`{"id":"evt_atomic","type":"invoice.paid","data":{"different":true}}`), true); !errors.Is(err, ErrWebhookAutomationConflict) {
			t.Fatalf("changed event=%v", err)
		}
		if err := starts.DeleteWebhookAutomationBinding(ctx, WebhookAutomationBindingOptions{EndpointID: endpoint.ID, AppID: app.ID, AccountID: app.AccountID, ExpectedVersion: binding.Version}); err != nil {
			t.Fatal(err)
		}
		retry, handled, err := starts.AcceptWebhookAutomation(ctx, endpoint, body, false)
		if err != nil || !handled || !retry.Duplicate {
			t.Fatalf("retry after revocation=%+v %v %v", retry, handled, err)
		}
		work, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		if len(work.RecipientSnapshot) != 1 || work.RecipientSnapshot[0].WebhookEndpointID != endpoint.ID {
			t.Fatalf("snapshot=%+v", work.RecipientSnapshot)
		}
		runID, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		again, err := store.(EventWorkflowStore).AdmitEventWorkflow(ctx, work.ID, work.ClaimToken, work.RecipientSnapshot[0].ID)
		if err != nil || again != runID {
			t.Fatalf("duplicate admission=%s %v", again, err)
		}
		run, err := store.GetWorkflowRun(ctx, runID)
		if err != nil {
			t.Fatal(err)
		}
		if !equalWorkflowJSON(run.DefinitionSnapshot, work.RecipientSnapshot[0].Workflow) {
			t.Fatal("definition changed")
		}
		var input struct {
			Source string          `json:"source"`
			Data   json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(run.Input, &input); err != nil || !equalWorkflowJSON(input.Data, body) || input.Source != webhookAutomationSource(endpoint.Provider, endpoint.ID) {
			t.Fatalf("input=%s err=%v", run.Input, err)
		}
		receipt, err := starts.GetWebhookAutomationReceipt(ctx, endpoint.ID, "evt_atomic")
		if err != nil || receipt.RunID != runID {
			t.Fatalf("receipt=%+v %v", receipt, err)
		}
		if err := store.(PublishedEventWorkStore).FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
			t.Fatal(err)
		}
		if next, err := store.(PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC()); err == nil {
			t.Fatalf("extra outbox=%+v", next)
		}
		_, handled, err = starts.AcceptWebhookAutomation(ctx, endpoint, webhookTestBody("evt_after_removal"), true)
		if err != nil || handled {
			t.Fatalf("future routing=%v %v", handled, err)
		}
		if _, err := store.(PublishedEventRetentionStore).PruneDeliveredPublishedEvents(ctx, time.Now().Add(time.Hour), 100); err != nil {
			t.Fatal(err)
		}
		if _, err := starts.GetWebhookAutomationReceipt(ctx, endpoint.ID, "evt_atomic"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("receipt retention=%v", err)
		}
	})
}
func TestWebhookAutomationEligibilityAndBindingRevision(t *testing.T) {
	workflowScheduleStores(t, func(t *testing.T, store Store) {
		app, endpoint, binding := seedWebhookAutomation(t, store)
		ctx := context.Background()
		starts := store.(WebhookAutomationStore)
		opts := WebhookAutomationBindingOptions{EndpointID: endpoint.ID, AppID: app.ID, AccountID: app.AccountID, WorkflowName: "paid", EventType: "invoice.paid"}
		if _, err := starts.SaveWebhookAutomationBinding(ctx, opts); !errors.Is(err, ErrWebhookAutomationConflict) {
			t.Fatalf("stale update=%v", err)
		}
		opts.ExpectedVersion = binding.Version
		opts.AccountID = uuid.NewString()
		if _, err := starts.SaveWebhookAutomationBinding(ctx, opts); !errors.Is(err, ErrNotFound) {
			t.Fatalf("ownership=%v", err)
		}
		opts.AccountID = app.AccountID
		opts.EventType = "invoice.*.paid"
		if _, err := starts.SaveWebhookAutomationBinding(ctx, opts); !errors.Is(err, ErrAutomationInvalid) {
			t.Fatalf("invalid type=%v", err)
		}
		if _, _, err := starts.AcceptWebhookAutomation(ctx, endpoint, webhookTestBody("evt_gate"), false); !errors.Is(err, ErrWebhookAutomationUnavailable) {
			t.Fatalf("runtime=%v", err)
		}
		rotated := endpoint
		rotated.SigningSecretSealed = []byte("old-secret")
		if _, _, err := starts.AcceptWebhookAutomation(ctx, rotated, webhookTestBody("evt_gate"), true); !errors.Is(err, ErrWebhookAutomationUnavailable) {
			t.Fatalf("rotation=%v", err)
		}
		typeMismatch := json.RawMessage(`{"id":"evt_filter","type":"customer.created"}`)
		receipt, handled, err := starts.AcceptWebhookAutomation(ctx, endpoint, typeMismatch, true)
		if err != nil || !handled || receipt.Status != "ignored" || receipt.IgnoredReason != "event_filtered" {
			t.Fatalf("type filter=%+v %v", receipt, err)
		}
		spec := api.WorkflowSpec{Name: "paid", Steps: []api.WorkflowStepSpec{{Name: "main", Path: "/new"}}}
		raw, _ := json.Marshal(spec)
		draft, err := store.(AutomationStore).MutateAutomation(ctx, app.ID, "paid", AutomationMutation{Action: "save", Draft: raw})
		if err != nil {
			t.Fatal(err)
		}
		published, err := store.(AutomationStore).MutateAutomation(ctx, app.ID, "paid", AutomationMutation{Action: "publish", ExpectedVersion: draft.Version, TakeOverManifest: true})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.(AutomationStore).MutateAutomation(ctx, app.ID, "paid", AutomationMutation{Action: "enable", ExpectedVersion: published.Version, Enabled: false}); err != nil {
			t.Fatal(err)
		}
		receipt, _, err = starts.AcceptWebhookAutomation(ctx, endpoint, webhookTestBody("evt_paused"), true)
		if err != nil || receipt.IgnoredReason != "automation_paused" {
			t.Fatalf("pause=%+v %v", receipt, err)
		}
		if _, err := starts.SaveWebhookAutomationBinding(ctx, WebhookAutomationBindingOptions{EndpointID: endpoint.ID, AppID: app.ID, AccountID: app.AccountID, WorkflowName: "missing", EventType: "*", ExpectedVersion: binding.Version}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("unpublished=%v", err)
		}
	})
}
