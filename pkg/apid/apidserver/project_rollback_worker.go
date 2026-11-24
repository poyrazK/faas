package apidserver

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const projectRollbackLease = 45 * time.Second

func (s *server) runProjectRollbackWorker(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := s.projectRollbackSweep(bounded, projectRollbackLease, 2*time.Second)
		if store, ok := s.store.(state.ProjectRollbackHTTPRecoveryStore); ok && bounded.Err() == nil {
			err = errors.Join(err, store.ReplayProjectRollbackRoutes(bounded))
		}
		cancel()
		if err != nil && ctx.Err() == nil {
			s.log.Warn("project rollback sweep", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *server) projectRollbackSweep(ctx context.Context, lease, delay time.Duration) error {
	store, ok := s.store.(state.ProjectRollbackOperationStore)
	if !ok {
		return nil
	}
	for i := 0; i < api.ServiceBindingCheckBatchSize && ctx.Err() == nil; i++ {
		op, err := store.ClaimProjectRollback(ctx, lease)
		if errors.Is(err, state.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if op.Phase == api.ProjectRollbackReleasing && op.AutoUndo != nil && op.AutoUndo.Status == api.ProjectRollbackAutoUndoReady {
			committed, err := s.advanceProjectRollbackAutoUndo(ctx, &op)
			if err != nil {
				return err
			}
			if committed {
				continue
			}
		} else if err := s.advanceProjectRollback(ctx, &op); err != nil {
			return err
		}
		now := time.Now().UTC()
		op.UpdatedAt = &now
		if err := store.SaveProjectRollback(ctx, op, delay); err != nil && !errors.Is(err, state.ErrConflict) {
			return err
		}
	}
	return ctx.Err()
}

func (s *server) advanceProjectRollback(ctx context.Context, op *state.ProjectRollbackRecord) error {
	if op.Phase != "" {
		return s.advancePreparedProjectRollback(ctx, op)
	}
	next := slices.IndexFunc(op.Steps, func(step api.ProjectRollbackStep) bool {
		return step.Status != "skipped" && step.Status != "rolled_back"
	})
	if next < 0 {
		op.Status = "complete"
		return nil
	}
	step := &op.Steps[next]
	project, err := s.store.ProjectByID(ctx, op.ProjectID)
	if errors.Is(err, state.ErrNotFound) {
		setProjectRollbackProblem(op, step, projectRollbackChanged("the project is no longer available"))
		return nil
	}
	if err != nil {
		return err
	}
	app, problem := s.projectRollbackApp(ctx, project, *step)
	if problem != nil {
		setProjectRollbackProblem(op, step, problem)
		return nil
	}
	children, ok := s.store.(state.CheckedRollbackStore)
	if !ok {
		return errors.New("checked rollback operations are unavailable")
	}
	// Recover an accepted child before preflight. Admission may have changed
	// the target's state while the parent still has the step marked planned.
	child, err := children.GetCheckedRollback(ctx, app.AccountID, app.ID, step.AttemptID)
	if err == nil {
		updateProjectRollbackChild(op, step, child)
		return nil
	}
	if !errors.Is(err, state.ErrNotFound) {
		return err
	}
	if step.Status != "planned" || step.AttemptID == "" {
		setProjectRollbackProblem(op, step, projectRollbackChanged("the accepted service rollback receipt is unavailable"))
		return nil
	}
	if !s.projectRollbackServingPins(ctx, project, op, next) {
		return nil
	}
	return s.admitProjectRollbackStep(ctx, op, app, step)
}

func (s *server) admitProjectRollbackStep(ctx context.Context, op *state.ProjectRollbackRecord, app state.App, step *api.ProjectRollbackStep) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "/", nil)
	if err != nil {
		return err
	}
	selection, warnings, problem := s.inspectProjectRollbackStep(request, app, *step, op.AcknowledgeReleases)
	if problem != nil {
		setProjectRollbackProblem(op, step, problem)
		return nil
	}
	step.Warnings = warnings
	review := op.ReleaseReviews[app.ID]
	if projectRollbackReleaseReviewChanged(review, selection.crossed) {
		setProjectRollbackProblem(op, step, projectRollbackChanged("a release command started after the project rollback was reviewed"))
		return nil
	}
	resource, err := state.AppResourceAccount(ctx, s.store, app)
	if err != nil {
		return err
	}
	admission := state.WithProjectRollbackStep(ctx, *op, *step)
	if op.AcknowledgeReleases {
		admission = state.WithCheckedRollbackReleaseReview(admission, review)
	}
	child, _, problem := s.persistCheckedRollback(admission, resource, app, api.RollbackRequest{}, selection.target, selection.currentID)
	if problem != nil {
		setProjectRollbackProblem(op, step, problem)
		return nil
	}
	s.notifyCheckedRollbackPreparation(ctx, app, child, selection.target)
	updateProjectRollbackChild(op, step, child)
	return nil
}

func projectRollbackReleaseReviewChanged(review, crossed []state.ReleaseRun) bool {
	for _, run := range crossed {
		if !slices.ContainsFunc(review, func(ack state.ReleaseRun) bool {
			return ack.DeploymentID == run.DeploymentID && ack.StartedAt.Equal(run.StartedAt)
		}) {
			return true
		}
	}
	return false
}

func (s *server) projectRollbackApp(ctx context.Context, project state.Project, step api.ProjectRollbackStep) (state.App, *api.Problem) {
	app, err := s.store.AppByID(ctx, step.AppID)
	if err != nil && !errors.Is(err, state.ErrNotFound) {
		return app, api.ErrCapacity("could not read the targeted service")
	}
	if app.ProjectID != project.ID || app.Status == state.AppDeleted || app.Slug != step.Slug {
		return app, projectRollbackChanged("the targeted service no longer belongs to this project")
	}
	return app, nil
}

func (s *server) projectRollbackServingPins(ctx context.Context, project state.Project, op *state.ProjectRollbackRecord, next int) bool {
	for i := 0; i <= next; i++ {
		step := &op.Steps[i]
		if step.Status == "skipped" {
			continue
		}
		checkServingPeriod := step.Status != "rolled_back" && (op.Phase == api.ProjectRollbackPreparing || i == next)
		if !s.projectRollbackServingPin(ctx, project, op, step, checkServingPeriod) {
			return false
		}
	}
	return true
}

func (s *server) projectRollbackServingPin(ctx context.Context, project state.Project, op *state.ProjectRollbackRecord, step *api.ProjectRollbackStep, checkServingPeriod bool) bool {
	if _, problem := s.projectRollbackApp(ctx, project, *step); problem != nil {
		setProjectRollbackProblem(op, step, problem)
		return false
	}
	current, problem := s.projectRollbackCurrent(ctx, step.AppID)
	if problem != nil {
		setProjectRollbackProblem(op, step, problem)
		return false
	}
	expected := step.CurrentID
	if step.Status == "rolled_back" {
		expected = step.TargetID
	}
	if current.ID != expected || checkServingPeriod && !sameProjectRollbackServingPeriod(current.RolloutCompletedAt, step.CurrentServedAt) {
		setProjectRollbackProblem(op, step, projectRollbackChanged("the targeted production serving revision changed after this operation was planned"))
		return false
	}
	return true
}

func sameProjectRollbackServingPeriod(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}

func setProjectRollbackProblem(op *state.ProjectRollbackRecord, step *api.ProjectRollbackStep, problem *api.Problem) {
	step.Problem, step.Reason = problem, problem.Detail
	if problem.Status < http.StatusInternalServerError {
		op.Status = "blocked"
		if step.Status == "planned" {
			step.Status = "blocked"
		}
	}
}

func updateProjectRollbackChild(op *state.ProjectRollbackRecord, step *api.ProjectRollbackStep, child api.RollbackOperation) {
	if child.ID != step.AttemptID || child.AppID != step.AppID || child.TargetDeploymentID != step.TargetID ||
		child.CurrentDeploymentID != step.CurrentID || child.Scope != step.Scope {
		setProjectRollbackProblem(op, step, projectRollbackChanged("the service rollback receipt does not match the planned deployment pair"))
		return
	}
	step.ReceiptID = child.ID
	step.Reason, step.Problem = "", nil
	switch child.Status {
	case "complete":
		if child.AuditID == "" || child.CompletedAt == nil {
			setProjectRollbackProblem(op, step, projectRollbackChanged("the service rollback is missing its committed receipt"))
			return
		}
		step.Status, step.DeploymentID = "rolled_back", child.TargetDeploymentID
		if projectRollbackFinished(op) {
			op.Status = "complete"
		}
	case "failed":
		step.Status = "failed"
		detail := "Review the service rollback receipt before retrying."
		if child.Code == api.CodeProjectRollbackUndoArtifactsUnavailable {
			detail = "Gregale could not retain the serving revision's artifacts for undo. Traffic remains on that revision."
		}
		setProjectRollbackProblem(op, step, api.NewProblem(http.StatusConflict, child.Code, "Service rollback failed", detail))
	case "blocked":
		step.Status = "in_progress"
		step.Reason = child.Code
		if len(child.Blockers) > 0 {
			step.Reason = child.Blockers[0].Message
		}
	default:
		step.Status = "in_progress"
	}
}

func projectRollbackFinished(op *state.ProjectRollbackRecord) bool {
	return !slices.ContainsFunc(op.Steps, func(step api.ProjectRollbackStep) bool {
		return projectRollbackPending(step) || step.Health != nil && !step.Health.PassedFor(op.StabilizeForSeconds) ||
			len(op.LiveTrafficPolicies[step.AppID].Routes) > 0 && !step.LiveTraffic.Passed()
	})
}
