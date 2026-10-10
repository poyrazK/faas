// adr: 601 — explicit historical rollback retains an exact pair through readiness.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) startCheckedRollback(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, req api.RollbackRequest) {
	operation, target, problem := s.prepareCheckedRollback(r.Context(), acct, app, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	// PostgreSQL commits the readiness outbox entry with intent. Notify also
	// supports in-memory/local stores; duplicate deliveries are safe.
	if s.notif != nil {
		payload, _ := json.Marshal(map[string]string{"app_id": app.ID, "deployment_id": target.ID})
		if err := s.notif.Notify(r.Context(), db.NotifySnapshotPrime, string(payload)); err != nil {
			s.log.Warn("checked rollback readiness wakeup", "request_id", operation.ID, "err", err)
		}
	}
	response := s.deploymentResponse(target, app)
	response.RollbackOperation = &operation
	writeJSON(w, http.StatusAccepted, response)
}
func (s *server) prepareCheckedRollback(ctx context.Context, acct state.Account, app state.App, req api.RollbackRequest) (api.RollbackOperation, state.Deployment, *api.Problem) {
	var empty api.RollbackOperation
	if req.TargetDeploymentID == nil || req.ExpectedCurrentDeploymentID == nil || strings.TrimSpace(*req.TargetDeploymentID) == "" || strings.TrimSpace(*req.ExpectedCurrentDeploymentID) == "" || req.AlertRuleID != nil || len(req.Reason) > api.BindingReleasePolicyReasonMaxBytes || !utf8.ValidString(req.Reason) || strings.IndexFunc(req.Reason, unicode.IsControl) >= 0 {
		return empty, state.Deployment{}, bindingPromotionValidation("checked rollback requires target_deployment_id and expected_current_deployment_id; reason must be one line of at most 256 bytes")
	}
	targetID, problem := s.resolveDeploymentRef(ctx, app.ID, *req.TargetDeploymentID)
	if problem != nil {
		return empty, state.Deployment{}, problem
	}
	currentID, problem := s.resolveDeploymentRef(ctx, app.ID, *req.ExpectedCurrentDeploymentID)
	if problem != nil {
		return empty, state.Deployment{}, problem
	}
	if targetID == currentID {
		return empty, state.Deployment{}, bindingPromotionValidation("rollback target must differ from the expected current deployment")
	}
	target, err := s.store.GetDeploymentByIDScopedToSuperseded(ctx, app.ID, targetID)
	if err != nil {
		return empty, target, api.ErrRollbackTargetIneligible("select a superseded or zero-traffic live deployment of this app")
	}
	if problem := s.verifyRollbackTargetArtifact(ctx, target); problem != nil {
		return empty, target, problem
	}
	if api.ApiContractDiffEnabled() && strings.EqualFold(target.Scope, "prod") {
		return s.prepareCheckedRollbackWithContract(ctx, acct, app, req, target, currentID)
	}
	return s.persistCheckedRollback(ctx, acct, app, req, target, currentID)
}
func (s *server) persistCheckedRollback(ctx context.Context, acct state.Account, app state.App, req api.RollbackRequest, target state.Deployment, currentID string) (api.RollbackOperation, state.Deployment, *api.Problem) {
	store, ok := s.store.(state.CheckedRollbackStore)
	if !ok {
		return api.RollbackOperation{}, target, api.ErrCapacity("checked rollback store is unavailable")
	}
	operation, err := store.CreateCheckedRollback(ctx, acct.ID, app.ID, target.ID, currentID, req.Reason)
	if err != nil {
		if errors.Is(err, state.ErrCheckedRollbackChanged) || errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNoRollbackTarget) {
			return operation, target, api.NewProblem(http.StatusConflict, "rollback_deployment_changed", "Rollback selection changed", "The expected deployment must still serve all traffic, with no active rollout or other rollback in this scope.")
		}
		return operation, target, api.ErrCapacity("could not persist checked rollback")
	}
	prepared, err := s.store.DeploymentByID(ctx, target.ID)
	if err != nil {
		prepared = target
		prepared.Status = state.DeploySnapshotting
		prepared.TrafficPercent = 0
	}
	return operation, prepared, nil
}
func (s *server) getCheckedRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	id, err := uuid.Parse(r.PathValue("operation"))
	if err != nil {
		api.WriteProblem(w, bindingPromotionValidation("operation must be a rollback UUID"))
		return
	}
	store, ok := s.store.(state.CheckedRollbackStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("checked rollback store is unavailable"))
		return
	}
	operation, err := store.GetCheckedRollback(r.Context(), acct.ID, app.ID, id.String())
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Rollback not found", "No such rollback operation for this app."))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read rollback progress"))
		return
	}
	writeJSON(w, http.StatusOK, operation)
}

func (s *server) prepareCheckedRollbackWithContract(ctx context.Context, acct state.Account, app state.App, req api.RollbackRequest, target state.Deployment, currentID string) (api.RollbackOperation, state.Deployment, *api.Problem) {
	check, err := openapidiff.CheckDeploymentPromotion(ctx, s.store, app.ID, target.ID, "prod")
	if err != nil && !errors.Is(err, openapidiff.ErrSnapshotBaselineMissing) {
		return api.RollbackOperation{}, target, api.ErrCapacity("could not evaluate API contract")
	}
	if check.Diff.Blocking() {
		return api.RollbackOperation{}, target, contractGateProblem(check.Diff)
	}
	return s.persistCheckedRollback(ctx, acct, app, req, target, currentID)
}
