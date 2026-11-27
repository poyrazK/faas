package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apisandbox"
	"github.com/onebox-faas/faas/pkg/billing/stripe/webhooksig"
	"github.com/onebox-faas/faas/pkg/state"
)

const sandboxWebhookProcessingTimeout = 40 * time.Second
const sandboxWebhookStateTimeout = 20 * time.Second

func (s *server) runAPISandboxStripeWebhook(ctx context.Context, store state.APISandboxRunStore, run *state.APISandboxRun, app state.App, scenario *api.APISandboxScenario, sandboxURL, readKey string) error {
	webhook := scenario.StripeWebhook
	// Use an endpoint identity private to the run. The signed fixture goes
	// through the same verifier and inbox as public Stripe deliveries.
	endpoint := state.InboundWebhookEndpoint{
		ID: run.ID, AppID: app.ID, AccountID: app.AccountID, Enabled: true,
		Provider: state.InboundWebhookProviderStripe, DeliveryPath: webhook.DeliveryPath,
	}
	secret, _, err := generateInboundWebhookToken()
	var body []byte
	if err == nil {
		sealed, problem := sealInboundWebhookSigningSecret(secret)
		if problem == nil {
			endpoint.SigningSecretSealed = sealed
			body, err = json.Marshal(map[string]any{
				"id":     "evt_sandbox_" + strings.ReplaceAll(run.ID, "-", ""),
				"object": "event", "type": webhook.EventType, "livemode": false, "created": time.Now().Unix(),
				"data": map[string]json.RawMessage{"object": webhook.DataObject},
			})
			if err != nil {
				body = nil
			}
		}
	}

	first := sandboxWebhookStep(1, "Send Stripe test event", api.APISandboxStepStripeWebhook, webhook)
	if len(body) == 0 {
		first.FailureCode = "webhook_fixture_unavailable"
	} else {
		s.sendSandboxWebhookFixture(ctx, endpoint, run.SandboxDeploymentID, secret, body, "", &first)
	}
	if first.InvocationID != "" {
		// A failed check can leave a retry pending. The sandbox lease
		// also fences subsequent dispatch as its app closes.
		defer func() {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			defer cancel()
			_, _ = s.store.CancelPendingInvocation(cleanup, first.InvocationID)
		}()
	}
	if err := checkpointAPISandboxWebhookStep(ctx, store, run, first); err != nil || first.FailureCode != "" {
		return err
	}

	replay := sandboxWebhookStep(2, "Resend the same event", api.APISandboxStepWebhookReplay, webhook)
	s.sendSandboxWebhookFixture(ctx, endpoint, run.SandboxDeploymentID, secret, body, first.InvocationID, &replay)
	if err := checkpointAPISandboxWebhookStep(ctx, store, run, replay); err != nil || replay.FailureCode != "" {
		return err
	}

	processing := sandboxWebhookStep(3, "Wait for webhook processing", api.APISandboxStepWebhookProcess, webhook)
	processing.InvocationID = first.InvocationID
	s.waitForSandboxWebhook(ctx, app, &processing)
	if err := checkpointAPISandboxWebhookStep(ctx, store, run, processing); err != nil || processing.FailureCode != "" {
		return err
	}

	step := apisandbox.WaitForState(ctx, s.apiSandboxProbeClient, sandboxURL, run.SandboxDeploymentID, readKey, 4, scenario.Requests[0], sandboxWebhookStateTimeout)
	if err := checkpointAPISandboxWebhookStep(ctx, store, run, step); err != nil || step.FailureCode != "" {
		return err
	}
	run.Status = api.APISandboxRunPassed
	return nil
}

func sandboxWebhookStep(index int, title, kind string, webhook *api.APISandboxStripeWebhook) api.APISandboxRunStep {
	return api.APISandboxRunStep{
		Index: index, Title: title, Kind: kind, EventType: webhook.EventType,
		Method: http.MethodPost, Path: webhook.DeliveryPath,
	}
}

func (s *server) sendSandboxWebhookFixture(ctx context.Context, endpoint state.InboundWebhookEndpoint, deploymentID, secret string, body []byte, originalReceipt string, step *api.APISandboxRunStep) {
	start := time.Now()
	defer func() { step.DurationMS = time.Since(start).Milliseconds() }()
	step.ExpectedStatus = http.StatusAccepted
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", bytes.NewReader(body))
	if err != nil {
		step.FailureCode = "webhook_fixture_unavailable"
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Stripe-Signature", webhooksig.Header(body, secret, start))
	rec := httptest.NewRecorder()
	s.receiveInboundWebhookDelivery(rec, req, endpoint, deploymentID)
	step.HTTPStatus = rec.Code
	if rec.Code != http.StatusAccepted {
		step.FailureCode = "webhook_ingress_rejected"
		return
	}
	var receipt api.InboundWebhookReceiptResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &receipt); err != nil || receipt.Status != "accepted" {
		step.FailureCode = "webhook_receipt_unavailable"
		return
	}
	if id, err := uuid.Parse(receipt.ReceiptID); err != nil || id == uuid.Nil || id.String() != receipt.ReceiptID {
		step.FailureCode = "webhook_receipt_unavailable"
		return
	}
	step.InvocationID = receipt.ReceiptID
	if receipt.Duplicate != (originalReceipt != "") || (originalReceipt != "" && originalReceipt != receipt.ReceiptID) {
		step.FailureCode = "webhook_duplicate_mismatch"
	}
}

func (s *server) waitForSandboxWebhook(ctx context.Context, app state.App, step *api.APISandboxRunStep) {
	start := time.Now()
	defer func() { step.DurationMS = time.Since(start).Milliseconds() }()
	waitCtx, cancel := context.WithTimeout(ctx, sandboxWebhookProcessingTimeout)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		inv, err := s.store.InvocationByID(waitCtx, step.InvocationID)
		if err != nil || inv.AppID != app.ID || inv.AccountID != app.AccountID || inv.Source != state.InvocationInboundWebhook {
			step.FailureCode = "webhook_processing_unavailable"
			if waitCtx.Err() != nil {
				step.FailureCode = "webhook_processing_timeout"
			}
			return
		}
		step.InvocationState, step.DeliveryAttempts = string(inv.State), inv.Attempts
		switch inv.State {
		case state.InvocationCompleted:
			return
		case state.InvocationPending, state.InvocationDispatching:
		default:
			step.FailureCode = "webhook_processing_failed"
			return
		}
		select {
		case <-waitCtx.Done():
			step.FailureCode = "webhook_processing_timeout"
			return
		case <-ticker.C:
		}
	}
}

func checkpointAPISandboxWebhookStep(ctx context.Context, store state.APISandboxRunStore, run *state.APISandboxRun, step api.APISandboxRunStep) error {
	if err := store.AppendAPISandboxRunStep(ctx, *run, step, time.Now().UTC()); err != nil {
		return err
	}
	if step.FailureCode != "" {
		run.Status, run.FailureCode = api.APISandboxRunFailed, step.FailureCode
	}
	return nil
}
