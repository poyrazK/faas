package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func workflowCallbackWebhookBindingStore(store state.Store) (state.WorkflowCallbackWebhookBindingStore, bool) {
	bindings, ok := store.(state.WorkflowCallbackWebhookBindingStore)
	return bindings, ok
}

func (s *server) workflowCallbackForAccount(w http.ResponseWriter, r *http.Request, acct state.Account) (*state.WorkflowRun, *api.WorkflowStepSpec, bool) {
	run, ok := s.workflowRunForAccount(w, r, acct)
	if !ok {
		return nil, nil, false
	}
	callbacks, err := workflowCallbackSpecs(run)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("workflow definition snapshot is invalid"))
		return nil, nil, false
	}
	for i := range callbacks {
		if api.WorkflowCallbackID(run.ID, callbacks[i].Name) == r.PathValue("callback_id") {
			return run, &callbacks[i], true
		}
	}
	api.WriteProblem(w, api.ErrWorkflowStepNotFound())
	return nil, nil, false
}

func workflowCallbackWebhookBindingResponse(binding state.WorkflowCallbackWebhookBinding) api.WorkflowCallbackWebhookBindingResponse {
	return api.WorkflowCallbackWebhookBindingResponse{
		ID: binding.ID, RunID: binding.RunID, StepName: binding.StepName,
		EndpointID: binding.EndpointID, EventType: binding.EventType,
		ObjectID: binding.ObjectID, CreatedAt: binding.CreatedAt.UTC().Format(time.RFC3339),
	}
}

// putWorkflowCallbackWebhookBinding binds an existing verified Stripe ingress
// endpoint to an exact provider object event. PUT is idempotent for retries.
func (s *server) putWorkflowCallbackWebhookBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, step, ok := s.workflowCallbackForAccount(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateWorkflowCallbackWebhookBindingRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid webhook binding JSON"))
		return
	}
	if _, err := uuid.Parse(req.EndpointID); err != nil {
		api.WriteProblem(w, api.ErrValidation("endpoint_id must be a UUID"))
		return
	}
	if !api.ValidStripeWorkflowCallbackMatch(req.EventType, req.ObjectID) {
		api.WriteProblem(w, api.ErrValidation("event_type or object_id is invalid"))
		return
	}
	endpointStore, ok := inboundWebhookStore(s.store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("inbound webhook store unavailable"))
		return
	}
	endpoint, err := endpointStore.InboundWebhookEndpointByID(r.Context(), req.EndpointID)
	if err != nil || endpoint.AppID != run.AppID || endpoint.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		return
	}
	if endpoint.Provider != state.InboundWebhookProviderStripe {
		api.WriteProblem(w, api.ErrValidation("webhook binding requires a Stripe endpoint"))
		return
	}
	bindings, ok := workflowCallbackWebhookBindingStore(s.store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow webhook binding store unavailable"))
		return
	}
	binding, err := bindings.CreateWorkflowCallbackWebhookBinding(r.Context(), state.WorkflowCallbackWebhookBinding{
		ID: api.WorkflowCallbackID(run.ID, step.Name), RunID: run.ID,
		StepName: step.Name, EndpointID: endpoint.ID,
		EventType: req.EventType, ObjectID: req.ObjectID,
	})
	if err != nil {
		switch {
		case errors.Is(err, state.ErrWorkflowCallbackClosed):
			api.WriteProblem(w, api.ErrWorkflowCallbackClosed())
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.ErrWorkflowCallbackBindingConflict())
		case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrWorkflowRunNotFound):
			api.WriteProblem(w, api.ErrWorkflowRunNotFound())
		default:
			s.log.Error("create workflow callback webhook binding failed", "run_id", run.ID, "err", err)
			api.WriteProblem(w, api.ErrCapacity("could not create webhook binding"))
		}
		return
	}
	s.audit.Emit(r.Context(), "app.workflow.callback_webhook_bound", &acct.ID, map[string]any{
		"run_id": run.ID, "step_name": step.Name, "endpoint_id": endpoint.ID,
		"event_type": req.EventType, "object_id": req.ObjectID,
	})
	writeJSON(w, http.StatusOK, workflowCallbackWebhookBindingResponse(binding))
}

func (s *server) getWorkflowCallbackWebhookBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, _, ok := s.workflowCallbackForAccount(w, r, acct)
	if !ok {
		return
	}
	bindings, ok := workflowCallbackWebhookBindingStore(s.store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow webhook binding store unavailable"))
		return
	}
	binding, err := bindings.WorkflowCallbackWebhookBindingByID(r.Context(), r.PathValue("callback_id"))
	if errors.Is(err, state.ErrNotFound) || (err == nil && binding.RunID != run.ID) {
		api.WriteProblem(w, api.ErrWorkflowStepNotFound())
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read webhook binding"))
		return
	}
	writeJSON(w, http.StatusOK, workflowCallbackWebhookBindingResponse(binding))
}

func (s *server) deleteWorkflowCallbackWebhookBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	run, _, ok := s.workflowCallbackForAccount(w, r, acct)
	if !ok {
		return
	}
	bindings, ok := workflowCallbackWebhookBindingStore(s.store)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("workflow webhook binding store unavailable"))
		return
	}
	binding, err := bindings.WorkflowCallbackWebhookBindingByID(r.Context(), r.PathValue("callback_id"))
	if errors.Is(err, state.ErrNotFound) || (err == nil && binding.RunID != run.ID) {
		api.WriteProblem(w, api.ErrWorkflowStepNotFound())
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read webhook binding"))
		return
	}
	if err := bindings.DeleteWorkflowCallbackWebhookBinding(r.Context(), binding.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete webhook binding"))
		return
	}
	s.audit.Emit(r.Context(), "app.workflow.callback_webhook_unbound", &acct.ID, map[string]any{
		"run_id": run.ID, "step_name": binding.StepName, "endpoint_id": binding.EndpointID,
	})
	w.WriteHeader(http.StatusNoContent)
}

type stripeWorkflowCallbackMatch struct {
	Type string `json:"type"`
	Data struct {
		Object struct {
			ID string `json:"id"`
		} `json:"object"`
	} `json:"data"`
}

// receiveBoundWorkflowCallback returns true once the signed event has been
// handled (including a store failure). Unmatched events retain ADR-212's
// ordinary invocation path.
func (s *server) receiveBoundWorkflowCallback(w http.ResponseWriter, r *http.Request, endpoint state.InboundWebhookEndpoint, body []byte) bool {
	bindings, ok := workflowCallbackWebhookBindingStore(s.store)
	if !ok {
		return false
	}
	var event stripeWorkflowCallbackMatch
	if json.Unmarshal(body, &event) != nil || event.Type == "" || event.Data.Object.ID == "" {
		return false
	}
	binding, err := bindings.WorkflowCallbackWebhookBindingByMatch(r.Context(), endpoint.ID, event.Type, event.Data.Object.ID)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not resolve workflow webhook binding"))
		return true
	}
	run, err := s.store.GetWorkflowRun(r.Context(), binding.RunID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not resolve workflow callback run"))
		return true
	}
	if run.AppID != endpoint.AppID {
		api.WriteProblem(w, api.ErrCapacity("workflow webhook binding ownership mismatch"))
		return true
	}
	callbacks, err := workflowCallbackSpecs(run)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("workflow definition snapshot is invalid"))
		return true
	}
	var timeout time.Duration
	for _, step := range callbacks {
		if step.Name == binding.StepName {
			timeout = step.Timeout
			break
		}
	}
	if timeout <= 0 {
		api.WriteProblem(w, api.ErrCapacity("workflow callback binding step is invalid"))
		return true
	}
	duplicate, err := s.store.CompleteWorkflowCallback(r.Context(), run.ID, binding.StepName,
		api.WorkflowCallbackEventName(run.ID, binding.StepName), binding.ID, timeout, json.RawMessage(body))
	status := "received"
	if errors.Is(err, state.ErrWorkflowCallbackClosed) || errors.Is(err, state.ErrWorkflowCallbackExpired) || errors.Is(err, state.ErrConflict) {
		status = "ignored"
		err = nil
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not durably complete workflow callback"))
		return true
	}
	writeJSON(w, http.StatusAccepted, api.WorkflowCallbackWebhookReceiptResponse{
		CallbackID: binding.ID, Status: status, Duplicate: duplicate,
	})
	return true
}
