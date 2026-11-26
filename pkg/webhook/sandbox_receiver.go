package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storedworkflowchecks"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

// A reserved target requires a live run and its completed invocation.
// Partner runs also require an unexpired registration for this scenario.
func (d *Dispatcher) sandboxReceiver(ctx context.Context, hook state.AppWebhook, row state.AppWebhookDelivery) (*sandboxReceiver, error) {
	id, reserved := strings.CutPrefix(hook.TargetURL, api.APISandboxWebhookReceiverPrefix)
	if !reserved {
		return nil, nil
	}
	parsed, err := uuid.Parse(id)
	runs, haveRuns := d.store.(state.APISandboxRunStore)
	operations, haveOperations := d.store.(state.APISandboxOperationStore)
	if err != nil || parsed == uuid.Nil || parsed.String() != id || !haveRuns || !haveOperations ||
		hook.Scope != state.AppWebhookScopeApp || hook.AppID != row.AppID || hook.AccountID != row.AccountID ||
		hook.DeliveryFormat != state.AppWebhookDeliveryFormatJSON || row.Event != state.AppWebhookEventJobFinished {
		return nil, state.ErrNotFound
	}
	now := d.Now()
	run, err := runs.GetAPISandboxRun(ctx, id)
	if err != nil {
		return nil, err
	}
	if run.AppID != hook.AppID || run.Mode != api.APISandboxRunModeAutomated || run.Status != api.APISandboxRunRunning ||
		run.LeaseUntil == nil || !run.LeaseUntil.After(now) {
		return nil, state.ErrNotFound
	}
	app, err := d.store.AppByID(ctx, hook.AppID)
	if err != nil {
		return nil, err
	}
	if app.AccountID != hook.AccountID || !app.Manifest.APISandboxRun || !state.APISandboxAcceptsRequests(app, now) {
		return nil, state.ErrNotFound
	}
	inv, err := d.store.InvocationByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if inv.AppID != app.ID || inv.AccountID != app.AccountID || inv.Source != state.InvocationAsyncInvoke ||
		inv.State != state.InvocationCompleted || inv.OnSuccessDestinationID != hook.ID {
		return nil, state.ErrNotFound
	}
	var payload struct {
		JobID string `json:"job_id"`
	}
	if json.Unmarshal(row.Payload, &payload) != nil || payload.JobID != inv.ID {
		return nil, state.ErrNotFound
	}
	op, err := operations.GetAPISandboxOperation(ctx, app.ID)
	if err != nil {
		return nil, err
	}
	if op.Phase != state.APISandboxLive || op.Scenario == nil || op.Scenario.Revision != run.ScenarioRevision || op.Scenario.OutboundWebhook == nil {
		return nil, state.ErrNotFound
	}
	if run.PartnerWebhook {
		partners, ok := d.store.(state.APISandboxWebhookPartnerStore)
		if !ok || row.Attempt > 1 || run.PartnerOutcomeUntil != nil {
			return nil, state.ErrNotFound
		}
		partner, err := partners.GetAPISandboxWebhookPartner(ctx, run.ID)
		if err != nil {
			return nil, err
		}
		if !partner.ReadyFor(run.SourceDeploymentID, run.ScenarioRevision, now) || !bytes.Equal(partner.SigningSecretSealed, hook.SecretSealed) {
			return nil, state.ErrNotFound
		}
		return &sandboxReceiver{partnerTarget: partner.TargetURL}, nil
	}
	return &sandboxReceiver{
		target: hook.TargetURL, delivery: row, retryOnce: op.Scenario.OutboundWebhook.RetryOnce,
	}, nil
}

type sandboxReceiver struct {
	partnerTarget string
	target        string
	delivery      state.AppWebhookDelivery
	retryOnce     bool
	signer        *webhookout.Signer
}

// RoundTrip receives the request serialized and signed by the ordinary
// dispatcher. Only the HTTP transport is replaced for the sandbox target.
func (r *sandboxReceiver) RoundTrip(req *http.Request) (*http.Response, error) {
	defer req.Body.Close()
	status := r.responseStatus(req)
	return &http.Response{
		StatusCode: status, Header: make(http.Header), Body: http.NoBody, Request: req,
	}, nil
}

func (r *sandboxReceiver) responseStatus(req *http.Request) int {
	if req.Method != http.MethodPost || req.URL.String() != r.target {
		return http.StatusNotFound
	}
	const maxBodyBytes = 1 << 20
	body, err := io.ReadAll(io.LimitReader(req.Body, maxBodyBytes+1))
	if err != nil || len(body) > maxBodyBytes {
		return http.StatusUnprocessableEntity
	}
	ts, err := strconv.ParseInt(req.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
	sig, signed := strings.CutPrefix(req.Header.Get("X-Faas-Webhook-Signature"), "sha256=")
	id := req.Header.Get("X-Faas-Delivery-Id")
	if err != nil || ts <= 0 || !signed || id != r.delivery.ID || r.signer.Verify(ts, id, body, sig) != nil {
		return http.StatusUnauthorized
	}
	var envelope struct {
		ID       string `json:"id"`
		RuleName string `json:"rule_name"`
		Payload  struct {
			DeliveryID string          `json:"delivery_id"`
			Event      string          `json:"event"`
			Attempt    *int            `json:"attempt"`
			Data       json.RawMessage `json:"data"`
		} `json:"payload"`
	}
	attempt, err := strconv.Atoi(req.Header.Get("X-Faas-Webhook-Attempt"))
	if err != nil || attempt != r.delivery.Attempt+1 || json.Unmarshal(body, &envelope) != nil ||
		envelope.ID != id || envelope.RuleName != string(r.delivery.Event) ||
		envelope.Payload.DeliveryID != id || envelope.Payload.Event != string(r.delivery.Event) ||
		envelope.Payload.Attempt == nil || *envelope.Payload.Attempt != r.delivery.Attempt ||
		!storedworkflowchecks.EqualJSON(envelope.Payload.Data, r.delivery.Payload) {
		return http.StatusUnprocessableEntity
	}
	if r.retryOnce && attempt == 1 {
		return http.StatusServiceUnavailable
	}
	return http.StatusNoContent
}
