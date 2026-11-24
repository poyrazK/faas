package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/deploystaging"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/routehealth"
	"github.com/onebox-faas/faas/pkg/routeprobe"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	projectRollbackHealthWindow          = time.Minute
	projectRollbackStabilizationInterval = 5 * time.Second
	projectRollbackStabilizationMaxGap   = 3 * projectRollbackStabilizationInterval
)

func (s *server) projectRollbackHealthAvailable() bool {
	sender, canProbe := s.routeProbes.(deploystaging.ProjectCriticalProbeSender)
	_, hasRoutes := s.store.(state.ProjectRollbackHealthStore)
	return canProbe && sender.Configured() && hasRoutes && s.notif != nil
}

func (s *server) projectRollbackProbes(ctx context.Context, app state.App) ([]api.ProjectReleaseProbe, *api.Problem) {
	store, ok := s.store.(state.RouteHealthStore)
	if !ok {
		return nil, api.ErrCapacity("could not read the service health probe configuration")
	}
	gate, err := store.GetRouteHealthGate(ctx, app.AccountID, app.ID)
	if err != nil {
		return nil, api.ErrCapacity("could not read the service health probe configuration")
	}
	var probes []api.ProjectReleaseProbe
	for _, route := range gate.Routes {
		if route.Probe != nil && routehealth.ValidateProbe(route) == nil {
			probes = append(probes, api.ProjectReleaseProbe{AppID: app.ID, Slug: app.Slug, Method: route.Method, Path: route.Probe.Path})
		}
	}
	if len(probes) == 0 {
		for _, path := range app.Manifest.ReleaseProbePaths {
			if api.ValidProbePath(path) {
				probes = append(probes, api.ProjectReleaseProbe{AppID: app.ID, Slug: app.Slug, Method: http.MethodGet, Path: path})
			}
		}
	}
	return probes, nil
}

func failProjectRollbackHealth(step *api.ProjectRollbackStep, code, detail string) {
	step.Health.Status, step.Health.HealthySince = "failed", nil
	step.Reason = detail
	step.Problem = api.NewProblem(http.StatusConflict, code, "Rollback health verification paused", detail)
}

// A true result lets the parent advance past this step. A newly passed check
// is persisted by the caller before any other child can cut over.
func (s *server) verifyProjectRollbackStep(ctx context.Context, op *state.ProjectRollbackRecord, step *api.ProjectRollbackStep) bool {
	h := step.Health
	now := time.Now().UTC()
	stabilizeFor := time.Duration(op.StabilizeForSeconds) * time.Second
	passed := h.PassedFor(op.StabilizeForSeconds)
	if !passed {
		if h.Deadline == nil {
			deadline := now.Add(projectRollbackHealthWindow + stabilizeFor)
			if op.PreparationDeadline != nil && op.PreparationDeadline.Before(deadline) {
				deadline = *op.PreparationDeadline
			}
			h.Deadline = &deadline
			// Save the deadline before an external probe can outlive this worker.
			return false
		}
		if !now.Before(*h.Deadline) {
			detail := "Timed out waiting for gateway acknowledgements of the restored route."
			switch h.Status {
			case "awaiting_probe":
				detail = "Timed out waiting for a successful health probe of the restored revision."
			case "stabilizing":
				detail = "The restored revision did not finish its health stabilization window before the deadline."
			}
			failProjectRollbackHealth(step, api.CodeProjectRollbackHealthTimeout, detail)
			op.Status = "blocked"
			return false
		}
	}
	routes, ok := s.store.(state.ProjectRollbackHealthStore)
	if !ok {
		return false
	}
	route, err := routes.ProjectRollbackHealthRoute(ctx, op.ID, step.AttemptID)
	if errors.Is(err, state.ErrNotFound) || err == nil && h.RouteGeneration != nil &&
		(route.RouteGeneration == nil || *h.RouteGeneration != *route.RouteGeneration) {
		failProjectRollbackHealth(step, api.CodeProjectRollbackHealthFailed, "The restored serving route changed before health verification could finish.")
		op.Status = "blocked"
		return false
	}
	if err != nil || route.RouteGeneration == nil {
		if !passed {
			h.HealthySince = nil
		}
		return false
	}
	if passed {
		return route.GatewaysAppliedAt != nil
	}
	h.RouteGeneration = route.RouteGeneration
	if route.GatewaysAppliedAt == nil {
		h.Status, h.HealthySince = "awaiting_gateways", nil
		return false
	}
	if !s.projectRollbackHealthAvailable() {
		h.Status, h.HealthySince = "awaiting_probe", nil
		return false
	}
	previousCheck := h.CheckedAt
	if stabilizeFor > 0 && previousCheck != nil && now.Before(previousCheck.Add(projectRollbackStabilizationInterval)) {
		return false
	}
	h.Status = "awaiting_probe"
	probeDeadline := now.Add(api.RouteHealthProbeRequestTimeout + 2*time.Second)
	if h.Deadline.Before(probeDeadline) {
		probeDeadline = *h.Deadline
	}
	probeCtx, cancel := context.WithDeadline(ctx, probeDeadline)
	defer cancel()
	outcome := s.probeProjectRollbackService(probeCtx, op.HealthProbes[step.AppID], *step)
	checked := time.Now().UTC()
	h.CheckedAt = &checked
	if outcome == routeprobe.Unattributed || !checked.Before(*h.Deadline) {
		h.HealthySince = nil
		return false
	}
	if outcome != routeprobe.Success {
		reason := "The restored revision returned a non-2xx response to its health probe."
		if outcome == routeprobe.ServerError {
			reason = "The restored revision returned 5xx to its health probe."
			if op.UndoOnHealthFailure && op.UndoOf == "" && op.PauseRequestedAt == nil &&
				projectRollbackHealthRouteMatches(ctx, routes, op.ID, step.AttemptID, h.RouteGeneration) {
				op.AutoUndo = &api.ProjectRollbackAutoUndo{Status: api.ProjectRollbackAutoUndoPending, TriggerAppID: step.AppID}
			}
		}
		failProjectRollbackHealth(step, api.CodeProjectRollbackHealthFailed, reason)
		op.Status = "blocked"
		return false
	}

	// Attribution proves which revision answered. Confirm that the same
	// production route was still in effect after the probe.
	if !projectRollbackHealthRouteMatches(ctx, routes, op.ID, step.AttemptID, h.RouteGeneration) {
		h.HealthySince = nil
		return false
	}
	if stabilizeFor > 0 {
		if h.HealthySince == nil || previousCheck == nil || checked.Sub(*previousCheck) > projectRollbackStabilizationMaxGap {
			h.HealthySince = &checked
		}
		if checked.Sub(*h.HealthySince) < stabilizeFor {
			h.Status = "stabilizing"
			return false
		}
	}
	h.Status = "passed"
	if projectRollbackFinished(op) {
		op.Status = "complete"
	}
	return false
}

func projectRollbackProbeRouteMatches(ctx context.Context, routes state.ProjectRollbackHealthStore, operation string, step api.ProjectRollbackStep) bool {
	return step.Health != nil && projectRollbackHealthRouteMatches(ctx, routes, operation, step.AttemptID, step.Health.RouteGeneration)
}

func projectRollbackHealthRouteMatches(ctx context.Context, routes state.ProjectRollbackHealthStore, operation, attempt string, generation *int64) bool {
	after, err := routes.ProjectRollbackHealthRoute(ctx, operation, attempt)
	return err == nil && generation != nil && after.GatewaysAppliedAt != nil && after.RouteGeneration != nil &&
		*after.RouteGeneration == *generation
}

func failProjectRollbackPreCutoverHealth(step *api.ProjectRollbackStep, code, detail string) {
	step.PreCutoverHealth.Status = "failed"
	step.Reason = detail
	step.Problem = api.NewProblem(http.StatusConflict, code, "Pre-cutover health verification failed", detail)
}

func (s *server) verifyPreparedProjectRollbackStep(ctx context.Context, op *state.ProjectRollbackRecord, step *api.ProjectRollbackStep) error {
	target, err := s.store.DeploymentByID(ctx, step.TargetID)
	if errors.Is(err, state.ErrNotFound) || err == nil &&
		(target.AppID != step.AppID || target.DeletedAt != nil || target.Status != state.DeployLive || target.TrafficPercent != 0) {
		setProjectRollbackProblem(op, step, projectRollbackChanged("the prepared revision is no longer available at zero traffic"))
		return nil
	}
	if err != nil {
		return err
	}

	h := &api.ProjectRollbackPreCutoverHealth{Status: "awaiting_probe", AttemptID: step.AttemptID}
	step.PreCutoverHealth = h
	if !s.projectRollbackHealthAvailable() {
		return nil
	}
	probeDeadline := time.Now().Add(api.RouteHealthProbeRequestTimeout + 2*time.Second)
	if op.PreparationDeadline.Before(probeDeadline) {
		probeDeadline = *op.PreparationDeadline
	}
	probeCtx, cancel := context.WithDeadline(ctx, probeDeadline)
	defer cancel()
	outcome := s.probeProjectRollbackService(probeCtx, op.HealthProbes[step.AppID], *step)
	checked := time.Now().UTC()
	h.CheckedAt = &checked
	if outcome == routeprobe.Unattributed || !checked.Before(*op.PreparationDeadline) {
		return nil
	}
	if outcome != routeprobe.Success {
		detail := "The prepared revision returned a non-2xx response to its health probe."
		if outcome == routeprobe.ServerError {
			detail = "The prepared revision returned 5xx to its health probe."
		}
		failProjectRollbackPreCutoverHealth(step, api.CodeProjectRollbackPreCutoverHealthFailed, detail)
		op.Status = "blocked"
		return nil
	}
	h.Status = "passed"
	return nil
}

func (s *server) probeProjectRollbackService(ctx context.Context, probes []api.ProjectReleaseProbe, step api.ProjectRollbackStep) routeprobe.Outcome {
	sender := s.routeProbes.(deploystaging.ProjectCriticalProbeSender)
	if len(probes) == 0 {
		return routeprobe.Unattributed
	}
	token, err := deploystaging.PublishRouteProbeChallenge(ctx, step.Scope, step.AppID, step.TargetID, func(ctx context.Context, payload string) error {
		return s.notif.Notify(ctx, db.NotifyRouteProbeChallenge, payload)
	})
	if err != nil {
		return routeprobe.Unattributed
	}
	select {
	case <-ctx.Done():
		return routeprobe.Unattributed
	case <-time.After(routeProbeChallengeDelay):
	}
	outcome := routeprobe.Success
	for _, probe := range probes {
		result, err := sender.ProbeCritical(ctx, probe.Slug, step.TargetID, token, probe.Method, probe.Path)
		if err != nil || result == routeprobe.Unattributed {
			outcome = routeprobe.Unattributed
			continue
		}
		if result != routeprobe.Success {
			return result
		}
	}
	return outcome
}
