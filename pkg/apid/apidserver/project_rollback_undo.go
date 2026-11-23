package apidserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

type projectRollbackUndoService struct {
	original api.ProjectRollbackStep
	app      state.App
}

func (s *server) projectRollbackUndoServices(ctx context.Context, source state.ProjectRollbackRecord) ([]projectRollbackUndoService, *api.Problem) {
	var services []projectRollbackUndoService
	for _, step := range slices.Backward(source.Steps) {
		if step.Status != "rolled_back" {
			continue
		}
		app, problem := s.projectRollbackApp(ctx, state.Project{ID: source.ProjectID}, step)
		if problem != nil {
			return nil, problem
		}
		services = append(services, projectRollbackUndoService{original: step, app: app})
	}
	if len(services) == 0 {
		return nil, projectRollbackUndoUnavailable("The paused rollback has no completed cutovers to undo.")
	}
	return services, nil
}

func newProjectRollbackUndoPlan(source state.ProjectRollbackRecord, acknowledge bool) state.ProjectRollbackRecord {
	return state.ProjectRollbackRecord{ProjectRollbackOperation: api.ProjectRollbackOperation{
		Project: source.Project, ProjectID: source.ProjectID, UndoOf: source.ID, AcknowledgeReleases: acknowledge,
	}, ReleaseReviews: make(map[string][]state.ReleaseRun)}
}

func (s *server) authorizeProjectRollbackUndo(r *http.Request, acct state.Account, source state.ProjectRollbackRecord, route string) ([]projectRollbackUndoService, *api.Problem) {
	if source.Status != "blocked" || source.UndoOf != "" {
		return nil, projectRollbackUndoUnavailable("Choose a paused project rollback that has switched at least one service.")
	}
	services, problem := s.projectRollbackUndoServices(r.Context(), source)
	if problem != nil {
		return nil, problem
	}
	for _, service := range services {
		if _, problem := appaccess.AuthorizeRoute(r, s.store, loadOrgAuditFrom(s.audit), actorAccount(r, acct), service.app, route); problem != nil {
			return nil, problem
		}
	}
	return services, nil
}

func (s *server) previewProjectRollbackUndo(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	_, source, ok := s.loadProjectRollback(w, r, acct)
	if !ok {
		return
	}
	var acknowledge bool
	if raw := r.URL.Query().Get("acknowledge_releases"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("acknowledge_releases must be a boolean"))
			return
		}
		acknowledge = value
	}
	undoStore, ok := s.store.(state.ProjectRollbackUndoStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project rollback undo is unavailable"))
		return
	}
	services, problem := s.authorizeProjectRollbackUndo(r, acct, source, "GET /v1/apps/{slug}")
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if source.UndoOperationID != "" {
		api.WriteProblem(w, projectRollbackUndoStarted(source.UndoOperationID))
		return
	}
	plan := newProjectRollbackUndoPlan(source, acknowledge)
	plan.Status = "planned"
	for _, service := range services {
		if problem := s.planProjectRollbackUndoStep(r, undoStore, source.ID, service.app, service.original, &plan); problem != nil {
			plan.Status = "blocked"
			step := &plan.Steps[len(plan.Steps)-1]
			step.Status, step.Reason, step.Problem = "blocked", problem.Detail, problem
		}
	}
	if plan.Status == "planned" {
		hash, err := projectRollbackPlanHash(plan, api.ProjectRollbackRequest{})
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not hash the rollback undo plan"))
			return
		}
		plan.PlanHash = hash
	}
	s.observeProjectRollbackUndoArtifacts(r.Context(), source.ID, &plan)
	writeJSON(w, http.StatusOK, plan.ProjectRollbackOperation)
}

func (s *server) undoProjectRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	operations, source, ok := s.loadProjectRollback(w, r, acct)
	if !ok {
		return
	}
	var req api.ProjectRollbackUndoRequest
	if err := decodeJSON(r, &req); err != nil && !errors.Is(err, io.EOF) {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if req.ExpectedPlanHash != "" && !api.ValidProjectRollbackPlanHash(req.ExpectedPlanHash) {
		api.WriteProblem(w, api.ErrValidation("expected_plan_hash must be a 64-character lowercase hexadecimal hash"))
		return
	}
	undoStore, ok := s.store.(state.ProjectRollbackUndoStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project rollback undo is unavailable"))
		return
	}
	services, problem := s.authorizeProjectRollbackUndo(r, acct, source, "POST /v1/apps/{slug}/rollback")
	if problem != nil {
		if req.ExpectedPlanHash != "" && (problem.Code == rollbackDeploymentChanged || problem.Code == api.CodeProjectRollbackUndoUnavailable) {
			problem = reviewedProjectRollbackUndoChanged()
		}
		api.WriteProblem(w, problem)
		return
	}
	plan := newProjectRollbackUndoPlan(source, req.AcknowledgeReleases)
	plan.PlanHash = req.ExpectedPlanHash
	if source.UndoOperationID == "" {
		for _, service := range services {
			problem = s.planProjectRollbackUndoStep(r, undoStore, source.ID, service.app, service.original, &plan)
			if problem != nil {
				break
			}
		}
		if req.ExpectedPlanHash != "" {
			if problem == nil {
				hash, err := projectRollbackPlanHash(plan, api.ProjectRollbackRequest{})
				switch {
				case err != nil:
					problem = api.ErrCapacity("could not hash the rollback undo plan")
				case hash != req.ExpectedPlanHash:
					problem = reviewedProjectRollbackUndoChanged()
				}
			} else if problem.Status == http.StatusConflict || problem.Status == http.StatusNotFound {
				problem = reviewedProjectRollbackUndoChanged()
			}
		}
		if problem != nil {
			// A concurrent acceptance may have changed production during preflight.
			latest, err := operations.GetProjectRollback(r.Context(), acct.ID, source.ProjectID, source.ID)
			if err != nil || latest.UndoOperationID == "" {
				api.WriteProblem(w, problem)
				return
			}
			source = latest
		}
	}
	if source.UndoOperationID == "" {
		initializeProjectRollback(&plan)
	}
	undo, err := undoStore.CreateProjectRollbackUndo(r.Context(), acct.ID, source, plan)
	if errors.Is(err, state.ErrProjectRollbackUndoPlanChanged) {
		api.WriteProblem(w, reviewedProjectRollbackUndoChanged())
		return
	}
	if err != nil {
		api.WriteProblem(w, projectRollbackWriteProblem(err))
		return
	}
	if s.audit != nil && undo.ID == plan.ID {
		s.audit.Emit(r.Context(), "project.rollback.undo_started", &acct.ID, projectaccess.AuditData(r.Context(), map[string]any{
			"project_id": source.ProjectID, "operation_id": undo.ID, "undo_of": source.ID, "acknowledge_releases": req.AcknowledgeReleases,
		}))
	}
	s.observeProjectRollbackTraffic(r.Context(), &undo)
	s.observeProjectRollbackUndoArtifacts(r.Context(), undo.ID, &undo)
	status := http.StatusAccepted
	if undo.Status != "running" {
		status = http.StatusOK
	}
	writeJSON(w, status, undo.ProjectRollbackOperation)
}

func (s *server) observeProjectRollbackUndoArtifacts(ctx context.Context, holder string, op *state.ProjectRollbackRecord) {
	store, ok := s.store.(state.ProjectRollbackUndoArtifactStore)
	if !ok || op.Status == "complete" {
		return
	}
	holds, err := store.ProjectRollbackUndoArtifactHolds(ctx, holder)
	if err != nil {
		s.log.Warn("read project rollback undo artifact holds", "operation", holder, "err", err)
		return
	}
	for i := range op.Steps {
		step := &op.Steps[i]
		deployment := step.CurrentID
		if op.UndoOf != "" {
			deployment = step.TargetID
		}
		if hold, ok := holds[deployment]; ok {
			step.UndoArtifacts = &hold
		}
	}
}

func (s *server) planProjectRollbackUndoStep(r *http.Request, store state.ProjectRollbackUndoStore, source string, app state.App, restored api.ProjectRollbackStep, undo *state.ProjectRollbackRecord) *api.Problem {
	undo.Steps = append(undo.Steps, api.ProjectRollbackStep{
		AppID: app.ID, Slug: restored.Slug, Service: restored.Service, Status: "planned",
		From: restored.To, To: restored.From, Scope: restored.Scope,
		CurrentID: restored.TargetID, TargetID: restored.CurrentID,
	})
	step := &undo.Steps[len(undo.Steps)-1]
	ctx := r.Context()
	current, problem := s.projectRollbackCurrent(ctx, app.ID)
	if problem != nil {
		return problem
	}
	if current.ID != restored.TargetID || restored.ReceiptID == "" {
		return projectRollbackChanged(fmt.Sprintf("%s is no longer serving the revision restored by this rollback.", restored.Slug))
	}
	servedAt, err := store.ProjectRollbackUndoServingAt(ctx, source, restored)
	if errors.Is(err, state.ErrNotFound) || err == nil && !sameProjectRollbackServingPeriod(current.RolloutCompletedAt, servedAt) {
		return projectRollbackChanged(fmt.Sprintf("%s has a different production serving period from the saved rollback.", restored.Slug))
	}
	if err != nil {
		return api.ErrCapacity("could not read the exact serving period for " + restored.Slug)
	}
	step.From, step.Scope, step.CurrentServedAt = current.Revision, current.Scope, servedAt
	selection, warnings, problem := s.inspectProjectRollbackStep(r, app, *step, undo.AcknowledgeReleases)
	step.CrossedReleases = projectRollbackCrossedReleaseDetails(selection.crossed)
	if problem != nil {
		return problem
	}
	step.To, step.Warnings = selection.target.Revision, warnings
	if len(selection.crossed) != 0 {
		undo.ReleaseReviews[app.ID] = selection.crossed
	}
	if mode := app.Manifest.ExecutionMode; mode == "" || mode == api.ExecutionModeRequest || mode == api.ExecutionModeService {
		undo.HTTPWorkloads = append(undo.HTTPWorkloads, app.ID)
	}
	return nil
}

func projectRollbackCrossedReleaseDetails(runs []state.ReleaseRun) []api.RollbackCrossedRelease {
	var details []api.RollbackCrossedRelease
	for _, run := range runs {
		details = append(details, api.RollbackCrossedRelease{
			DeploymentID: run.DeploymentID, Revision: run.Revision, Status: string(run.Status),
			StartedAt: run.StartedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	return details
}

func reviewedProjectRollbackUndoChanged() *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeProjectRollbackPlanChanged, "Reviewed undo plan changed",
		"Preview the undo again with --preview-undo and use the new plan hash.")
}

func projectRollbackUndoUnavailable(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeProjectRollbackUndoUnavailable, "Rollback undo unavailable", detail)
}

func projectRollbackUndoStarted(id string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeProjectRollbackUndoStarted, "Rollback undo already accepted", "Inspect or resume undo operation "+id+".")
}
