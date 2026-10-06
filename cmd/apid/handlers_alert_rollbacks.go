package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listAlertRollbacks(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.AlertRollbackStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("alert rollback status is unavailable"))
		return
	}
	rows, err := store.ListAlertRollbacks(r.Context(), acct.ID, app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read alert rollback status"))
		return
	}
	writeJSON(w, http.StatusOK, rows)
}
func (s *server) getAlertRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("fire"))
	if err != nil {
		api.WriteProblem(w, bindingPromotionValidation("fire must be an alert delivery UUID"))
		return
	}
	store, ok := s.store.(state.AlertRollbackStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("alert rollback status is unavailable"))
		return
	}
	operation, err := store.GetAlertRollback(r.Context(), acct.ID, app.ID, id.String())
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such alert rollback for this app")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read alert rollback status"))
		return
	}
	writeJSON(w, http.StatusOK, operation)
}
func (s *server) internalProcessAlertRollback(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("fire"))
	if err != nil {
		api.WriteProblem(w, bindingPromotionValidation("fire must be an alert delivery UUID"))
		return
	}
	store, ok := s.store.(state.AlertRollbackStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("alert rollback store is unavailable"))
		return
	}
	operation, err := store.ReadAlertRollback(r.Context(), id.String())
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such alert rollback")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read alert rollback"))
		return
	}
	if err = s.processAlertRollback(r.Context(), operation); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not process alert rollback"))
		return
	}
	operation, err = store.ReadAlertRollback(r.Context(), id.String())
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read alert rollback progress"))
		return
	}
	writeJSON(w, http.StatusOK, operation)
}
