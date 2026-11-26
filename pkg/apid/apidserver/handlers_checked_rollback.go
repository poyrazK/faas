// adr: 601 — explicit historical rollback retains an exact pair through readiness.
package apidserver

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
	"github.com/onebox-faas/faas/pkg/apid/release"
	"github.com/onebox-faas/faas/pkg/apid/trafficproblem"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/openapidiff"
	"github.com/onebox-faas/faas/pkg/state"
)

// plainRollbacks is the verified plain-rollback ledger (ADR-1446).
func (s *server) plainRollbacks() *release.Rollbacks {
	return &release.Rollbacks{Store: s.store, Log: s.log, Audit: s.audit, Metrics: s.ops, AutomaticFailed: s.automaticRollbackFailed}
}

func (s *server) startCheckedRollback(w http.ResponseWriter, r *http.Request, acct state.Account, app state.App, req api.RollbackRequest) {
	operation, target, problem := s.prepareCheckedRollback(r.Context(), acct, app, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.notifyCheckedRollbackPreparation(r.Context(), app, operation, target)
	response := s.deploymentResponse(r.Context(), target, app)
	s.warnRollbackCrons(r.Context(), w, app, target)
	response.RollbackOperation = &operation
	writeJSON(w, http.StatusAccepted, response)
}

func (s *server) notifyCheckedRollbackPreparation(ctx context.Context, app state.App, operation api.RollbackOperation, target state.Deployment) {
	// Preparing targets have a transactional readiness outbox. Notify also
	// wakes local stores and service reconciliation; duplicate deliveries are safe.
	if s.notif != nil && (operation.Status == "preparing" || operation.Service) {
		channel := db.NotifySnapshotPrime
		fields := map[string]string{"app_id": app.ID, "deployment_id": target.ID}
		if operation.Status == "ready" {
			channel = db.NotifyDeploymentChanged
			fields["kind"], fields["status"] = "service_rollout", "live"
		}
		payload, _ := json.Marshal(fields)
		if err := s.notif.Notify(ctx, channel, string(payload)); err != nil {
			s.log.Warn("checked rollback readiness wakeup", "request_id", operation.ID, "err", err)
		}
	}
}

type checkedRollbackSelection struct {
	target    state.Deployment
	currentID string
	crossed   []state.ReleaseRun
}

func (s *server) inspectCheckedRollback(ctx context.Context, app state.App, req api.RollbackRequest) (checkedRollbackSelection, *api.Problem) {
	if req.TargetDeploymentID == nil || req.ExpectedCurrentDeploymentID == nil || strings.TrimSpace(*req.TargetDeploymentID) == "" || strings.TrimSpace(*req.ExpectedCurrentDeploymentID) == "" || req.AlertRuleID != nil || len(req.Reason) > api.BindingReleasePolicyReasonMaxBytes || !utf8.ValidString(req.Reason) || strings.IndexFunc(req.Reason, unicode.IsControl) >= 0 {
		return checkedRollbackSelection{}, bindingPromotionValidation("checked rollback requires target_deployment_id and expected_current_deployment_id; reason must be one line of at most 256 bytes")
	}
	targetID, problem := s.resolveDeploymentRef(ctx, app.ID, *req.TargetDeploymentID)
	if problem != nil {
		return checkedRollbackSelection{}, problem
	}
	currentID, problem := s.resolveDeploymentRef(ctx, app.ID, *req.ExpectedCurrentDeploymentID)
	if problem != nil {
		return checkedRollbackSelection{}, problem
	}
	if targetID == currentID {
		return checkedRollbackSelection{}, bindingPromotionValidation("rollback target must differ from the expected current deployment")
	}
	target, err := s.store.GetDeploymentByIDScopedToSuperseded(ctx, app.ID, targetID)
	if err != nil {
		return checkedRollbackSelection{target: target}, api.ErrRollbackTargetIneligible("select a superseded or zero-traffic live deployment of this app")
	}
	if problem := s.rollbackTargetApprovalProblem(ctx, app, target); problem != nil {
		return checkedRollbackSelection{target: target}, problem
	}
	crossed, problem := s.rollbackReleaseGuard(ctx, app, target, req.AcknowledgeReleases)
	selection := checkedRollbackSelection{target: target, currentID: currentID, crossed: crossed}
	if problem != nil {
		return selection, problem
	}
	if problem := s.verifyRollbackTargetArtifact(ctx, target); problem != nil {
		return selection, problem
	}
	if api.ApiContractDiffEnabled() && openapidiff.IsProductionScope(target.Scope) {
		check, err := openapidiff.CheckDeploymentRollback(ctx, s.store, app.ID, target.ID, currentID, target.Scope)
		if err != nil && !errors.Is(err, openapidiff.ErrSnapshotBaselineMissing) {
			return selection, api.ErrCapacity("could not evaluate API contract")
		}
		if check.Diff.Blocking() {
			return selection, contractGateProblem(check.Diff)
		}
	}
	return selection, nil
}

func (s *server) prepareCheckedRollback(ctx context.Context, acct state.Account, app state.App, req api.RollbackRequest) (api.RollbackOperation, state.Deployment, *api.Problem) {
	selection, problem := s.inspectCheckedRollback(ctx, app, req)
	if problem != nil {
		return api.RollbackOperation{}, selection.target, problem
	}
	if req.AcknowledgeReleases {
		ctx = state.WithCheckedRollbackReleaseReview(ctx, selection.crossed)
	}
	return s.persistCheckedRollback(ctx, acct, app, req, selection.target, selection.currentID)
}
func (s *server) persistCheckedRollback(ctx context.Context, acct state.Account, app state.App, req api.RollbackRequest, target state.Deployment, currentID string) (api.RollbackOperation, state.Deployment, *api.Problem) {
	store, ok := s.store.(state.CheckedRollbackStore)
	if !ok {
		return api.RollbackOperation{}, target, api.ErrCapacity("checked rollback store is unavailable")
	}
	operation, err := store.CreateCheckedRollback(ctx, acct.ID, app.ID, target.ID, currentID, req.Reason)
	if err != nil {
		if problem := rollbackAttemptProblem(err); problem != nil {
			return operation, target, problem
		}
		if errors.Is(err, state.ErrLayerArtifactRetired) {
			return operation, target, rollbackTargetArtifactsReclaimed()
		}
		if errors.Is(err, state.ErrCheckedRollbackChanged) || errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNoRollbackTarget) {
			return operation, target, api.NewProblem(http.StatusConflict, "rollback_deployment_changed", "Rollback selection changed", "The expected deployment must still serve all traffic, with no active rollout or other rollback in this scope.")
		}
		// The customer sees a retryable 503; the cause has to reach the
		// operator log (production-us hunt #9: the first of two identical
		// rollbacks failed after 16 s and nothing recorded why).
		s.log.Error("checked rollback: persist", "app", app.ID, "target", target.ID, "expected_current", currentID, "err", err)
		return operation, target, trafficproblem.Policy(err, api.ErrCapacity("could not persist checked rollback"))
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
		// ADR-1446: plain rollbacks share this receipt endpoint.
		if plain, ok := s.plainRollbacks().Operation(r.Context(), acct.ID, app.ID, id.String()); ok {
			writeJSON(w, http.StatusOK, plain)
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Rollback not found", "No such rollback operation for this app."))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read rollback progress"))
		return
	}
	writeJSON(w, http.StatusOK, operation)
}
