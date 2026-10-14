package appevents

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"strconv"
	"time"
)

func (s *Service) workflowRecoveryPolicyScope(w http.ResponseWriter, r *http.Request, acct state.Account) (state.WorkflowRecoveryPolicyStore, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.Host.Store().(state.WorkflowRecoveryPolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("recovery policy unavailable"))
		return nil, "", false
	}
	app, ok := s.Host.LoadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return nil, "", false
	}
	return store, app.ID, true
}
func (s *Service) GetWorkflowRecoveryPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	out, err := store.GetWorkflowRecoveryPolicy(r.Context(), acct.ID, app)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) SetWorkflowRecoveryPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	var req api.SetWorkflowRecoveryPolicyRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid recovery policy request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if req.Policy.Enabled && req.Policy.Mode == "automatic_initial" && !s.Host.WorkflowRuntimeEnabled() {
		api.WriteProblem(w, api.ErrWorkflowDeploymentUnavailable())
		return
	}
	r = recoveryActorRequest(r)
	out, err := store.SetWorkflowRecoveryPolicy(r.Context(), acct.ID, app, req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) ListWorkflowRecoveryPolicyReviews(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	limit := api.EventRecoveryHistoryPageMax
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("invalid review limit"))
			return
		}
		limit = n
	}
	out, err := store.ListWorkflowRecoveryPolicyReviews(r.Context(), acct.ID, app, r.URL.Query().Get("after"), limit)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) GetWorkflowRecoveryPolicyReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	out, err := store.GetWorkflowRecoveryPolicyReview(r.Context(), acct.ID, app, r.PathValue("reviewID"))
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

func (s *Service) DecideWorkflowRecoveryPolicyReview(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	var req api.WorkflowRecoveryReviewDecisionRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid review decision request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	r = recoveryActorRequest(r)
	out, err := store.DecideWorkflowRecoveryPolicyReview(r.Context(), acct.ID, app, r.PathValue("reviewID"), req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) GetWorkflowRecoveryReviewStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	inspector, ok := store.(state.WorkflowRecoveryReviewInspector)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("review inspection unavailable"))
		return
	}
	out, err := state.InspectWorkflowRecoveryReview(r.Context(), inspector, acct.ID, app, r.PathValue("reviewID"), time.Now().UTC())
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

func (s *Service) ControlWorkflowRecoveryPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	operator, ok := store.(state.WorkflowRecoveryPolicyOperatorStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("policy controls unavailable"))
		return
	}
	var req api.ControlWorkflowRecoveryPolicyRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid policy control request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	r = recoveryActorRequest(r)
	out, err := operator.ControlWorkflowRecoveryPolicy(r.Context(), acct.ID, app, req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
func (s *Service) GetWorkflowRecoveryAutomaticQueue(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	operator, ok := store.(state.WorkflowRecoveryPolicyOperatorStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("automatic queue unavailable"))
		return
	}
	out, err := operator.GetWorkflowRecoveryAutomaticQueue(r.Context(), acct.ID, app)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}

func (s *Service) SuppressWorkflowRecoveryAutomaticAction(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, app, ok := s.workflowRecoveryPolicyScope(w, r, acct)
	if !ok {
		return
	}
	operator, ok := store.(state.WorkflowRecoveryAutomaticActionOperatorStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("automatic suppression unavailable"))
		return
	}
	var req api.SuppressWorkflowRecoveryAutomaticActionRequest
	if err := httpkit.DecodeJSONSized(r, &req, api.EventRecoveryRequestMaxBytes); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid automatic suppression request"))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	r = recoveryActorRequest(r)
	out, err := operator.SuppressWorkflowRecoveryAutomaticAction(r.Context(), acct.ID, app, r.PathValue("reviewID"), r.PathValue("actionID"), req)
	s.writeEventRecovery(w, r, http.StatusOK, out, err)
}
