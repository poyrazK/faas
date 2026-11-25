package apidserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/projectaccess"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) previewProjectRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	var req api.ProjectRollbackRequest
	query := r.URL.Query()
	req.ToDeploy = query.Get("to_deploy")
	for key, field := range map[string]*bool{
		"all": &req.All, "acknowledge_releases": &req.AcknowledgeReleases, "verify": &req.Verify,
		"undo_on_health_failure": &req.UndoOnHealthFailure, "verify_before_cutover": &req.VerifyBeforeCutover,
		"verify_live_traffic": &req.VerifyLiveTraffic, "undo_on_live_traffic_failure": &req.UndoOnLiveTrafficFailure,
	} {
		if raw := query.Get(key); raw != "" {
			value, err := strconv.ParseBool(raw)
			if err != nil {
				api.WriteProblem(w, api.ErrValidation(key+" must be a boolean"))
				return
			}
			*field = value
		}
	}
	if query.Has("stabilize_for_seconds") {
		seconds, err := strconv.Atoi(query.Get("stabilize_for_seconds"))
		if err != nil {
			api.WriteProblem(w, api.ErrValidation("stabilize_for_seconds must be an integer"))
			return
		}
		req.StabilizeForSeconds = seconds
	}
	if query.Has("to_deploy") && req.ToDeploy == "" {
		api.WriteProblem(w, api.ErrValidation("to_deploy must be a non-zero deploy UUID"))
		return
	}
	if problem := normalizeProjectRollbackTarget(&req); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	plan, problem := s.planProjectRollback(r, acct, project, req, false)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, plan.ProjectRollbackOperation)
}

func (s *server) startProjectRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// Keep the original body for older callers using the generic replay
	// middleware. A durable start uses its own key and always reads live state.
	var body bytes.Buffer
	legacyKey := r.Header.Get("Idempotency-Key") != ""
	if legacyKey && r.Body != nil {
		r.Body = struct {
			io.Reader
			io.Closer
		}{io.TeeReader(r.Body, &body), r.Body}
	}
	var req api.ProjectRollbackRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if req.ExpectedPlanHash != "" && !api.ValidProjectRollbackPlanHash(req.ExpectedPlanHash) {
		api.WriteProblem(w, api.ErrValidation("expected_plan_hash must be a 64-character lowercase hexadecimal hash"))
		return
	}
	if problem := normalizeProjectRollbackTarget(&req); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if req.RequestID == "" && legacyKey {
		r.Body = io.NopCloser(bytes.NewReader(body.Bytes()))
		s.idempotent(func(w http.ResponseWriter, r *http.Request, acct state.Account) {
			s.admitProjectRollbackStart(w, r, acct, req, nil)
		})(w, r, acct)
		return
	}
	var starts state.ProjectRollbackStartStore
	if req.RequestID != "" {
		id, err := uuid.Parse(req.RequestID)
		if err != nil || id == uuid.Nil {
			api.WriteProblem(w, api.ErrValidation("request_id must be a non-zero UUID"))
			return
		}
		req.RequestID = id.String()
		var ok bool
		starts, ok = s.store.(state.ProjectRollbackStartStore)
		if !ok {
			api.WriteProblem(w, api.ErrCapacity("durable project rollback starts are unavailable"))
			return
		}
	}
	s.admitProjectRollbackStart(w, r, acct, req, starts)
}

func (s *server) admitProjectRollbackStart(w http.ResponseWriter, r *http.Request, acct state.Account, req api.ProjectRollbackRequest, starts state.ProjectRollbackStartStore) {
	project, ok := s.loadProject(w, r, acct)
	if !ok || s.replayProjectRollbackStart(w, r, starts, project, req) {
		return
	}
	plan, problem := s.preflightProjectRollbackStart(r, acct, project, req)
	if problem != nil || plan.Status != "planned" {
		// A concurrent retry may have admitted this request during preflight.
		if s.replayProjectRollbackStart(w, r, starts, project, req) {
			return
		}
		if problem != nil {
			api.WriteProblem(w, problem)
		} else {
			writeJSON(w, http.StatusOK, plan.ProjectRollbackOperation)
		}
		return
	}
	store, ok := s.store.(state.ProjectRollbackOperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project rollback operations are unavailable"))
		return
	}
	initializeProjectRollback(&plan)
	created := true
	var err error
	if starts != nil {
		plan, created, err = starts.CreateProjectRollbackStart(r.Context(), project.AccountID, plan, req)
	} else {
		err = store.CreateProjectRollback(r.Context(), project.AccountID, plan)
	}
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			if problem := s.activeProjectRollbackConflict(r.Context(), project); problem != nil {
				api.WriteProblem(w, problem)
				return
			}
		}
		api.WriteProblem(w, projectRollbackWriteProblem(err))
		return
	}
	if created && s.audit != nil {
		s.audit.Emit(r.Context(), "project.rollback.started", &acct.ID, projectaccess.AuditData(r.Context(), map[string]any{
			"project_id": plan.ProjectID, "operation_id": plan.ID, "request_id": plan.RequestID, "plan_hash": plan.PlanHash,
			"acknowledge_releases": plan.AcknowledgeReleases, "verify": plan.Verify,
		}))
	}
	if !created {
		s.observeProjectRollbackTraffic(r.Context(), &plan)
	}
	writeProjectRollbackStart(w, plan)
}

func (s *server) preflightProjectRollbackStart(r *http.Request, acct state.Account, project state.Project, req api.ProjectRollbackRequest) (state.ProjectRollbackRecord, *api.Problem) {
	if problem := s.activeProjectRollbackConflict(r.Context(), project); problem != nil {
		return state.ProjectRollbackRecord{}, problem
	}
	plan, problem := s.planProjectRollback(r, acct, project, req, true)
	if (problem == nil && plan.Status != "planned") || (problem != nil && problem.Status == http.StatusConflict) {
		// A concurrent start may have progressed far enough to block preflight.
		if conflict := s.activeProjectRollbackConflict(r.Context(), project); conflict != nil {
			return plan, conflict
		}
	}
	if req.ExpectedPlanHash != "" {
		mismatch := problem == nil && req.ExpectedPlanHash != plan.PlanHash
		unavailable := problem != nil && (problem.Code == rollbackDeploymentChanged || problem.Code == api.CodeProjectRollbackProbeRequired ||
			problem.Code == api.CodeProjectRollbackLiveTrafficRequired)
		if mismatch || unavailable {
			return plan, api.NewProblem(http.StatusConflict, api.CodeProjectRollbackPlanChanged,
				"Reviewed rollback plan changed", "Run a fresh rollback preview with the same options and review its plan before retrying.")
		}
	}
	if problem == nil && plan.Status == "planned" && plan.Verify && !s.projectRollbackHealthAvailable() {
		problem = api.ErrCapacity("project rollback health verification is unavailable")
	}
	return plan, problem
}

func (s *server) replayProjectRollbackStart(w http.ResponseWriter, r *http.Request, starts state.ProjectRollbackStartStore, project state.Project, req api.ProjectRollbackRequest) bool {
	if starts == nil {
		return false
	}
	op, err := starts.GetProjectRollbackStart(r.Context(), project.AccountID, project.ID, req)
	if errors.Is(err, state.ErrNotFound) {
		return false
	}
	if err != nil {
		api.WriteProblem(w, projectRollbackWriteProblem(err))
		return true
	}
	s.observeProjectRollbackTraffic(r.Context(), &op)
	writeProjectRollbackStart(w, op)
	return true
}

func writeProjectRollbackStart(w http.ResponseWriter, op state.ProjectRollbackRecord) {
	status := http.StatusOK
	if op.Status == "running" {
		status = http.StatusAccepted
	}
	writeJSON(w, status, op.ProjectRollbackOperation)
}

func initializeProjectRollback(plan *state.ProjectRollbackRecord) {
	now := time.Now().UTC()
	plan.ID, plan.Status, plan.CreatedAt, plan.UpdatedAt = uuid.NewString(), "running", &now, &now
	beginProjectRollbackPreparation(plan, now)
	for i := range plan.Steps {
		if plan.Steps[i].Status == "planned" {
			plan.Steps[i].AttemptID = uuid.NewString()
		}
	}
}

func (s *server) getProjectRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	_, op, ok := s.loadProjectRollback(w, r, acct)
	if ok {
		s.observeProjectRollbackTraffic(r.Context(), &op)
		s.observeProjectRollbackUndoArtifacts(r.Context(), op.ID, &op)
		writeJSON(w, http.StatusOK, op.ProjectRollbackOperation)
	}
}

func (s *server) pauseProjectRollback(w http.ResponseWriter, r *http.Request, acct state.Account) {
	_, op, ok := s.loadProjectRollback(w, r, acct)
	if !ok {
		return
	}
	store, ok := s.store.(state.ProjectRollbackPauseStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project rollback pause is unavailable"))
		return
	}
	for _, step := range op.Steps {
		if !projectRollbackPending(step) {
			continue
		}
		app, err := s.store.AppByID(r.Context(), step.AppID)
		if errors.Is(err, state.ErrNotFound) {
			continue
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not read the targeted service"))
			return
		}
		// A removed service cannot take part in future project cutovers.
		if app.ProjectID != op.ProjectID || app.Status == state.AppDeleted {
			continue
		}
		if _, problem := appaccess.AuthorizeRoute(r, s.store, loadOrgAuditFrom(s.audit), actorAccount(r, acct), app, "POST /v1/apps/{slug}/rollback"); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	paused, changed, err := store.PauseProjectRollback(r.Context(), acct.ID, op.ProjectID, op.ID)
	if err != nil {
		if errors.Is(err, state.ErrConflict) && paused.Status == "complete" {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, "project_rollback_already_complete", "Rollback already complete", "This project rollback has already finished."))
		} else {
			api.WriteProblem(w, projectRollbackWriteProblem(err))
		}
		return
	}
	if changed && s.audit != nil {
		s.audit.Emit(r.Context(), "project.rollback.pause_requested", &acct.ID, projectaccess.AuditData(r.Context(), map[string]any{
			"project_id": paused.ProjectID, "operation_id": paused.ID, "undo_of": paused.UndoOf,
		}))
	}
	s.observeProjectRollbackTraffic(r.Context(), &paused)
	s.observeProjectRollbackUndoArtifacts(r.Context(), paused.ID, &paused)
	status := http.StatusAccepted
	if paused.Status == "blocked" {
		status = http.StatusOK
	}
	writeJSON(w, status, paused.ProjectRollbackOperation)
}

func (s *server) loadProjectRollback(w http.ResponseWriter, r *http.Request, acct state.Account) (state.ProjectRollbackOperationStore, state.ProjectRollbackRecord, bool) {
	var empty state.ProjectRollbackRecord
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return nil, empty, false
	}
	id, err := uuid.Parse(r.PathValue("operation"))
	if err != nil || id == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation("operation must be a project rollback UUID"))
		return nil, empty, false
	}
	store, ok := s.store.(state.ProjectRollbackOperationStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project rollback operations are unavailable"))
		return nil, empty, false
	}
	op, err := store.GetProjectRollback(r.Context(), project.AccountID, project.ID, id.String())
	if err != nil {
		api.WriteProblem(w, projectRollbackWriteProblem(err))
		return nil, empty, false
	}
	return store, op, true
}

func projectRollbackWriteProblem(err error) *api.Problem {
	switch {
	case errors.Is(err, state.ErrProjectRollbackRequestReused):
		return api.NewProblem(http.StatusConflict, api.CodeProjectRollbackRequestReused, "Rollback request ID already used",
			"This request ID was accepted with different rollback options.")
	case errors.Is(err, state.ErrConflict):
		return api.NewProblem(http.StatusConflict, api.CodeProjectRollbackConflict, "Project rollback conflict", "A project rollback is already running or the operation changed. Refresh its status before retrying.")
	case errors.Is(err, state.ErrNotFound):
		return api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Rollback not found", "No such rollback for this project.")
	default:
		return api.ErrCapacity("could not record project rollback progress")
	}
}

const rollbackDeploymentChanged = "rollback_deployment_changed"

func projectRollbackChanged(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, rollbackDeploymentChanged, "Rollback selection changed", detail)
}

func (s *server) planProjectRollback(r *http.Request, acct state.Account, project state.Project, req api.ProjectRollbackRequest, write bool) (state.ProjectRollbackRecord, *api.Problem) {
	var plan state.ProjectRollbackRecord
	if req.UndoOnLiveTrafficFailure && !req.VerifyLiveTraffic {
		return plan, api.ErrValidation("undo_on_live_traffic_failure requires verify_live_traffic")
	}
	if req.VerifyLiveTraffic && !req.Verify {
		return plan, api.ErrValidation("verify_live_traffic requires verify")
	}
	if req.VerifyBeforeCutover && !req.Verify {
		return plan, api.ErrValidation("verify_before_cutover requires verify")
	}
	if req.StabilizeForSeconds < 0 || req.StabilizeForSeconds > api.ProjectRollbackMaxStabilizeSeconds {
		return plan, api.ErrValidation("stabilize_for_seconds must be between 0 and 60")
	}
	if req.StabilizeForSeconds > 0 && !req.Verify {
		return plan, api.ErrValidation("stabilize_for_seconds requires verify")
	}
	if req.UndoOnHealthFailure && !req.Verify {
		return plan, api.ErrValidation("undo_on_health_failure requires verify")
	}
	ctx := r.Context()
	var liveTraffic state.ProjectRollbackLiveTrafficStore
	if req.VerifyLiveTraffic {
		var ok bool
		liveTraffic, ok = s.store.(state.ProjectRollbackLiveTrafficStore)
		if !ok {
			return plan, api.ErrCapacity("project rollback live-traffic verification is unavailable")
		}
		owner, err := s.store.AccountByID(ctx, project.AccountID)
		if err != nil {
			return plan, api.ErrCapacity("could not read the project account")
		}
		if !owner.Plan.DebugTelemetryEnabled() {
			return plan, api.NewProblem(http.StatusConflict, api.CodeProjectRollbackLiveTrafficRequired,
				"Request telemetry required", "Live-traffic verification requires request telemetry on the project account.")
		}
	}
	apps, err := s.store.AppsForProject(ctx, project.AccountID, project.ID)
	if err != nil {
		return plan, api.ErrCapacity("could not list project workloads")
	}
	plan.Project, plan.ProjectID, plan.Status, plan.AcknowledgeReleases = project.Slug, project.ID, "complete", req.AcknowledgeReleases
	plan.Verify, plan.VerifyBeforeCutover = req.Verify, req.VerifyBeforeCutover
	plan.UndoOnHealthFailure, plan.UndoOnLiveTrafficFailure = req.UndoOnHealthFailure, req.UndoOnLiveTrafficFailure
	plan.StabilizeForSeconds, plan.VerifyLiveTraffic = req.StabilizeForSeconds, req.VerifyLiveTraffic
	plan.ReleaseReviews = make(map[string][]state.ReleaseRun)
	plan.Steps = make([]api.ProjectRollbackStep, 0, len(apps))
	members := make(map[string]api.ProjectRollbackMember)
	if !req.All {
		batch, problem := s.projectRollbackBatch(ctx, project, req.ToDeploy)
		if problem != nil {
			return plan, problem
		}
		plan.DeployGroupID = batch.ID
		for _, member := range batch.Members {
			if member.Slug == "" || members[member.Slug].DeploymentID != "" {
				return plan, projectRollbackChanged("the deploy contains a detached service or multiple revisions of one service")
			}
			members[member.Slug] = member
		}
	}
	for _, app := range projectRollbackOrder(apps) {
		step := api.ProjectRollbackStep{Service: app.WorkloadName, Slug: app.Slug, AppID: app.ID, Status: "planned"}
		member, selected := members[app.Slug]
		if !selected && !req.All {
			step.Status, step.Reason = "skipped", "outside the recorded deploy"
		} else {
			delete(members, app.Slug)
			method := "GET /v1/apps/{slug}"
			if write {
				method = "POST /v1/apps/{slug}/rollback"
			}
			_, problem := appaccess.AuthorizeRoute(r, s.store, loadOrgAuditFrom(s.audit), actorAccount(r, acct), app, method)
			if problem != nil {
				return plan, problem
			}
			if req.ToDeploy != "" {
				problem = s.planProjectRollbackTargetStep(r, app, member, &plan, &step)
			} else {
				problem = s.planProjectRollbackStep(r, app, member, &plan, &step, req.All)
			}
			if problem != nil {
				step.Status, step.Problem, step.Reason = "blocked", problem, problem.Detail
				plan.Status = "blocked"
			}
		}
		if step.Status == "planned" {
			if plan.Status == "complete" {
				plan.Status = "planned"
			}
			if mode := app.Manifest.ExecutionMode; mode == "" || mode == api.ExecutionModeRequest || mode == api.ExecutionModeService {
				plan.HTTPWorkloads = append(plan.HTTPWorkloads, app.ID)
				if liveTraffic != nil {
					if problem := pinProjectRollbackLiveTraffic(ctx, liveTraffic, app, &plan, &step); problem != nil {
						return plan, problem
					}
				}
				if plan.Verify {
					probes, problem := s.projectRollbackProbes(ctx, app)
					if problem != nil {
						return plan, problem
					}
					if len(probes) > 0 {
						if plan.HealthProbes == nil {
							plan.HealthProbes = make(map[string][]api.ProjectReleaseProbe)
						}
						plan.HealthProbes[app.ID] = probes
						step.Health = &api.ProjectRollbackHealth{Status: "pending"}
						if plan.VerifyBeforeCutover {
							step.PreCutoverHealth = &api.ProjectRollbackPreCutoverHealth{Status: "pending"}
						}
					}
				}
			}
		}
		plan.Steps = append(plan.Steps, step)
	}
	if len(members) != 0 || len(plan.Steps) == 0 {
		return plan, projectRollbackChanged("the recorded services no longer match the project")
	}
	if plan.Verify && plan.Status == "planned" && len(plan.HealthProbes) == 0 {
		return plan, api.NewProblem(http.StatusConflict, api.CodeProjectRollbackProbeRequired, "Health probe required", "Configure at least one health probe on a targeted HTTP service to verify this rollback.")
	}
	if plan.VerifyLiveTraffic && plan.Status == "planned" && len(plan.LiveTrafficPolicies) == 0 {
		return plan, api.NewProblem(http.StatusConflict, api.CodeProjectRollbackLiveTrafficRequired,
			"Production error budget required", "Enable a production route monitor with a 5xx budget on at least one targeted HTTP service.")
	}
	if plan.Status == "planned" {
		var err error
		plan.PlanHash, err = projectRollbackPlanHash(plan, req)
		if err != nil {
			return plan, api.ErrCapacity("could not identify the project rollback plan")
		}
	}
	return plan, nil
}

func (s *server) planProjectRollbackStep(r *http.Request, app state.App, member api.ProjectRollbackMember, plan *state.ProjectRollbackRecord, step *api.ProjectRollbackStep, all bool) *api.Problem {
	if !all {
		switch member.Status {
		case "failed", "cancelled", "superseded":
			if !member.PreviouslyServed {
				step.Status, step.Reason = "skipped", "this revision never served"
				return nil
			}
		case "live":
		default:
			return projectRollbackChanged(fmt.Sprintf("recorded revision v%d is still %s", member.Revision, member.Status))
		}
	}
	ctx := r.Context()
	current, problem := s.projectRollbackCurrent(ctx, app.ID)
	if problem != nil {
		return problem
	}
	step.From, step.CurrentID, step.Scope, step.CurrentServedAt = current.Revision, current.ID, current.Scope, current.RolloutCompletedAt
	if !all {
		switch {
		case current.ID != "" && current.ID == member.PredecessorID:
			step.Status, step.Reason = "skipped", "recorded predecessor already serves"
			return nil
		case current.ID != member.DeploymentID:
			return projectRollbackChanged(fmt.Sprintf("serving revision changed since the recorded deploy (expected v%d)", member.Revision))
		case member.PredecessorID == "" && member.Revision == 1:
			step.Status, step.Reason = "skipped", "first release has no predecessor"
			return nil
		case member.PredecessorID == "":
			return projectRollbackChanged(fmt.Sprintf("v%d has no recorded serving predecessor", member.Revision))
		}
	} else if current.ID == "" {
		step.Status, step.Reason = "skipped", "no serving release"
		return nil
	}
	target, revision, err := s.rollbackTarget(ctx, app.ID, current)
	if err != nil {
		return api.ErrCapacity("could not read the serving revision's rollback target")
	}
	if !all && target != member.PredecessorID {
		return projectRollbackChanged(fmt.Sprintf("recorded predecessor v%d is no longer the serving revision's rollback target", member.PredecessorRevision))
	}
	if target == "" || all && revision > step.From {
		step.Status, step.Reason = "skipped", "no earlier rollback target"
		return nil
	}
	step.TargetID, step.To = target, revision
	return s.checkProjectRollbackPlannedStep(r, app, plan, step)
}

func (s *server) checkProjectRollbackPlannedStep(r *http.Request, app state.App, plan *state.ProjectRollbackRecord, step *api.ProjectRollbackStep) *api.Problem {
	selection, warnings, problem := s.inspectProjectRollbackStep(r, app, *step, plan.AcknowledgeReleases)
	if problem != nil {
		return problem
	}
	step.Warnings = warnings
	if len(selection.crossed) != 0 {
		plan.ReleaseReviews[app.ID] = selection.crossed
	}
	return nil
}

func (s *server) inspectProjectRollbackStep(r *http.Request, app state.App, step api.ProjectRollbackStep, acknowledge bool) (checkedRollbackSelection, []api.BindingCheckFinding, *api.Problem) {
	selection, problem := s.inspectCheckedRollback(r.Context(), app, api.RollbackRequest{
		TargetDeploymentID: &step.TargetID, ExpectedCurrentDeploymentID: &step.CurrentID, AcknowledgeReleases: acknowledge,
	})
	if problem != nil {
		return selection, nil, problem
	}
	warnings, problem := s.checkRollbackPreflight(r, app, selection)
	return selection, warnings, problem
}

func (s *server) projectRollbackCurrent(ctx context.Context, app string) (state.Deployment, *api.Problem) {
	rows, err := s.store.LiveDeployments(ctx, app)
	if err != nil {
		return state.Deployment{}, api.ErrCapacity("could not read serving deployments")
	}
	var current state.Deployment
	for _, d := range rows {
		if (d.Scope != "" && d.Scope != api.DefaultEnvScope && d.Scope != "production") ||
			d.DeletedAt != nil || d.Status != state.DeployLive || d.TrafficPercent == 0 {
			continue
		}
		if current.ID != "" || d.TrafficPercent != 100 || d.RolloutState != "complete" {
			return state.Deployment{}, projectRollbackChanged("production does not have a single completed serving revision")
		}
		current = d
		if current.Scope == "" {
			current.Scope = api.DefaultEnvScope
		}
	}
	return current, nil
}

// Callers roll back before the services they depend on.
func projectRollbackOrder(apps []state.App) []state.App {
	apps = slices.DeleteFunc(slices.Clone(apps), func(app state.App) bool { return app.Slug == "" || app.Status == state.AppDeleted })
	sort.Slice(apps, func(i, j int) bool { return apps[i].WorkloadName < apps[j].WorkloadName })
	placed := make([]bool, len(apps))
	out := make([]state.App, 0, len(apps))
	for len(out) < len(apps) {
		before := len(out)
		for i, app := range apps {
			if placed[i] {
				continue
			}
			hasCaller := false
			for j, other := range apps {
				if i != j && !placed[j] && slices.ContainsFunc(other.Manifest.ServiceBindings, func(binding api.AppServiceBinding) bool {
					return api.SameServiceName(app.WorkloadName, binding.Service)
				}) {
					hasCaller = true
					break
				}
			}
			if !hasCaller {
				placed[i] = true
				out = append(out, app)
			}
		}
		if before == len(out) {
			for i, app := range apps {
				if !placed[i] {
					out = append(out, app)
				}
			}
		}
	}
	return out
}
