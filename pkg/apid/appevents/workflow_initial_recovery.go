package appevents

import (
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *Service) ListWorkflowInitialFailures(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	failures, ok := store.(state.WorkflowInitialRecoveryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("workflow failure inventory"))
		return
	}
	q := r.URL.Query()
	o := api.WorkflowInitialFailureOptions{Workflow: q.Get("workflow"), After: q.Get("after")}
	for key, values := range q {
		if (key != "workflow" && key != "after" && key != "limit") || len(values) != 1 {
			api.WriteProblem(w, api.ErrValidation("invalid failure inventory filters"))
			return
		}
	}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid failure inventory limit"))
			return
		}
		o.Limit = n
	}
	out, err := failures.ListWorkflowInitialFailures(r.Context(), acct.ID, app.ID, o)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) PreviewWorkflowInitialRecovery(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, r, cancel, ok := s.eventRecoveryStore(w, r)
	if !ok {
		return
	}
	defer cancel()
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	failures, ok := store.(state.WorkflowInitialRecoveryStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("workflow failure inventory"))
		return
	}
	if len(r.URL.Query()) != 0 {
		api.WriteProblem(w, api.ErrValidation("recovery preview does not accept query parameters"))
		return
	}
	out, err := failures.PreviewWorkflowInitialRecovery(r.Context(), acct.ID, app.ID, r.PathValue("failureID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
