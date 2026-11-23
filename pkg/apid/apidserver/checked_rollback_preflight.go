package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) preflightCheckedRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	query := r.URL.Query()
	target, current := query.Get("target_deployment_id"), query.Get("expected_current_deployment_id")
	var acknowledge bool
	if value := query.Get("acknowledge_releases"); value != "" {
		var err error
		acknowledge, err = strconv.ParseBool(value)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("acknowledge_releases must be a boolean"))
			return
		}
	}
	selection, problem := s.inspectCheckedRollback(r.Context(), app, api.RollbackRequest{
		TargetDeploymentID: &target, ExpectedCurrentDeploymentID: &current, AcknowledgeReleases: acknowledge,
	})
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	warnings, problem := s.checkRollbackPreflight(r, app, selection)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, warnings)
}

func (s *server) checkRollbackPreflight(r *http.Request, app state.App, selection checkedRollbackSelection) ([]api.BindingCheckFinding, *api.Problem) {
	ctx, target := r.Context(), selection.target
	store, ok := s.store.(state.CheckedRollbackPreflightStore)
	if !ok {
		return nil, api.ErrCapacity("rollback preflight is unavailable")
	}
	if err := store.CheckRollbackPreflight(ctx, app.AccountID, app.ID, target.ID, selection.currentID); err != nil {
		return nil, rollbackPreflightProblem(err)
	}
	if target.Status == state.DeployLive {
		if problem := s.supersededRollbackTargetProblem(ctx, app, target); problem != nil {
			return nil, problem
		}
	}
	resource, err := state.AppResourceAccount(ctx, s.store, app)
	if err != nil {
		return nil, api.ErrCapacity("could not read deployment account policy")
	}
	if !resource.MayDeploy() {
		return nil, api.NewProblem(http.StatusConflict, "account_deploy_blocked", "Deployment unavailable", "The account cannot currently change serving deployments.")
	}
	warnings := make([]api.BindingCheckFinding, 0, len(selection.crossed)+1)
	for _, run := range selection.crossed {
		warnings = append(warnings, api.BindingCheckFinding{
			Code: api.CodeRollbackCrossesRelease, DeploymentID: run.DeploymentID,
			Message: fmt.Sprintf("Release command for v%d (%s) will remain applied.", run.Revision, run.Status),
		})
	}
	if target.Status != state.DeployLive {
		policies, ok := s.store.(state.BindingReleasePolicyStore)
		if !ok {
			return nil, api.ErrCapacity("binding release policies are unavailable")
		}
		policy, err := policies.GetBindingReleasePolicy(ctx, resource.ID, app.ID, target.Scope)
		if err != nil {
			return nil, api.ErrCapacity("binding release policy could not be read")
		}
		if problem := s.durable.ValidatorReleaseProblem(ctx, app, target); problem != nil {
			return nil, problem
		}
		if policy.Mode == "enforce" {
			if age, err := time.ParseDuration(policy.MaxVerificationAge); err != nil || age <= 0 {
				return nil, api.ErrCapacity("binding release policy is invalid")
			}
			warnings = append(warnings, api.BindingCheckFinding{Code: "rollback_binding_verification_deferred",
				DeploymentID: target.ID, Message: "Binding verification requires a prepared target and will run before routing."})
		}
		return warnings, nil
	}
	// Use the same internal reads as the checked rollback worker, and
	// return only the overall verdict to a caller with app read access.
	check := r.Clone(context.WithValue(ctx, bindingReleaseWorkerReadsKey{}, true))
	check.URL.RawQuery = ""
	if _, problem := s.serviceRecipientBindingObservation(check, resource, app, target); problem != nil {
		if problem.BindingsCheck != nil {
			problem = api.NewProblem(problem.Status, problem.Code, problem.Title, "The rollback target does not satisfy the current binding release policy.")
		}
		return nil, problem
	}
	return warnings, nil
}

func rollbackPreflightProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrLayerArtifactRetired):
		return rollbackTargetArtifactsReclaimed()
	case errors.Is(err, state.ErrCheckedRollbackChanged):
		return api.NewProblem(http.StatusConflict, "rollback_deployment_changed", "Rollback selection changed", "The expected revision must still serve all traffic with no active rollout.")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, "rollback_operation_required", "Checked rollback in progress", "Wait for the active rollback in this scope.")
	case errors.Is(err, state.ErrProjectReleaseRollbackRequired):
		return api.NewProblem(http.StatusConflict, api.CodeProjectReleaseRollbackRequired, "Project release owns traffic", "Use the environment release-set rollback workflow.")
	case errors.Is(err, state.ErrInvalidArgument):
		return api.ErrRollbackTargetIneligible("this serving scope is managed by environment GitOps")
	case errors.Is(err, state.ErrNotFound), errors.Is(err, state.ErrNoRollbackTarget):
		return api.ErrRollbackTargetIneligible("select an available superseded or zero-traffic live deployment of this app")
	default:
		return api.ErrCapacity("could not check rollback admission")
	}
}
