package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routemonitor"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	projectRollbackLiveTrafficWindow   = 5 * time.Minute
	projectRollbackLiveTrafficInterval = 15 * time.Second
)

func pinProjectRollbackLiveTraffic(ctx context.Context, store state.ProjectRollbackLiveTrafficStore, app state.App, op *state.ProjectRollbackRecord, step *api.ProjectRollbackStep) *api.Problem {
	monitor, err := store.GetRouteMonitor(ctx, app.AccountID, app.ID)
	if err != nil {
		return api.ErrCapacity("could not read the service production route budgets")
	}
	if !monitor.Enabled {
		return nil
	}
	policy := state.ProjectRollbackLiveTrafficPolicy{Revision: monitor.Revision}
	for _, route := range routemonitor.CloneRoutes(monitor.Routes) {
		if route.Max5xxRateBPS != nil {
			route.MaxP95MS = 0
			policy.Routes = append(policy.Routes, route)
		}
	}
	if len(policy.Routes) > 0 {
		if op.LiveTrafficPolicies == nil {
			op.LiveTrafficPolicies = make(map[string]state.ProjectRollbackLiveTrafficPolicy)
		}
		op.LiveTrafficPolicies[app.ID] = policy
		step.LiveTraffic = &api.ProjectRollbackLiveTraffic{Status: "pending"}
	}
	return nil
}

func failProjectRollbackLiveTraffic(step *api.ProjectRollbackStep, code, detail string) {
	step.LiveTraffic.Status = "failed"
	step.Reason = detail
	step.Problem = api.NewProblem(http.StatusConflict, code, "Rollback live-traffic verification paused", detail)
}

func timeoutProjectRollbackLiveTraffic(step *api.ProjectRollbackStep) {
	detail := "Live-traffic verification did not finish before the deadline."
	if step.LiveTraffic.Status == "insufficient_traffic" {
		detail = "There was insufficient production traffic to verify the restored service before the deadline."
	}
	failProjectRollbackLiveTraffic(step, api.CodeProjectRollbackLiveTrafficTimeout, detail)
}

func (s *server) verifyProjectRollbackLiveTraffic(ctx context.Context, op *state.ProjectRollbackRecord, step *api.ProjectRollbackStep) bool {
	gate := step.LiveTraffic
	now := time.Now().UTC()
	if !gate.Passed() {
		if gate.Deadline == nil {
			deadline := now.Add(projectRollbackLiveTrafficWindow)
			if op.PreparationDeadline != nil && op.PreparationDeadline.Before(deadline) {
				deadline = *op.PreparationDeadline
			}
			gate.Deadline = &deadline
			return false
		}
		if !now.Before(*gate.Deadline) {
			timeoutProjectRollbackLiveTraffic(step)
			op.Status = "blocked"
			return false
		}
	}
	routes, ok := s.store.(state.ProjectRollbackHealthStore)
	if !ok {
		return false
	}
	route, err := routes.ProjectRollbackHealthRoute(ctx, op.ID, step.AttemptID)
	if errors.Is(err, state.ErrNotFound) || err == nil && gate.RouteGeneration != nil &&
		(route.RouteGeneration == nil || *gate.RouteGeneration != *route.RouteGeneration) {
		failProjectRollbackLiveTraffic(step, api.CodeProjectRollbackLiveTrafficFailed, "The restored serving route changed before live-traffic verification could finish.")
		op.Status = "blocked"
		return false
	}
	if err != nil || route.RouteGeneration == nil {
		return false
	}
	gate.RouteGeneration = route.RouteGeneration
	if route.GatewaysAppliedAt == nil {
		gate.Status, gate.Reason = "awaiting_gateways", ""
		return false
	}
	if gate.Passed() {
		return true
	}
	if gate.CheckedAt != nil && now.Before(gate.CheckedAt.Add(projectRollbackLiveTrafficInterval)) {
		return false
	}
	gate.CheckedAt = &now
	store, ok := s.store.(state.ProjectRollbackLiveTrafficStore)
	if !ok {
		gate.Status, gate.Reason = "evidence_unavailable", "monitor_unavailable"
		return false
	}
	policy := op.LiveTrafficPolicies[step.AppID]
	report, err := store.ProjectRollbackLiveTrafficReport(ctx, op.ID, *step, policy)
	if errors.Is(err, state.ErrNotFound) {
		failProjectRollbackLiveTraffic(step, api.CodeProjectRollbackLiveTrafficFailed, "The restored serving route changed before live-traffic verification could finish.")
		op.Status = "blocked"
		return false
	}
	if err != nil || !projectRollbackLiveTrafficReportMatches(report, *step, policy) {
		gate.Status, gate.Reason = "evidence_unavailable", "monitor_unavailable"
		return false
	}
	if time.Now().Before(*gate.Deadline) {
		switch report.Status {
		case "violated":
			gate.Reason = report.Reason
			failProjectRollbackLiveTraffic(step, api.CodeProjectRollbackLiveTrafficFailed, "A production route exceeded its configured 5xx budget in two post-cutover windows.")
			if op.UndoOnLiveTrafficFailure && op.UndoOf == "" && op.PauseRequestedAt == nil {
				start, end, ok := projectRollbackLiveTrafficBreachWindow(report)
				if ok && projectRollbackHealthRouteMatches(ctx, routes, op.ID, step.AttemptID, gate.RouteGeneration) {
					op.AutoUndo = &api.ProjectRollbackAutoUndo{
						Status: api.ProjectRollbackAutoUndoPending, TriggerAppID: step.AppID,
						Trigger: api.ProjectRollbackAutoUndoLiveTrafficTrigger, RouteGeneration: gate.RouteGeneration,
						WindowStart: &start, WindowEnd: &end,
					}
				}
			}
			op.Status = "blocked"
		case "healthy":
			if projectRollbackHealthRouteMatches(ctx, routes, op.ID, step.AttemptID, gate.RouteGeneration) {
				gate.Status, gate.Reason = "passed", ""
				if projectRollbackFinished(op) {
					op.Status = "complete"
				}
			}
		default:
			gate.Status, gate.Reason = pendingProjectRollbackLiveTraffic(report)
		}
	}
	return false
}

func projectRollbackLiveTrafficReportMatches(report api.RouteMonitorReport, step api.ProjectRollbackStep, policy state.ProjectRollbackLiveTrafficPolicy) bool {
	return report.AppID == step.AppID && report.DeploymentID == step.TargetID && report.Revision == policy.Revision
}

func projectRollbackLiveTrafficBreachWindow(report api.RouteMonitorReport) (time.Time, time.Time, bool) {
	if report.Status == "violated" && report.ObservationAnchor != nil {
		for _, finding := range report.Routes {
			if finding.ErrorStatus != "violated" || len(finding.Windows) != api.RouteHealthWindows {
				continue
			}
			start, end := finding.Windows[0].Start, finding.Windows[len(finding.Windows)-1].End
			if !start.Before(*report.ObservationAnchor) && end.Sub(start) == api.RouteHealthWindows*api.RouteHealthWindow {
				return start, end, true
			}
		}
	}
	return time.Time{}, time.Time{}, false
}

func pendingProjectRollbackLiveTraffic(report api.RouteMonitorReport) (status, reason string) {
	if report.Reason != "evidence_incomplete_or_unsettled" {
		return "evidence_unavailable", report.Reason
	}
	for _, finding := range report.Routes {
		for _, window := range finding.Windows {
			switch window.ErrorReason {
			case "observation_window_not_elapsed":
				return "awaiting_traffic", window.ErrorReason
			case "insufficient_requests":
				return "insufficient_traffic", window.ErrorReason
			}
		}
	}
	return "awaiting_traffic", report.Reason
}
