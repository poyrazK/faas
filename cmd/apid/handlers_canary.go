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

// advanceCanary handles a customer-requested step. meterd uses the same
// transition handler through advanceCanaryByWorker, which adds a durable
// worker-lease requirement.
func (s *server) advanceCanary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	s.advanceCanaryWithLeasePolicy(w, r, acct, false)
}

// advanceCanaryByWorker is used only by meterd's loopback operator route. Its
// state transaction rechecks the durable lease and configured stage dwell
// before any persisted traffic advance.
func (s *server) advanceCanaryByWorker(w http.ResponseWriter, r *http.Request, acct state.Account) {
	r = r.WithContext(context.WithValue(r.Context(), bindingReleaseWorkerReadsKey{}, true))
	s.advanceCanaryWithLeasePolicy(w, r, acct, true)
}

func (s *server) advanceCanaryWithLeasePolicy(w http.ResponseWriter, r *http.Request, acct state.Account, requireWorkerLease bool) {
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
	current, next, problem := canaryStagesForAdvance(d, req.ExpectedStep)
	if problem != nil {
		if problem.Status >= http.StatusInternalServerError {
			logCustomerFailure(s.log, "read persisted canary configuration", fmt.Errorf("%s", problem.Detail))
			problem.Detail = "Gregale could not read this rollout's configuration."
			problem.Hint = "Retry the request in a moment; if it continues, contact support."
		}
		api.WriteProblem(w, problem)
		return
	}
	advancer, ok := s.store.(state.CanaryAdvancer)
	if !ok {
		writeCustomerInternalProblem(w, r, s.log, "advance canary",
			"Gregale could not advance this canary rollout.",
			"Retry the request in a moment; if it continues, contact support.", errors.New("configured store does not support atomic canary transitions"))
		return
	}
	actor := canaryProgressionActor
	if !requireWorkerLease {
		actor = "account:" + acct.ID
	}
	audit, err := canaryAdvanceAudit(d, app, acct, req.ExpectedStep, next, actor)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "prepare canary audit",
			"Gregale could not advance this canary rollout.",
			"Retry the request in a moment; if it continues, contact support.", err)
		return
	}
	profileIntent, problem := s.profileGateAdvanceIntent(r, acct, app, d, req, requireWorkerLease, next.Percent)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	var profileDecision api.ProfileCanaryGateDecision
	if !requireWorkerLease {
		audit.Actor = "account:" + acct.ID
	}
	var gateDecision api.RouteGateDecision
	var healthDecision api.RouteHealthDecision
	var updated state.Deployment
	var auditID int64
	gateProblem, err := s.withBindingReleaseTraffic(r, acct, app, d, profileIntent, func(ctx context.Context) error {
		var writeErr error
		updated, auditID, writeErr = advancer.AdvanceCanary(ctx, d.ID, state.CanaryAdvanceParams{
			ExpectedStep: req.ExpectedStep, TrafficPercent: next.Percent,
			ProfileGateDecision: &profileDecision, ProfileGateOverride: req.ProfileGateOverride, ProfileGateRollback: profileIntent == 0,
			RequireSafeReleaseLease:   requireWorkerLease,
			RequireCanaryStageElapsed: requireWorkerLease,
			CanaryStageDuration:       current.Duration,
			Audit:                     audit,
			RouteCheckFingerprint:     s.routeGateFingerprint,
			RouteGateDecision:         &gateDecision,
			RouteHealthDecision:       &healthDecision,
		})
		return writeErr
	})
	if gateProblem != nil {
		api.WriteProblem(w, gateProblem)
		return
	}
	if !s.writeCanaryAdvanceError(r.Context(), w, err, d.ID, req.ExpectedStep, d.CanaryStep) {
		return
	}
	s.notifyCanaryTraffic(r, app, updated)
	writeJSON(w, http.StatusOK, api.CanaryAdvanceResponse{
		Deployment: s.deploymentResponse(updated, app), AuditID: int64ToAuditIDString(auditID), RouteGate: &gateDecision, RouteHealth: &healthDecision, ProfileGate: &profileDecision,
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

func canaryStagesForAdvance(d state.Deployment, expected int) (canarycatalog.Stage, canarycatalog.Stage, *api.Problem) {
	if expected < 0 || d.CanaryStep != expected {
		return canarycatalog.Stage{}, canarycatalog.Stage{}, api.ErrCanaryStepConflict(expected, d.CanaryStep)
	}
	preset, err := persistedCanaryPreset(d)
	if err != nil {
		return canarycatalog.Stage{}, canarycatalog.Stage{}, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "invalid persisted canary", err.Error())
	}
	if d.CanaryTotalSteps != preset.TotalSteps() || expected >= preset.TotalSteps()-1 {
		return canarycatalog.Stage{}, canarycatalog.Stage{}, api.ErrRolloutStateInvalid(d.RolloutState)
	}
	current, ok := preset.StageAt(expected)
	if !ok {
		return canarycatalog.Stage{}, canarycatalog.Stage{}, api.ErrRolloutStateInvalid(d.RolloutState)
	}
	next, ok := preset.StageAt(expected + 1)
	if !ok {
		return canarycatalog.Stage{}, canarycatalog.Stage{}, api.ErrRolloutStateInvalid(d.RolloutState)
	}
	return current, next, nil
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

func canaryAdvanceAudit(d state.Deployment, app state.App, acct state.Account, expected int, next canarycatalog.Stage, actor string) (state.DeploymentAudit, error) {
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
		"canary_preset": d.CanaryPreset, "actor": actor,
		"at": now.Format(time.RFC3339Nano),
	})
	if err != nil {
		return state.DeploymentAudit{}, err
	}
	return state.DeploymentAudit{
		DeploymentID: depID, AccountID: &acctID, Kind: state.DeployTrafficChanged,
		Actor: actor, At: now, Data: data,
	}, nil
}

func (s *server) writeCanaryAdvanceError(ctx context.Context, w http.ResponseWriter, err error, id string, expected, observed int) bool {
	if err == nil {
		return true
	}
	var profileBlocked *state.ProfileGateBlockedError
	var routeBlocked *state.RouteGateBlockedError
	var healthBlocked *state.RouteHealthBlockedError
	switch {
	case errors.As(err, &profileBlocked):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeProfileGateBlocked, "Canary profiling gate held", profileBlocked.Decision.Reason).WithHint("Inspect GET /v1/deployments/"+id+"/canary/profile-gate for the exact stage, baseline, route and code evidence. A customer may explicitly override this profile gate with the current policy revision and a reason."))
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation(err.Error()))
	case errors.As(err, &healthBlocked):
		s.routeHealthError(w, err)
	case errors.As(err, &routeBlocked):
		s.canaryRouteGateError(w, err)
	case errors.Is(err, state.ErrSafeReleaseLeaseUnavailable):
		api.WriteProblem(w, api.ErrSafeReleaseUnavailable())
	case errors.Is(err, state.ErrCanaryStageNotElapsed):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Canary stage has not elapsed",
			"The current stage must run for its configured duration before the worker can advance the rollout."))
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
		logCustomerFailure(s.log, "advance canary", err)
		api.WriteProblem(w, api.ErrInternal("Gregale could not advance this canary rollout.").
			WithHint("Retry the request in a moment; if it continues, contact support."))
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
