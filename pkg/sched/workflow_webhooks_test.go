// adr: 439
package sched

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type webhookCheckpointFailure struct {
	state.Store
	state.EventWorkflowStore
	state.PublishedEventRecipientProgressStore
	fail bool
}

func (s *webhookCheckpointFailure) RecordPublishedEventRecipientProgress(ctx context.Context, id int64, token, recipientID string, p state.PublishedEventRecipientProgress) error {
	if s.fail && p.State == state.PublishedEventRecipientEnqueued {
		s.fail = false
		return errors.New("interrupted after admission")
	}
	return s.PublishedEventRecipientProgressStore.RecordPublishedEventRecipientProgress(ctx, id, token, recipientID, p)
}
func TestWebhookAutomationFanoutFiltersAndCheckpointRecovery(t *testing.T) {
	guardOrchestrationStores(t, func(t *testing.T, store state.Store) {
		ctx := t.Context()
		account, err := store.CreateAccount(ctx, uuid.NewString()+"@example.com", api.PlanHobby)
		if err != nil {
			t.Fatal(err)
		}
		app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "webhook-" + uuid.NewString(), Type: state.AppTypeApp, RAMMB: 256})
		if err != nil {
			t.Fatal(err)
		}
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployPending, ImageDigest: "sha256:webhook", Workflows: json.RawMessage(`[{"name":"paid","steps":[{"name":"main","path":"/paid","input":{"invoice":"{{input.data.data.object.id}}"}}]}]`)})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
			t.Fatal(err)
		}
		endpoint, err := store.(state.InboundWebhookStore).CreateInboundWebhookEndpointIfUnderQuota(ctx, state.InboundWebhookEndpoint{AppID: app.ID, AccountID: account.ID, Name: "stripe", Provider: state.InboundWebhookProviderStripe, TokenHash: bytes.Repeat([]byte{3}, 32), SigningSecretSealed: []byte("sealed"), DeliveryPath: "/legacy", Enabled: true}, api.MustLimitsFor(account.Plan))
		if err != nil {
			t.Fatal(err)
		}
		starts := store.(state.WebhookAutomationStore)
		_, err = starts.SaveWebhookAutomationBinding(ctx, state.WebhookAutomationBindingOptions{EndpointID: endpoint.ID, AppID: app.ID, AccountID: account.ID, WorkflowName: "paid", EventType: "invoice.paid", Filter: json.RawMessage(`{"data":{"data":{"object":{"amount_paid":{"$gt":100}}}}}`)})
		if err != nil {
			t.Fatal(err)
		}
		wrapper := &webhookCheckpointFailure{Store: store, EventWorkflowStore: store.(state.EventWorkflowStore), PublishedEventRecipientProgressStore: store.(state.PublishedEventRecipientProgressStore), fail: true}
		loop := &Loop{engine: &Engine{store: wrapper}, workflowsDispatched: true}
		for _, sample := range []struct {
			id       string
			body     json.RawMessage
			filtered bool
		}{
			{"evt_small", json.RawMessage(`{"id":"evt_small","type":"invoice.paid","data":{"object":{"id":"in_small","amount_paid":50}}}`), true},
			{"evt_paid", json.RawMessage(`{"id":"evt_paid","type":"invoice.paid","data":{"object":{"id":"in_paid","amount_paid":150}}}`), false},
		} {
			receipt, handled, err := starts.AcceptWebhookAutomation(ctx, endpoint, sample.body, true)
			if err != nil || !handled {
				t.Fatal(receipt, err)
			}
			work, err := store.(state.PublishedEventWorkStore).ClaimDuePublishedEvent(ctx, time.Now().UTC())
			if err != nil {
				t.Fatal(err)
			}
			err = loop.routePublishedEventSnapshot(ctx, work)
			if !sample.filtered && err == nil {
				t.Fatal("missing checkpoint interruption")
			}
			if err := loop.routePublishedEventSnapshot(ctx, work); err != nil {
				t.Fatal(err)
			}
			if err := store.(state.PublishedEventWorkStore).FinishPublishedEvent(ctx, work.ID, work.ClaimToken, nil); err != nil {
				t.Fatal(err)
			}
			receipt, err = starts.GetWebhookAutomationReceipt(ctx, endpoint.ID, sample.id)
			if err != nil {
				t.Fatal(err)
			}
			if sample.filtered {
				if receipt.RoutingStatus != "filtered" || receipt.RunID != "" {
					t.Fatalf("filter=%+v", receipt)
				}
			} else if receipt.RoutingStatus != "enqueued" || receipt.RunID == "" {
				t.Fatalf("admission=%+v", receipt)
			}
		}
		runs, total, err := store.ListWorkflowRuns(ctx, app.ID, state.ListWorkflowRunsOpts{Limit: 10})
		if err != nil || total != 1 {
			t.Fatalf("runs=%d %v", total, err)
		}
		executor := &scheduleStepExecutor{}
		orchestrator := NewWorkflowOrchestrator(store, executor, nil, nil, quietLog())
		if err := orchestrator.DispatchTick(ctx); err != nil {
			t.Fatal(err)
		}
		run, err := store.GetWorkflowRun(ctx, runs[0].ID)
		var input map[string]string
		inputErr := json.Unmarshal(executor.input, &input)
		if err != nil || run.Status != state.WorkflowRunStatusSucceeded || executor.calls != 1 || inputErr != nil || len(input) != 1 || input["invoice"] != "in_paid" {
			t.Fatalf("run=%+v calls=%d input=%s err=%v", run, executor.calls, executor.input, err)
		}
	})
}
