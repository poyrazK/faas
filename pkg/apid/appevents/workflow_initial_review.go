package appevents

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
)

func (s *Service) initialReviewScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.WorkflowInitialRecoveryReviewStore, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, "", false
	}
	store, ok := s.Host.Store().(state.WorkflowInitialRecoveryReviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("initial recovery review store"))
		return nil, "", false
	}
	if len(r.URL.Query()) != 0 {
		api.WriteProblem(w, api.ErrValidation("initial recovery reviews do not accept query parameters"))
		return nil, "", false
	}
	return store, app.ID, true
}
func (s *Service) PrepareWorkflowInitialRecoveryReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.initialReviewScope(w, r, acct)
	if !ok {
		return
	}
	var req api.WorkflowInitialRecoveryReviewRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid initial review request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	out, err := store.PrepareWorkflowInitialRecoveryReview(recoveryActorRequest(r).Context(), acct.ID, app, r.PathValue("failureID"), req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) GetWorkflowInitialRecoveryReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.initialReviewScope(w, r, acct)
	if !ok {
		return
	}
	out, err := store.GetWorkflowInitialRecoveryReview(r.Context(), acct.ID, app, r.PathValue("reviewID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) DecideWorkflowInitialRecoveryReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.initialReviewScope(w, r, acct)
	if !ok {
		return
	}
	var req api.WorkflowInitialRecoveryReviewDecisionRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid initial review decision"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if req.Decision == "submit" && !s.Host.WorkflowRuntimeEnabled() {
		api.WriteProblem(w, api.ErrWorkflowDeploymentUnavailable())
		return
	}
	out, err := store.DecideWorkflowInitialRecoveryReview(recoveryActorRequest(r).Context(), acct.ID, app, r.PathValue("reviewID"), req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) ListWorkflowInitialRecoveryReviews(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.Host.Store().(state.WorkflowInitialRecoveryReviewStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("initial recovery review store"))
		return
	}
	q := r.URL.Query()
	limit := api.EventRecoveryHistoryPageMax
	for key, values := range q {
		if (key != "after" && key != "limit") || len(values) != 1 {
			api.WriteProblem(w, api.ErrValidation("invalid initial review filters"))
			return
		}
	}
	if q.Has("limit") {
		n, err := strconv.Atoi(q.Get("limit"))
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid initial review limit"))
			return
		}
		limit = n
	}
	out, err := store.ListWorkflowInitialRecoveryReviews(r.Context(), acct.ID, app.ID, q.Get("after"), limit)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
