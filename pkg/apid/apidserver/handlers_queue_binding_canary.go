// adr:1352
package apidserver

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/bindings"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
)

func (s *server) createQueueBindingCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.CreateQueueBindingCanaryRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	store, ok := s.store.(state.QueueBindingCanaryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("queue canary is unavailable"))
		return
	}
	result, err := store.CreateQueueBindingCanary(r.Context(), acct.ID, app.ID, req)
	if problem := bindings.QueueCanaryProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.audit.Emit(r.Context(), "queue.canary_published", &acct.ID, map[string]any{"app_id": app.ID, "canary_id": result.ID, "deployment_id": result.DeploymentID, "binding": result.Binding})
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, result)
}
func (s *server) getQueueBindingCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.readQueueBindingCanary(w, r, acct, false)
}
func (s *server) cancelQueueBindingCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.readQueueBindingCanary(w, r, acct, true)
}
func (s *server) readQueueBindingCanary(w http.ResponseWriter, r *http.Request, acct state.Account, cancel bool) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.store.(state.QueueBindingCanaryStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("queue canary is unavailable"))
		return
	}
	var result api.QueueBindingCanary
	var err error
	if cancel {
		result, err = store.CancelQueueBindingCanary(r.Context(), acct.ID, app.ID, r.PathValue("canary"))
	} else {
		result, err = store.GetQueueBindingCanary(r.Context(), acct.ID, app.ID, r.PathValue("canary"))
	}
	if problem := bindings.QueueCanaryProblem(err); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if cancel {
		s.audit.Emit(r.Context(), "queue.canary_cancelled", &acct.ID, map[string]any{"app_id": app.ID, "canary_id": result.ID})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, result)
}
