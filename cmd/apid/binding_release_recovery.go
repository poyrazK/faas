package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
	"github.com/onebox-faas/faas/pkg/state"
)

// Only an exact canary abort has a synchronous, known traffic recipient.
// Service rollouts retain their separate gateway acknowledgement/drain path.
func (s *server) recoverPinnedCanaryRollout(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, req api.RecoverRolloutRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	r = r.WithContext(ctx)
	candidateID, candidateErr := uuid.Parse(req.DeploymentID)
	predecessorID, predecessorErr := uuid.Parse(req.ExpectedPredecessorDeploymentID)
	if req.Action != "abort" || candidateErr != nil || predecessorErr != nil || candidateID == predecessorID {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid recovery selection", "Exact recovery requires action abort and two different deployment UUIDs: deployment_id and expected_predecessor_deployment_id."))
		return
	}
	lookup := func(id uuid.UUID) (state.Deployment, error) {
		d, err := s.store.DeploymentByID(ctx, id.String())
		if errors.Is(err, state.ErrNotFound) {
			d, err = s.store.DeploymentByID(ctx, strings.ReplaceAll(id.String(), "-", ""))
		}
		return d, err
	}
	candidate, err := lookup(candidateID)
	if err != nil || candidate.AppID != app.ID {
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrInternal("could not read recovery deployment"))
		} else {
			s.notFound(w, "no such recovery deployment")
		}
		return
	}
	predecessor, err := lookup(predecessorID)
	if err != nil || predecessor.AppID != app.ID || predecessor.Status != state.DeployLive || (!state.IsServiceRollout(candidate) && predecessor.TrafficPercent <= 0) || !predecessor.CreatedAt.Before(candidate.CreatedAt) || recoveryScope(predecessor.Scope) != recoveryScope(candidate.Scope) {
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrInternal("could not read recovery predecessor"))
		} else {
			s.notFound(w, "no such serving predecessor in the deployment scope")
		}
		return
	}
	if state.IsServiceRollout(candidate) {
		s.requestPinnedServiceAbort(w, r, acct, app, candidate, predecessor, req.Reason)
		return
	}
	if !acct.Plan.TrafficSplitAllowed() {
		api.WriteProblem(w, api.ErrPlanTrafficSplitNotAllowed(acct.Plan))
		return
	}
	rolloutState := state.NormalizeRolloutState(candidate.RolloutState)
	if candidate.Status != state.DeployLive || candidate.CanaryTotalSteps <= 0 || (rolloutState != "pending" && rolloutState != "rolling_out") {
		api.WriteProblem(w, api.ErrRolloutStateInvalid(rolloutState))
		return
	}
	recoverer, ok := s.store.(exactDeploymentRolloutRecoverer)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("exact rollout recovery is unavailable"))
		return
	}
	policyStore, ok := s.store.(state.BindingReleasePolicyStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("binding release policies are unavailable"))
		return
	}
	policy, err := policyStore.GetBindingReleasePolicy(ctx, acct.ID, app.ID, predecessor.Scope)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("binding release policy could not be read"))
		return
	}
	var observations []bindingPromotionObservation
	if policy.Mode == "enforce" {
		age, err := time.ParseDuration(policy.MaxVerificationAge)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("binding release policy is invalid"))
			return
		}
		observation, problem := s.observeBindingPromotion(r, acct, app, predecessor, api.BindingPromotionRequest{RequireApplicationAck: policy.RequireApplicationAck}, age)
		if problem != nil {
			if problem.BindingsCheck != nil {
				problem = problem.WithHint("Resolve the binding blockers and verify this exact retained predecessor before retrying recovery.")
			}
			api.WriteProblem(w, problem)
			return
		}
		observation.expiration.Message = "Binding verification expired before recovery; verify the exact predecessor again."
		observations = append(observations, observation)
	}
	var updated state.Deployment
	var auditID int64
	req.Reason = safetext.Truncate(req.Reason, api.AuditReasonMaxBytes)
	problem, err := s.withBindingReleaseObservations(r, acct, app, observations, func(writeCtx context.Context) error {
		var writeErr error
		updated, auditID, writeErr = recoverer.RecoverRolloutForDeployment(writeCtx, app.ID, candidate.ID, predecessor.ID, "abort", req.Reason)
		return writeErr
	})
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such active rollout or serving predecessor")
		case errors.Is(err, state.ErrRolloutStateInvalid):
			api.WriteProblem(w, api.ErrRolloutStateInvalid(updated.RolloutState))
		default:
			writeCustomerInternalProblem(w, r, s.log, "recover exact deployment rollout", "Gregale could not recover this rollout.", "Retry the request in a moment; if it continues, contact support.", err)
		}
		return
	}
	receipt := &api.RolloutRecoveryReceipt{DeploymentID: updated.ID, PredecessorDeploymentID: predecessor.ID, RestoredTrafficPercent: 100}
	for _, observation := range observations {
		receipt.BindingsChecks = append(receipt.BindingsChecks, observation.report)
	}
	if s.audit != nil {
		s.audit.Emit(ctx, "deployment.rollout_recovered", &acct.ID, map[string]any{
			"app": app.ID, "deployment": updated.ID, "expected_predecessor": predecessor.ID,
			"action": "abort", "reason": req.Reason, "deployment_audit": auditID, "actor": acct.ID,
		})
	}
	s.notifyCanaryTraffic(r, app, updated)
	writeJSON(w, http.StatusOK, api.RolloutTransitionResponse{Deployment: s.deploymentResponse(updated, app), AuditID: int64ToAuditIDString(auditID), Recovery: receipt})
}

func recoveryScope(scope string) string {
	if scope == "" {
		return "default"
	}
	return scope
}
