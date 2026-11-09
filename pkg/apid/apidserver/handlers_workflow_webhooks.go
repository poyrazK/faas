package apidserver

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) webhookWorkflowStore(w http.ResponseWriter) (state.WebhookWorkflowStore, bool) {
	store, ok := s.store.(state.WebhookWorkflowStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("webhook workflow storage unavailable"))
	}
	return store, ok
}
func (s *server) putWebhookWorkflowBinding(w http.ResponseWriter, r *http.Request, account state.Account) {
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, account)
	if !ok {
		return
	}
	var body api.PutWebhookWorkflowBindingRequest
	if err := decodeJSONSized(r, &body, api.WorkflowWebhookBindingMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.WorkflowWebhookBindingMaxBytes, api.WorkflowWebhookBindingMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid webhook workflow binding"))
		}
		return
	}
	if body.ExpectedVersion == nil || *body.ExpectedVersion < 0 || !body.TakeOverDelivery {
		api.WriteProblem(w, api.ErrValidation("expected_version is required and take_over_delivery must be true"))
		return
	}
	if !account.Plan.WorkflowsAllowed() {
		api.WriteProblem(w, api.ErrPlanWorkflowsNotAllowed(account.Plan))
		return
	}
	store, ok := s.webhookWorkflowStore(w)
	if !ok {
		return
	}
	binding, err := store.SaveWebhookWorkflowBinding(r.Context(), state.WebhookWorkflowBindingOptions{EndpointID: endpoint.ID, AppID: endpoint.AppID, AccountID: account.ID, ExpectedVersion: *body.ExpectedVersion, WorkflowName: body.WorkflowName, EventType: body.EventType, Filter: body.Filter})
	if err != nil {
		writeWebhookWorkflowError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "app.webhook_workflow_bound", &account.ID, map[string]any{"endpoint_id": endpoint.ID, "workflow_name": binding.WorkflowName, "version": binding.Version})
	writeJSON(w, http.StatusOK, webhookWorkflowBindingResponse(binding))
}
func webhookWorkflowBindingResponse(binding state.WebhookWorkflowBinding) api.WebhookWorkflowBindingResponse {
	return api.WebhookWorkflowBindingResponse{EndpointID: binding.EndpointID, WorkflowName: binding.WorkflowName, EventType: binding.EventType, Filter: binding.Filter, Version: binding.Version, UpdatedAt: binding.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}
func (s *server) getWebhookWorkflowBinding(w http.ResponseWriter, r *http.Request, account state.Account) {
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, account)
	if !ok {
		return
	}
	store, ok := s.webhookWorkflowStore(w)
	if !ok {
		return
	}
	binding, err := store.GetWebhookWorkflowBinding(r.Context(), endpoint.ID)
	if err != nil {
		writeWebhookWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, webhookWorkflowBindingResponse(binding))
}
func (s *server) deleteWebhookWorkflowBinding(w http.ResponseWriter, r *http.Request, account state.Account) {
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, account)
	if !ok {
		return
	}
	version, err := strconv.ParseInt(r.URL.Query().Get("expected_version"), 10, 64)
	if err != nil || version <= 0 {
		api.WriteProblem(w, api.ErrValidation("expected_version must be a positive integer"))
		return
	}
	store, ok := s.webhookWorkflowStore(w)
	if !ok {
		return
	}
	if err := store.DeleteWebhookWorkflowBinding(r.Context(), state.WebhookWorkflowBindingOptions{EndpointID: endpoint.ID, AppID: endpoint.AppID, AccountID: account.ID, ExpectedVersion: version}); err != nil {
		writeWebhookWorkflowError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "app.webhook_workflow_unbound", &account.ID, map[string]any{"endpoint_id": endpoint.ID})
	w.WriteHeader(http.StatusNoContent)
}
func webhookWorkflowReceiptResponse(receipt state.WebhookWorkflowReceipt) api.WebhookWorkflowReceiptResponse {
	return api.WebhookWorkflowReceiptResponse{ReceiptID: receipt.ReceiptID, EndpointID: receipt.EndpointID, ProviderEventID: receipt.ProviderEventID, WorkflowName: receipt.WorkflowName, Status: receipt.Status, IgnoredReason: receipt.IgnoredReason, Duplicate: receipt.Duplicate, AcceptedAt: receipt.AcceptedAt.UTC().Format(time.RFC3339Nano), EventSource: receipt.EventSource, RoutingStatus: receipt.RoutingStatus, RunID: receipt.RunID}
}
func (s *server) getWebhookWorkflowReceipt(w http.ResponseWriter, r *http.Request, account state.Account) {
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, account)
	if !ok {
		return
	}
	store, ok := s.webhookWorkflowStore(w)
	if !ok {
		return
	}
	receipt, err := store.GetWebhookWorkflowReceipt(r.Context(), endpoint.ID, r.PathValue("event_id"))
	if err != nil {
		writeWebhookWorkflowError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, webhookWorkflowReceiptResponse(receipt))
}
func (s *server) receiveWebhookWorkflow(w http.ResponseWriter, r *http.Request, endpoint state.InboundWebhookEndpoint, eventID, eventType string, body []byte) bool {
	store, ok := s.store.(state.WebhookWorkflowStore)
	if !ok {
		return false
	}
	receipt, handled, err := store.AcceptVerifiedWebhookWorkflow(r.Context(), endpoint, eventID, eventType, body, s.workflowRuntimeEnabled)
	if !handled {
		return false
	}
	if err != nil {
		writeWebhookWorkflowError(w, err)
		return true
	}
	_ = s.notif.Notify(r.Context(), db.NotifyEventPublished, "1")
	writeJSON(w, http.StatusAccepted, webhookWorkflowReceiptResponse(receipt))
	return true
}
func writeWebhookWorkflowError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "webhook endpoint, published stored workflow, binding or receipt not found"))
	case errors.Is(err, state.ErrWebhookWorkflowConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeWebhookWorkflowConflict, "Webhook workflow conflict", "binding revision, endpoint routing mode or provider event content changed"))
	case errors.Is(err, state.ErrWorkflowEventInputTooLarge):
		api.WriteProblem(w, api.ErrWorkflowEventEnvelopeTooLarge())
	case errors.Is(err, state.ErrStoredWorkflowInvalid), errors.Is(err, state.ErrWorkflowEventDefinitionInvalid):
		api.WriteProblem(w, api.ErrValidation("invalid stored workflow definition, event type, filter or workflow input size"))
	case errors.Is(err, state.ErrWebhookWorkflowUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeWebhookWorkflowUnavailable, "Webhook workflow unavailable", "workflow runtime, account, app or deployment is unavailable"))
	default:
		api.WriteProblem(w, api.ErrCapacity("webhook workflow operation failed"))
	}
}
