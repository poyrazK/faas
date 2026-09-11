package main

import (
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) prewarmStore(w http.ResponseWriter) (state.PrewarmStore, bool) {
	store, ok := s.store.(state.PrewarmStore)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusNotImplemented, api.CodeNotImplemented,
			"Prewarm unavailable", "the scheduler has not been upgraded with scheduled prewarm support"))
	}
	return store, ok
}

func (s *server) createPrewarm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.prewarmStore(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.PrewarmRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid prewarm request"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	if req.Count <= 0 || req.Count > limits.MaxConcurrency {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid prewarm count", "count must be between 1 and the app plan's max concurrency"))
		return
	}
	if err := state.ValidatePrewarmIntent(req.Count, req.WakeAt, req.ExpiresAt, time.Now(), state.PrewarmTriggerCalendar); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid prewarm window", err.Error()))
		return
	}
	intent, err := store.CreatePrewarmIntent(r.Context(), app.ID, acct.ID, req.Count, req.WakeAt, req.ExpiresAt, state.PrewarmTriggerCalendar)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such app")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not schedule prewarm"))
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "prewarm.scheduled", &acct.ID, map[string]any{
			"intent_id":  intent.ID,
			"app_id":     intent.AppID,
			"count":      intent.Count,
			"wake_at":    intent.WakeAt,
			"expires_at": intent.ExpiresAt,
			"trigger":    intent.Trigger,
		})
	}
	writeJSON(w, http.StatusAccepted, prewarmResponse(intent))
}

func (s *server) listPrewarms(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.prewarmStore(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rows, err := store.ListPrewarmIntentsForApp(r.Context(), app.ID, 50)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list prewarm intents"))
		return
	}
	resp := make([]api.PrewarmIntentResponse, 0, len(rows))
	for _, row := range rows {
		resp = append(resp, prewarmResponse(row))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) cancelPrewarm(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.prewarmStore(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	intent, err := store.PrewarmIntentByID(r.Context(), r.PathValue("id"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such prewarm intent")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load prewarm intent"))
		return
	}
	if intent.AccountID != acct.ID || intent.AppID != app.ID {
		s.notFound(w, "no such prewarm intent")
		return
	}
	if err := store.CancelPrewarmIntent(r.Context(), intent.ID, acct.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such prewarm intent")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not cancel prewarm intent"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func prewarmResponse(intent state.PrewarmIntent) api.PrewarmIntentResponse {
	return api.PrewarmIntentResponse{
		ID: intent.ID, AppID: intent.AppID, Count: intent.Count,
		WakeAt: intent.WakeAt, ExpiresAt: intent.ExpiresAt,
		Trigger: intent.Trigger, Status: intent.Status, CreatedAt: intent.CreatedAt,
		ClaimedAt: intent.ClaimedAt, FiredAt: intent.FiredAt,
		AdmittedCount: intent.AdmittedCount,
		Outcome:       intent.Outcome, LastError: intent.LastError,
	}
}
