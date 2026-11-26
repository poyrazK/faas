package apidserver

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewProjectRollbackResume(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	_, source, ok := s.loadProjectRollback(w, r, acct)
	if !ok {
		return
	}
	if source.UndoOperationID != "" {
		api.WriteProblem(w, projectRollbackUndoStarted(source.UndoOperationID))
		return
	}
	if source.Status != "blocked" {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeProjectRollbackConflict,
			"Rollback resume unavailable", "Choose a paused rollback or undo."))
		return
	}
	apps, problem := s.authorizeProjectRollbackResume(r, acct, source, "GET /v1/apps/{slug}")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	plan, _, problem := s.planProjectRollbackResume(r, source, apps)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	s.observeProjectRollbackUndoArtifacts(r.Context(), source.ID, &plan)
	writeJSON(w, http.StatusOK, plan.ProjectRollbackOperation)
}

func (s *server) resumeProjectRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, op, ok := s.loadProjectRollback(w, r, acct)
	if !ok {
		return
	}
	var req api.ProjectRollbackResumeRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	expected := req.ExpectedPlanHash
	if expected != "" && !api.ValidProjectRollbackPlanHash(expected) {
		api.WriteProblem(w, api.ErrValidation("expected_plan_hash must be a 64-character lowercase hexadecimal hash"))
		return
	}
	if op.UndoOperationID != "" && (expected == "" || op.ResumePlanHash != expected) {
		api.WriteProblem(w, projectRollbackUndoStarted(op.UndoOperationID))
		return
	}
	if op.Status == "blocked" || expected != "" {
		apps, problem := s.authorizeProjectRollbackResume(r, acct, op, "POST /v1/apps/{slug}/rollback")
		if problem != nil {
			if expected != "" && problem.Code == rollbackDeploymentChanged {
				problem = reviewedProjectRollbackResumeChanged()
			}
			api.WriteProblem(w, problem)
			return
		}
		switch {
		case expected != "" && op.ResumePlanHash == expected:
			// The same approval can be retried even if the attempt paused again.
		case op.Status == "blocked":
			if expected != "" {
				resumed, problem := s.resumeReviewedProjectRollback(r, store, acct, op, apps, expected)
				if problem != nil {
					api.WriteProblem(w, problem)
					return
				}
				op = resumed
			} else {
				resetProjectRollbackForResume(&op, time.Now().UTC(), true)
				op.ResumePlanHash = ""
				if err := store.ResumeProjectRollback(r.Context(), acct.ID, op); err != nil {
					latest, readErr := store.GetProjectRollback(r.Context(), acct.ID, op.ProjectID, op.ID)
					if readErr == nil && latest.UndoOperationID != "" {
						api.WriteProblem(w, projectRollbackUndoStarted(latest.UndoOperationID))
						return
					}
					if !errors.Is(err, state.ErrConflict) || readErr != nil || latest.Status == "blocked" {
						api.WriteProblem(w, projectRollbackWriteProblem(err))
						return
					}
					op = latest
				}
			}
		default:
			api.WriteProblem(w, reviewedProjectRollbackResumeChanged())
			return
		}
	}
	s.observeProjectRollbackTraffic(r.Context(), &op)
	s.observeProjectRollbackUndoArtifacts(r.Context(), op.ID, &op)
	writeJSON(w, http.StatusOK, op.ProjectRollbackOperation)
}

func (s *server) resumeReviewedProjectRollback(r *http.Request, store state.ProjectRollbackOperationStore, acct state.Account, op state.ProjectRollbackRecord, apps map[string]state.App, expected string) (state.ProjectRollbackRecord, *api.Problem) {
	plan, pins, problem := s.planProjectRollbackResume(r, op, apps)
	if problem == nil && plan.ResumePlanHash != expected {
		problem = reviewedProjectRollbackResumeChanged()
		for _, step := range plan.Steps {
			if step.Problem != nil && step.Problem.Status >= http.StatusInternalServerError {
				problem = step.Problem
				break
			}
		}
	}
	if problem == nil {
		resetProjectRollbackForResume(&op, time.Now().UTC(), true)
		op.ResumePlanHash = expected
		ctx := state.WithProjectRollbackResumeReview(r.Context(), pins)
		if err := store.ResumeProjectRollback(ctx, acct.ID, op); err != nil {
			problem = projectRollbackWriteProblem(err)
			if errors.Is(err, state.ErrProjectRollbackResumePlanChanged) {
				problem = reviewedProjectRollbackResumeChanged()
			}
		}
	}
	if problem != nil {
		latest, err := store.GetProjectRollback(r.Context(), acct.ID, op.ProjectID, op.ID)
		if err == nil {
			if latest.ResumePlanHash == expected {
				return latest, nil
			}
			if latest.UndoOperationID != "" {
				return latest, projectRollbackUndoStarted(latest.UndoOperationID)
			}
			if latest.Version != op.Version {
				return latest, reviewedProjectRollbackResumeChanged()
			}
		}
	}
	return op, problem
}

func (s *server) authorizeProjectRollbackResume(r *http.Request, acct state.Account, op state.ProjectRollbackRecord, route string) (map[string]state.App, *api.Problem) {
	apps := make(map[string]state.App, len(op.Steps))
	for _, step := range op.Steps {
		if step.Status == "skipped" {
			continue
		}
		app, err := s.store.AppByID(r.Context(), step.AppID)
		if err != nil && !errors.Is(err, state.ErrNotFound) {
			return nil, api.ErrCapacity("could not read the targeted service")
		}
		if app.ProjectID != op.ProjectID || app.Status == state.AppDeleted {
			return nil, projectRollbackChanged("a targeted service no longer belongs to this project")
		}
		if _, problem := appaccess.AuthorizeRoute(r, s.store, loadOrgAuditFrom(s.audit), actorAccount(r, acct), app, route); problem != nil {
			return nil, problem
		}
		apps[step.AppID] = app
	}
	return apps, nil
}

func (s *server) planProjectRollbackResume(r *http.Request, source state.ProjectRollbackRecord, apps map[string]state.App) (state.ProjectRollbackRecord, []state.ProjectRollbackResumePin, *api.Problem) {
	plan := source
	plan.Steps = append([]api.ProjectRollbackStep(nil), source.Steps...)
	resetProjectRollbackForResume(&plan, time.Time{}, false)
	plan.Status, plan.PlanHash, plan.ResumePlanHash, plan.PreparationDeadline = "planned", "", "", nil
	if plan.Phase != "" {
		plan.Phase = api.ProjectRollbackPreparing
	}
	pins := make([]state.ProjectRollbackResumePin, 0, len(plan.Steps))
	for i := range plan.Steps {
		step := &plan.Steps[i]
		if step.Status == "skipped" {
			continue
		}
		pin, problem := s.inspectProjectRollbackResumeStep(r, source, apps[step.AppID], step)
		if problem != nil {
			plan.Status = "blocked"
			step.Problem, step.Reason = problem, problem.Detail
			if step.Status == "planned" {
				step.Status = "blocked"
			}
			continue
		}
		pins = append(pins, pin)
	}
	if plan.Status == "planned" {
		hash, err := projectRollbackResumePlanHash(source, pins)
		if err != nil {
			return plan, nil, api.ErrCapacity("could not hash the rollback resume plan")
		}
		plan.ResumePlanHash = hash
	}
	return plan, pins, nil
}

func (s *server) inspectProjectRollbackResumeStep(r *http.Request, source state.ProjectRollbackRecord, app state.App, step *api.ProjectRollbackStep) (state.ProjectRollbackResumePin, *api.Problem) {
	var pin state.ProjectRollbackResumePin
	current, problem := s.projectRollbackCurrent(r.Context(), app.ID)
	if problem != nil {
		return pin, problem
	}
	if app.Slug != step.Slug || current.RolloutCompletedAt == nil {
		return pin, projectRollbackChanged("the targeted service or its production serving period changed")
	}
	pin = state.ProjectRollbackResumePin{
		AppID: app.ID, Slug: app.Slug, Scope: current.Scope, CurrentID: current.ID,
		CurrentServedAt: current.RolloutCompletedAt.UTC(),
	}
	if step.Status == "rolled_back" {
		store, ok := s.store.(state.ProjectRollbackUndoStore)
		if !ok {
			return pin, api.ErrCapacity("could not check the completed cutover's serving period")
		}
		if current.ID != step.TargetID || step.ReceiptID == "" {
			return pin, projectRollbackChanged("the revision restored by this cutover is no longer serving")
		}
		servedAt, err := store.ProjectRollbackUndoServingAt(r.Context(), source.ID, *step)
		if errors.Is(err, state.ErrNotFound) || err == nil && !sameProjectRollbackServingPeriod(current.RolloutCompletedAt, servedAt) {
			return pin, projectRollbackChanged("the completed cutover has a different production serving period")
		}
		if err != nil {
			return pin, api.ErrCapacity("could not check the completed cutover's serving period")
		}
		return pin, nil
	}
	if current.ID != step.CurrentID || current.Scope != step.Scope ||
		!sameProjectRollbackServingPeriod(current.RolloutCompletedAt, step.CurrentServedAt) {
		return pin, projectRollbackChanged("the targeted production serving revision or period changed after this operation was planned")
	}
	selection, warnings, problem := s.inspectProjectRollbackStep(r, app, *step, source.AcknowledgeReleases)
	step.Warnings = warnings
	step.CrossedReleases = projectRollbackCrossedReleaseDetails(selection.crossed)
	if problem != nil {
		return pin, problem
	}
	if projectRollbackReleaseReviewChanged(source.ReleaseReviews[app.ID], selection.crossed) {
		return pin, projectRollbackChanged("a release command started after the project rollback was reviewed")
	}
	pin.TargetID, pin.Releases = step.TargetID, canonicalProjectRollbackReleaseRuns(selection.crossed)
	return pin, nil
}

func resetProjectRollbackForResume(op *state.ProjectRollbackRecord, now time.Time, start bool) {
	for i := range op.Steps {
		step := &op.Steps[i]
		if step.Status == "skipped" {
			continue
		}
		if op.Phase != "" && projectRollbackPending(*step) || step.Status == "failed" {
			step.AttemptID, step.ReceiptID = "", ""
			if start {
				step.AttemptID = uuid.NewString()
			}
		}
		if op.Phase != "" && projectRollbackPending(*step) {
			step.Status, step.PreparationStatus, step.Blockers = "planned", "", nil
			if op.VerifyBeforeCutover && len(op.HealthProbes[step.AppID]) > 0 {
				step.PreCutoverHealth = &api.ProjectRollbackPreCutoverHealth{Status: "pending"}
			}
		}
		if step.Status == "blocked" || step.Status == "failed" {
			step.Status = "planned"
		}
		if step.Health != nil && !step.Health.PassedFor(op.StabilizeForSeconds) {
			step.Health = &api.ProjectRollbackHealth{Status: "pending"}
		}
		if step.LiveTraffic != nil && !step.LiveTraffic.Passed() {
			step.LiveTraffic = &api.ProjectRollbackLiveTraffic{Status: "pending"}
			if start && step.Status == "rolled_back" {
				step.LiveTraffic.ObserveAfter = &now
			}
		}
		step.Problem, step.Reason = nil, ""
	}
	op.PauseRequestedAt, op.AutoUndo = nil, nil
	if start {
		op.UpdatedAt, op.Status = &now, "running"
		if op.Phase != "" {
			beginProjectRollbackPreparation(op, now)
		}
	}
}

func reviewedProjectRollbackResumeChanged() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeProjectRollbackPlanChanged, "Reviewed resume plan changed",
		"Preview the resume again with --preview-resume and use the new plan hash.")
}
