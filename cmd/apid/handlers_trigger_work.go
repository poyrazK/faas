package main

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) triggerWorkResource(w http.ResponseWriter, r *http.Request, acct state.Account) (string, string, state.TriggerWorkBindingStore, bool) {
	id, ok := parseTriggerID(w, r)
	if !ok {
		return "", "", nil, false
	}
	trigger, err := s.store.TriggerByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such trigger")
		return "", "", nil, false
	}
	appID := uuidFromPgtype(trigger.AppID).String()
	app, err := s.store.AppByID(r.Context(), appID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such trigger")
		return "", "", nil, false
	}
	bindings, ok := s.store.(state.TriggerWorkBindingStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("trigger work bindings unavailable"))
		return "", "", nil, false
	}
	return id, appID, bindings, true
}

func (s *server) getTriggerWorkBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, _, bindings, ok := s.triggerWorkResource(w, r, acct)
	if !ok {
		return
	}
	binding, err := bindings.TriggerWorkBindingByID(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load trigger work binding"))
		return
	}
	if binding == nil {
		s.notFound(w, "trigger has no work binding")
		return
	}
	writeJSON(w, http.StatusOK, api.TriggerWorkBinding{
		PolicyName: binding.PolicyName, Key: binding.KeySelector,
		FairnessKey: binding.FairnessSelector,
	})
}

func (s *server) putTriggerWorkBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, appID, bindings, ok := s.triggerWorkResource(w, r, acct)
	if !ok {
		return
	}
	var req api.TriggerWorkBinding
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.PolicyName == "" || req.Key == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid work binding", "policy_name and key are required"))
		return
	}
	if _, err := bindings.SetTriggerWorkBinding(r.Context(), appID, id,
		req.PolicyName, req.Key, req.FairnessKey); err != nil {
		writeTriggerWorkBindingError(w, err)
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyTriggerChanged, notifyTriggerChangedJSON("updated", appID, id))
	writeJSON(w, http.StatusOK, req)
}

func (s *server) deleteTriggerWorkBinding(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id, appID, bindings, ok := s.triggerWorkResource(w, r, acct)
	if !ok {
		return
	}
	if _, err := bindings.SetTriggerWorkBinding(r.Context(), appID, id, "", "", ""); err != nil {
		writeTriggerWorkBindingError(w, err)
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyTriggerChanged, notifyTriggerChangedJSON("updated", appID, id))
	w.WriteHeader(http.StatusNoContent)
}

func writeTriggerWorkBindingError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeValidation,
			"Work policy not found", "the named work policy does not exist for this app"))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Trigger work binding conflict", "initial binding requires a disabled trigger with no existing records"))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid work binding", "this trigger kind cannot use a work binding"))
	default:
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Invalid work binding", err.Error()))
	}
}
