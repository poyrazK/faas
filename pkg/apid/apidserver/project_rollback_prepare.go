package apidserver

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	projectRollbackPreparationWindow      = 10 * time.Minute
	projectRollbackConcurrentPreparations = 4
)

func beginProjectRollbackPreparation(op *state.ProjectRollbackRecord, now time.Time) {
	deadline := now.Add(projectRollbackPreparationWindow)
	healthBudget := projectRollbackHealthWindow + time.Duration(op.StabilizeForSeconds)*time.Second
	for _, step := range op.Steps {
		if step.Status == "skipped" {
			continue
		}
		if step.Health != nil && !step.Health.PassedFor(op.StabilizeForSeconds) {
			deadline = deadline.Add(healthBudget)
		}
		if step.LiveTraffic != nil && !step.LiveTraffic.Passed() {
			deadline = deadline.Add(projectRollbackLiveTrafficWindow)
		}
	}
	op.Phase, op.PreparationDeadline = api.ProjectRollbackPreparing, &deadline
}

func projectRollbackPending(step api.ProjectRollbackStep) bool {
	return step.Status != "skipped" && step.Status != "rolled_back"
}

func (s *server) advancePreparedProjectRollback(ctx context.Context, op *state.ProjectRollbackRecord) error {
	children, ok := s.store.(state.CheckedRollbackStore)
	if !ok {
		return errors.New("checked rollback operations are unavailable")
	}
	if op.Phase == api.ProjectRollbackReleasing {
		return s.releaseProjectRollbackPreparation(ctx, op, children)
	}
	if op.PreparationDeadline == nil || !time.Now().Before(*op.PreparationDeadline) {
		for i := range op.Steps {
			step := &op.Steps[i]
			if step.Status == "rolled_back" && step.Health != nil && !step.Health.PassedFor(op.StabilizeForSeconds) {
				failProjectRollbackHealth(step, api.CodeProjectRollbackHealthTimeout, "Health verification did not finish before the rollback deadline.")
				continue
			}
			if step.Status == "rolled_back" && step.LiveTraffic != nil && !step.LiveTraffic.Passed() {
				timeoutProjectRollbackLiveTraffic(step)
				continue
			}
			if !projectRollbackPending(*step) {
				continue
			}
			if step.PreCutoverHealth != nil && step.PreCutoverHealth.Status == "awaiting_probe" {
				failProjectRollbackPreCutoverHealth(step, api.CodeProjectRollbackPreCutoverHealthTimeout,
					"Timed out waiting for a successful health probe of the prepared revision.")
				continue
			}
			if step.PreparationStatus != "blocked" {
				step.Reason = "The project rollback preparation deadline expired."
				step.Problem = api.NewProblem(http.StatusConflict, api.CodeProjectRollbackPreparationExpired, "Rollback preparation expired", step.Reason)
			}
		}
		op.Phase = api.ProjectRollbackReleasing
		return nil
	}
	project, err := s.store.ProjectByID(ctx, op.ProjectID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			op.Phase = api.ProjectRollbackReleasing
			return nil
		}
		return err
	}
	if err := s.reconcileProjectRollbackPreparation(ctx, op, project, children); err != nil {
		return err
	}
	if op.Status == "blocked" {
		// Persist the closed barrier before releasing children. A restart
		// can continue cleanup without making another cutover eligible.
		op.Status, op.Phase = "running", api.ProjectRollbackReleasing
	}
	return nil
}

func (s *server) reconcileProjectRollbackPreparation(ctx context.Context, op *state.ProjectRollbackRecord, project state.Project, children state.CheckedRollbackStore) error {
	if op.Phase == api.ProjectRollbackPreparing && !s.projectRollbackServingPins(ctx, project, op, len(op.Steps)-1) {
		return nil
	}
	type admission struct {
		step *api.ProjectRollbackStep
		app  state.App
	}
	var admissions []admission
	pending, warming, prepared := 0, 0, 0
	for i := range op.Steps {
		step := &op.Steps[i]
		if !projectRollbackPending(*step) {
			if step.Status == "rolled_back" {
				if op.Phase == api.ProjectRollbackCutover && !s.projectRollbackServingPin(ctx, project, op, step, false) {
					return nil
				}
				if step.Health != nil && !s.verifyProjectRollbackStep(ctx, op, step) {
					return nil
				}
				if step.LiveTraffic != nil && !s.verifyProjectRollbackLiveTraffic(ctx, op, step) {
					return nil
				}
			}
			continue
		}
		pending++
		app, problem := s.projectRollbackApp(ctx, project, *step)
		if problem != nil {
			setProjectRollbackProblem(op, step, problem)
			return nil
		}
		child, err := children.GetCheckedRollback(ctx, app.AccountID, app.ID, step.AttemptID)
		if errors.Is(err, state.ErrNotFound) {
			if op.Phase != api.ProjectRollbackPreparing || step.Status != "planned" || step.AttemptID == "" {
				setProjectRollbackProblem(op, step, projectRollbackChanged("the prepared rollback receipt is unavailable"))
				return nil
			}
			admissions = append(admissions, admission{step: step, app: app})
			continue
		}
		if err != nil {
			return err
		}
		if child.ProjectRollbackID != op.ID || child.ID != step.AttemptID || child.AppID != step.AppID ||
			child.TargetDeploymentID != step.TargetID || child.CurrentDeploymentID != step.CurrentID || child.Scope != step.Scope {
			setProjectRollbackProblem(op, step, projectRollbackChanged("the prepared rollback receipt does not match the saved plan"))
			return nil
		}
		switch child.Status {
		case "complete", "routing":
			if op.Phase != api.ProjectRollbackCutover || pending != 1 {
				setProjectRollbackProblem(op, step, projectRollbackChanged("a prepared rollback advanced out of order"))
				return nil
			}
			updateProjectRollbackChild(op, step, child)
			if step.Status == "rolled_back" && (step.Health != nil || step.LiveTraffic != nil) {
				// Save the cutover before starting its first health round.
				return nil
			}
		case "failed":
			step.PreparationStatus, step.Blockers = "blocked", child.Blockers
			updateProjectRollbackChild(op, step, child)
			return nil
		default:
			recordProjectRollbackPreparation(step, child)
			if step.PreparationStatus == "ready" {
				prepared++
			} else {
				warming++
				if op.Phase == api.ProjectRollbackPreparing && step.PreCutoverHealthPassed() {
					step.PreCutoverHealth = &api.ProjectRollbackPreCutoverHealth{Status: "pending"}
				}
			}
		}
	}
	if pending == 0 {
		op.Status = "complete"
		return nil
	}
	if op.Phase == api.ProjectRollbackCutover {
		return nil
	}
	if prepared == pending {
		if op.VerifyBeforeCutover {
			for i := range op.Steps {
				step := &op.Steps[i]
				if projectRollbackPending(*step) && len(op.HealthProbes[step.AppID]) > 0 && !step.PreCutoverHealthPassed() {
					// Persist each result before probing the next target or authorizing traffic.
					return s.verifyPreparedProjectRollbackStep(ctx, op, step)
				}
			}
		}
		// The commit path rechecks every pending pair and its preparation
		// while holding this project receipt's row lock.
		op.Phase = api.ProjectRollbackCutover
		return nil
	}
	for _, next := range admissions[:min(len(admissions), max(0, projectRollbackConcurrentPreparations-warming))] {
		if err := s.admitProjectRollbackStep(ctx, op, next.app, next.step); err != nil {
			return err
		}
		if op.Status == "blocked" {
			return nil
		}
		if next.step.ReceiptID != "" {
			next.step.PreparationStatus = "warming"
		}
	}
	return nil
}

func recordProjectRollbackPreparation(step *api.ProjectRollbackStep, child api.RollbackOperation) {
	step.Status, step.ReceiptID = "in_progress", child.ID
	step.Reason, step.Problem, step.Blockers = "", nil, nil
	switch {
	case child.Status == "preparing":
		step.PreparationStatus = "warming"
	case child.Status == "ready" && child.Code == api.CodeProjectRollbackPrepared:
		step.PreparationStatus = "ready"
	case child.Status == "blocked":
		step.PreparationStatus, step.Blockers = "blocked", child.Blockers
		step.Reason = child.Code
		if len(child.Blockers) > 0 {
			step.Reason = child.Blockers[0].Message
		}
		step.Problem = api.NewProblem(http.StatusConflict, child.Code, "Rollback preparation blocked", step.Reason)
	default:
		step.PreparationStatus = "checking"
	}
}

func (s *server) releaseProjectRollbackPreparation(ctx context.Context, op *state.ProjectRollbackRecord, children state.CheckedRollbackStore) error {
	pending := false
	releaseCode := api.CodeProjectRollbackPreparationReleased
	if op.PauseRequestedAt != nil {
		releaseCode = api.CodeProjectRollbackPaused
	}
	for i := range op.Steps {
		step := &op.Steps[i]
		if !projectRollbackPending(*step) {
			continue
		}
		app, err := s.store.AppByID(ctx, step.AppID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return err
		}
		if err == nil {
			child, err := children.GetCheckedRollback(ctx, app.AccountID, app.ID, step.AttemptID)
			switch {
			case errors.Is(err, state.ErrNotFound):
			case err != nil:
				return err
			case !projectRollbackCleanupOwnsChild(op, *step, child):
				step.Problem = projectRollbackChanged("the prepared rollback receipt does not match the saved plan")
				step.Reason = step.Problem.Detail
			case child.Status == "routing":
				pending = true
				continue
			case child.Status == "complete":
				updateProjectRollbackChild(op, step, child)
				continue
			case child.Status != "failed":
				if op.PauseRequestedAt == nil && child.Status == "blocked" && (step.Problem == nil || step.Problem.Code == api.CodeProjectRollbackPreparationExpired) {
					recordProjectRollbackPreparation(step, child)
				}
				if err := children.UpdateCheckedRollback(ctx, child, "failed", releaseCode, child.Blockers); err != nil {
					if errors.Is(err, state.ErrCheckedRollbackChanged) {
						pending = true
						continue
					}
					if !errors.Is(err, state.ErrNotFound) {
						return err
					}
				}
			}
		}
		step.Status, step.PreparationStatus = "blocked", "released"
		if op.PauseRequestedAt != nil && step.Problem == nil {
			step.Problem = api.NewProblem(http.StatusConflict, api.CodeProjectRollbackPaused, "Project rollback paused", "An operator paused this operation before the service switched.")
			step.Reason = step.Problem.Detail
		} else if step.Reason == "" {
			step.Reason = "Project rollback preparation was released."
		}
	}
	if !pending && op.AutoUndo != nil && op.AutoUndo.Status == api.ProjectRollbackAutoUndoPending {
		if op.PauseRequestedAt != nil {
			blockProjectRollbackAutoUndo(op, projectRollbackAutoUndoBlocked("An operator paused the rollback before its automatic undo started."))
		} else {
			op.Status = "running"
			op.AutoUndo.Status = api.ProjectRollbackAutoUndoReady
		}
		return nil
	}
	if !pending && (op.PauseRequestedAt != nil || op.Status != "complete") {
		op.Status = "blocked"
	}
	return nil
}

func projectRollbackCleanupOwnsChild(op *state.ProjectRollbackRecord, step api.ProjectRollbackStep, child api.RollbackOperation) bool {
	owned := child.ProjectRollbackID == op.ID || child.ProjectRollbackID == "" && op.PauseRequestedAt != nil
	return owned && child.ID == step.AttemptID && child.AppID == step.AppID && child.TargetDeploymentID == step.TargetID &&
		child.CurrentDeploymentID == step.CurrentID && child.Scope == step.Scope
}
