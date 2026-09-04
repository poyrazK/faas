// handlers_canary.go — APID-owned automatic canary transitions
// (issue #976 / ADR-122).

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	canarycatalog "github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

const canaryProgressionActor = "meterd:canary_progression"

// advanceCanary is the APID write seam used by meterd. The request carries
// only the step observed by the worker; APID derives the next percentage from
// persisted state before handing the atomic transition to the store.
func (s *server) advanceCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !acct.Plan.TrafficSplitAllowed() {
		api.WriteProblem(w, api.ErrPlanTrafficSplitNotAllowed(acct.Plan))
		return
	}
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	var req api.AdvanceCanaryRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	next, problem := nextCanaryStage(d, req.ExpectedStep)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	advancer, ok := s.store.(state.CanaryAdvancer)
	if !ok {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "canary unavailable", "the configured state store does not support atomic canary transitions"))
		return
	}
	audit, err := canaryAdvanceAudit(d, app, acct, req.ExpectedStep, next)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "canary audit unavailable", err.Error()))
		return
	}
	updated, auditID, err := advancer.AdvanceCanary(r.Context(), d.ID, state.CanaryAdvanceParams{
		ExpectedStep: req.ExpectedStep, TrafficPercent: next.Percent, Audit: audit,
	})
	if !s.writeCanaryAdvanceError(r.Context(), w, err, d.ID, req.ExpectedStep, d.CanaryStep) {
		return
	}
	s.notifyCanaryTraffic(r, app, updated)
	writeJSON(w, http.StatusOK, api.CanaryAdvanceResponse{
		Deployment: s.deploymentResponse(updated, app), AuditID: int64ToAuditIDString(auditID),
	})
}

func (s *server) loadCanaryDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Deployment, state.App, bool) {
	d, err := s.store.DeploymentByID(r.Context(), r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such deployment")
		return state.Deployment{}, state.App{}, false
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return state.Deployment{}, state.App{}, false
	}
	return d, app, true
}

func nextCanaryStage(d state.Deployment, expected int) (canarycatalog.Stage, *api.Problem) {
	if expected < 0 || d.CanaryStep != expected {
		return canarycatalog.Stage{}, api.ErrCanaryStepConflict(expected, d.CanaryStep)
	}
	preset, err := persistedCanaryPreset(d)
	if err != nil {
		return canarycatalog.Stage{}, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "invalid persisted canary", err.Error())
	}
	if d.CanaryTotalSteps != preset.TotalSteps() || expected >= preset.TotalSteps()-1 {
		return canarycatalog.Stage{}, api.ErrRolloutStateInvalid(d.RolloutState)
	}
	next, ok := preset.StageAt(expected + 1)
	if !ok {
		return canarycatalog.Stage{}, api.ErrRolloutStateInvalid(d.RolloutState)
	}
	return next, nil
}

func persistedCanaryPreset(d state.Deployment) (canarycatalog.Preset, error) {
	if d.CanaryPreset == "custom" {
		var stages []canarycatalog.CustomStage
		if err := json.Unmarshal(d.CanaryStages, &stages); err != nil {
			return canarycatalog.Preset{}, fmt.Errorf("custom stages: %w", err)
		}
		return canarycatalog.LookupCustomPreset(stages)
	}
	preset, ok := canarycatalog.LookupPreset(d.CanaryPreset)
	if !ok {
		return canarycatalog.Preset{}, fmt.Errorf("unknown preset %q", d.CanaryPreset)
	}
	return preset, nil
}

func canaryAdvanceAudit(d state.Deployment, app state.App, acct state.Account, expected int, next canarycatalog.Stage) (state.DeploymentAudit, error) {
	depID, err := uuid.Parse(d.ID)
	if err != nil {
		return state.DeploymentAudit{}, fmt.Errorf("deployment id %q: %w", d.ID, err)
	}
	acctID, err := uuid.Parse(acct.ID)
	if err != nil {
		return state.DeploymentAudit{}, fmt.Errorf("account id %q: %w", acct.ID, err)
	}
	now := time.Now().UTC()
	data, err := json.Marshal(map[string]any{
		"deployment_id": d.ID, "app_id": app.ID, "from_percent": d.TrafficPercent,
		"to_percent": next.Percent, "from_step": expected, "to_step": expected + 1,
		"canary_preset": d.CanaryPreset, "actor": canaryProgressionActor,
		"at": now.Format(time.RFC3339Nano),
	})
	if err != nil {
		return state.DeploymentAudit{}, err
	}
	return state.DeploymentAudit{
		DeploymentID: depID, AccountID: &acctID, Kind: state.DeployTrafficChanged,
		Actor: canaryProgressionActor, At: now, Data: data,
	}, nil
}

func (s *server) writeCanaryAdvanceError(ctx context.Context, w http.ResponseWriter, err error, id string, expected, observed int) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, state.ErrCanaryStepConflict):
		// The losing request may have loaded the same step as the
		// winner before the store CAS ran. Re-read for an accurate
		// problem detail; the response must not claim the deployment
		// is still at the stale step.
		if current, readErr := s.store.DeploymentByID(ctx, id); readErr == nil {
			observed = current.CanaryStep
		}
		api.WriteProblem(w, api.ErrCanaryStepConflict(expected, observed))
	case errors.Is(err, state.ErrCanaryStateInvalid):
		api.WriteProblem(w, api.ErrRolloutStateInvalid("current"))
	case errors.Is(err, state.ErrTrafficPercentSumInvalid):
		api.WriteProblem(w, api.ErrTrafficPercentSumInvalid(0))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such deployment"))
	default:
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "canary advance failed", err.Error()))
	}
	return false
}

func (s *server) notifyCanaryTraffic(r *http.Request, app state.App, d state.Deployment) {
	if s.notif == nil {
		return
	}
	payload, _ := json.Marshal(map[string]any{
		"kind": "traffic", "app_id": app.ID, "deployment_id": d.ID,
		"traffic_percent": d.TrafficPercent,
	})
	if err := s.notif.Notify(r.Context(), db.NotifyDeploymentChanged, string(payload)); err != nil {
		s.log.Warn("apid: notify deployment_changed (canary) failed", "err", err)
	}
}
