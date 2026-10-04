package main

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) webhookAutomationStore(w http.ResponseWriter) (state.WebhookAutomationStore, bool) {
	store, ok := s.store.(state.WebhookAutomationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("webhook automation storage unavailable"))
	}
	return store, ok
}
func (s *server) putWebhookAutomationBinding(w http.ResponseWriter, r *http.Request, account state.Account) {
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, account)
	if !ok {
		return
	}
	var body api.PutWebhookAutomationBindingRequest
	if err := decodeJSONSized(r, &body, api.WorkflowWebhookBindingMaxBytes); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			api.WriteProblem(w, api.ErrRequestBodyTooLarge(api.WorkflowWebhookBindingMaxBytes, api.WorkflowWebhookBindingMaxBytes+1))
		} else {
			api.WriteProblem(w, api.ErrValidation("invalid webhook automation binding"))
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
	store, ok := s.webhookAutomationStore(w)
	if !ok {
		return
	}
	binding, err := store.SaveWebhookAutomationBinding(r.Context(), state.WebhookAutomationBindingOptions{EndpointID: endpoint.ID, AppID: endpoint.AppID, AccountID: account.ID, ExpectedVersion: *body.ExpectedVersion, WorkflowName: body.WorkflowName, EventType: body.EventType, Filter: body.Filter})
	if err != nil {
		writeWebhookAutomationError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "app.webhook_automation_bound", &account.ID, map[string]any{"endpoint_id": endpoint.ID, "workflow_name": binding.WorkflowName, "version": binding.Version})
	writeJSON(w, http.StatusOK, webhookAutomationBindingResponse(binding))
}
func webhookAutomationBindingResponse(binding state.WebhookAutomationBinding) api.WebhookAutomationBindingResponse {
	return api.WebhookAutomationBindingResponse{EndpointID: binding.EndpointID, WorkflowName: binding.WorkflowName, EventType: binding.EventType, Filter: binding.Filter, Version: binding.Version, UpdatedAt: binding.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}
func (s *server) getWebhookAutomationBinding(w http.ResponseWriter, r *http.Request, account state.Account) {
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, account)
	if !ok {
		return
	}
	store, ok := s.webhookAutomationStore(w)
	if !ok {
		return
	}
	binding, err := store.GetWebhookAutomationBinding(r.Context(), endpoint.ID)
	if err != nil {
		writeWebhookAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, webhookAutomationBindingResponse(binding))
}
func (s *server) deleteWebhookAutomationBinding(w http.ResponseWriter, r *http.Request, account state.Account) {
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, account)
	if !ok {
		return
	}
	version, err := strconv.ParseInt(r.URL.Query().Get("expected_version"), 10, 64)
	if err != nil || version <= 0 {
		api.WriteProblem(w, api.ErrValidation("expected_version must be a positive integer"))
		return
	}
	store, ok := s.webhookAutomationStore(w)
	if !ok {
		return
	}
	if err := store.DeleteWebhookAutomationBinding(r.Context(), state.WebhookAutomationBindingOptions{EndpointID: endpoint.ID, AppID: endpoint.AppID, AccountID: account.ID, ExpectedVersion: version}); err != nil {
		writeWebhookAutomationError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "app.webhook_automation_unbound", &account.ID, map[string]any{"endpoint_id": endpoint.ID})
	w.WriteHeader(http.StatusNoContent)
}
func webhookAutomationReceiptResponse(receipt state.WebhookAutomationReceipt) api.WebhookAutomationReceiptResponse {
	return api.WebhookAutomationReceiptResponse{ReceiptID: receipt.ReceiptID, EndpointID: receipt.EndpointID, ProviderEventID: receipt.ProviderEventID, WorkflowName: receipt.WorkflowName, Status: receipt.Status, IgnoredReason: receipt.IgnoredReason, Duplicate: receipt.Duplicate, AcceptedAt: receipt.AcceptedAt.UTC().Format(time.RFC3339Nano), EventSource: receipt.EventSource, RoutingStatus: receipt.RoutingStatus, RunID: receipt.RunID}
}
func (s *server) getWebhookAutomationReceipt(w http.ResponseWriter, r *http.Request, account state.Account) {
	endpoint, ok := s.loadInboundWebhookEndpoint(w, r, account)
	if !ok {
		return
	}
	store, ok := s.webhookAutomationStore(w)
	if !ok {
		return
	}
	receipt, err := store.GetWebhookAutomationReceipt(r.Context(), endpoint.ID, r.PathValue("event_id"))
	if err != nil {
		writeWebhookAutomationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, webhookAutomationReceiptResponse(receipt))
}
func (s *server) receiveWebhookAutomation(w http.ResponseWriter, r *http.Request, endpoint state.InboundWebhookEndpoint, body []byte) bool {
	store, ok := s.store.(state.WebhookAutomationStore)
	if !ok {
		return false
	}
	receipt, handled, err := store.AcceptWebhookAutomation(r.Context(), endpoint, body, s.workflowRuntimeEnabled)
	if !handled {
		return false
	}
	if err != nil {
		writeWebhookAutomationError(w, err)
		return true
	}
	_ = s.notif.Notify(r.Context(), db.NotifyEventPublished, "1")
	writeJSON(w, http.StatusAccepted, webhookAutomationReceiptResponse(receipt))
	return true
}
func writeWebhookAutomationError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "webhook endpoint, published automation, binding or receipt not found"))
	case errors.Is(err, state.ErrWebhookAutomationConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeWebhookAutomationConflict, "Webhook automation conflict", "binding revision, endpoint routing mode or provider event content changed"))
	case errors.Is(err, state.ErrAutomationInvalid), errors.Is(err, state.ErrWorkflowEventDefinitionInvalid):
		api.WriteProblem(w, api.ErrValidation("invalid automation definition, event type, filter or workflow input size"))
	case errors.Is(err, state.ErrWebhookAutomationUnavailable):
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeWebhookAutomationUnavailable, "Webhook automation unavailable", "workflow runtime, account, app or deployment is unavailable"))
	default:
		api.WriteProblem(w, api.ErrCapacity("webhook automation operation failed"))
	}
}
