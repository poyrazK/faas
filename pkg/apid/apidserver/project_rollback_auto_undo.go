package apidserver

import (
	"context"
	"errors"
	"net/http"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func projectRollbackAutoUndoBlocked(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeProjectRollbackAutoUndoBlocked, "Automatic rollback undo paused", detail)
}

func blockProjectRollbackAutoUndo(op *state.ProjectRollbackRecord, problem *api.Problem) {
	op.Status = "blocked"
	op.AutoUndo.Status = api.ProjectRollbackAutoUndoBlocked
	op.AutoUndo.Problem = problem
}

// The source remains running with its cutover gate closed until the store
// atomically transfers its slot to the inverse operation.
func (s *server) advanceProjectRollbackAutoUndo(ctx context.Context, op *state.ProjectRollbackRecord) (bool, error) {
	if op.PauseRequestedAt != nil {
		blockProjectRollbackAutoUndo(op, projectRollbackAutoUndoBlocked("An operator paused the rollback before its automatic undo started."))
		return false, nil
	}
	store, ok := s.store.(state.ProjectRollbackUndoStore)
	if !ok {
		blockProjectRollbackAutoUndo(op, api.ErrCapacity("automatic rollback undo is unavailable"))
		return false, nil
	}
	project, err := s.store.ProjectByID(ctx, op.ProjectID)
	if errors.Is(err, state.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	services, problem := s.projectRollbackUndoServices(ctx, *op)
	if problem != nil {
		blockProjectRollbackAutoUndo(op, problem)
		return false, nil
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return false, err
	}
	// Authorization for these services was checked at start. The inverse
	// still runs current rollback policy and requires any release review.
	plan := newProjectRollbackUndoPlan(*op, false)
	for _, service := range services {
		if problem := s.planProjectRollbackUndoStep(r, store, op.ID, service.app, service.original, &plan); problem != nil {
			blockProjectRollbackAutoUndo(op, problem)
			return false, nil
		}
	}
	if problem := s.checkProjectRollbackAutoUndoServing(ctx, project, *op); problem != nil {
		blockProjectRollbackAutoUndo(op, problem)
		return false, nil
	}
	initializeProjectRollback(&plan)
	undo, err := store.CreateProjectRollbackUndo(ctx, project.AccountID, *op, plan)
	if errors.Is(err, state.ErrConflict) || errors.Is(err, state.ErrNotFound) {
		// A pause, expired claim, or project removal can win the transaction.
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if s.audit != nil && undo.ID == plan.ID {
		s.audit.Emit(ctx, "project.rollback.undo_started", &project.AccountID, map[string]any{
			"project_id": op.ProjectID, "operation_id": undo.ID, "undo_of": op.ID,
			"automatic": true, "trigger_app_id": op.AutoUndo.TriggerAppID, "trigger": op.AutoUndo.Trigger,
		})
	}
	return true, nil
}

func (s *server) checkProjectRollbackAutoUndoServing(ctx context.Context, project state.Project, op state.ProjectRollbackRecord) *api.Problem {
	routes, ok := s.store.(state.ProjectRollbackHealthStore)
	trigger := slices.IndexFunc(op.Steps, func(step api.ProjectRollbackStep) bool {
		return step.AppID == op.AutoUndo.TriggerAppID && step.Status == "rolled_back"
	})
	if !ok || trigger < 0 {
		return projectRollbackAutoUndoBlocked("The failed route could no longer be confirmed. Review the current serving revisions.")
	}
	step := op.Steps[trigger]
	switch op.AutoUndo.Trigger {
	case api.ProjectRollbackAutoUndoLiveTrafficTrigger:
		if problem := s.confirmProjectRollbackLiveTrafficUndo(ctx, routes, op, step); problem != nil {
			return problem
		}
	case "":
		if !projectRollbackProbeRouteMatches(ctx, routes, op.ID, step) {
			return projectRollbackAutoUndoBlocked("The failed route could no longer be confirmed. Review the current serving revisions.")
		}
	default:
		return projectRollbackAutoUndoBlocked("The automatic undo trigger could no longer be confirmed.")
	}
	for _, step := range op.Steps {
		if step.Status == "rolled_back" || step.Status == "skipped" {
			continue
		}
		if _, problem := s.projectRollbackApp(ctx, project, step); problem != nil {
			return problem
		}
		current, problem := s.projectRollbackCurrent(ctx, step.AppID)
		if problem != nil {
			return problem
		}
		if current.ID != step.CurrentID || !sameProjectRollbackServingPeriod(current.RolloutCompletedAt, step.CurrentServedAt) {
			return projectRollbackChanged("a targeted serving revision changed before automatic undo")
		}
	}
	return nil
}

func (s *server) confirmProjectRollbackLiveTrafficUndo(ctx context.Context, routes state.ProjectRollbackHealthStore, op state.ProjectRollbackRecord, step api.ProjectRollbackStep) *api.Problem {
	store, ok := s.store.(state.ProjectRollbackLiveTrafficStore)
	if !ok || !op.VerifyLiveTraffic || !op.UndoOnLiveTrafficFailure || !op.AutoUndo.MatchesLiveTrafficFailure(step) {
		return projectRollbackAutoUndoBlocked("The saved production 5xx breach could not be confirmed.")
	}
	if !projectRollbackHealthRouteMatches(ctx, routes, op.ID, step.AttemptID, op.AutoUndo.RouteGeneration) {
		return projectRollbackAutoUndoBlocked("The serving route for the production 5xx breach changed before automatic undo.")
	}
	policy := op.LiveTrafficPolicies[step.AppID]
	report, err := store.ProjectRollbackLiveTrafficReport(ctx, op.ID, step, policy)
	_, end, breached := projectRollbackLiveTrafficBreachWindow(report)
	if err != nil || !projectRollbackLiveTrafficReportMatches(report, step, policy) || !breached || end.Before(*op.AutoUndo.WindowEnd) {
		return projectRollbackAutoUndoBlocked("New or unavailable production evidence no longer confirms the 5xx breach.")
	}
	if !projectRollbackHealthRouteMatches(ctx, routes, op.ID, step.AttemptID, op.AutoUndo.RouteGeneration) {
		return projectRollbackAutoUndoBlocked("The serving route for the production 5xx breach changed before automatic undo.")
	}
	return nil
}
