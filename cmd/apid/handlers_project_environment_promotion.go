package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectEnvironmentPromotionTokenWire is deliberately independent from the
// source-upload plan token. It is an opaque identity, not a bearer credential:
// execute and approval paths must recompute the current preview before using
// it.
type projectEnvironmentPromotionTokenWire struct {
	Version         int    `json:"v"`
	AccountID       string `json:"account_id"`
	ProjectID       string `json:"project_id"`
	ProjectSlug     string `json:"project_slug"`
	FromEnvironment string `json:"from_environment"`
	ToEnvironment   string `json:"to_environment"`
	FromConfigHash  string `json:"from_config_hash"`
	ToConfigHash    string `json:"to_config_hash"`
	PromotionHash   string `json:"promotion_hash"`
	IssuedAt        int64  `json:"issued_at"`
}

type projectEnvironmentPromotionPlan struct {
	ProjectID string
	Preview   api.ProjectEnvironmentPromotionPreviewResponse
	Apps      map[string]state.App
	Sources   map[string]state.Deployment
	Targets   map[string]state.Deployment
}

func (s *server) previewProjectEnvironmentPromotion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	projectSlug := r.PathValue("slug")
	fromEnvironment := strings.TrimSpace(r.URL.Query().Get("from"))
	toEnvironment := strings.TrimSpace(r.PathValue("environment"))
	plan, problem := s.buildProjectEnvironmentPromotionPlan(r.Context(), acct, projectSlug, fromEnvironment, toEnvironment)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, plan.Preview)
}

func (s *server) buildProjectEnvironmentPromotionPlan(ctx context.Context, acct state.Account, projectSlug, fromEnvironment, toEnvironment string) (projectEnvironmentPromotionPlan, *api.Problem) {
	plan := projectEnvironmentPromotionPlan{
		Apps:    make(map[string]state.App),
		Sources: make(map[string]state.Deployment),
		Targets: make(map[string]state.Deployment),
	}
	if fromEnvironment == "" {
		return plan, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Source environment required", "from is required")
	}
	if !api.ValidProjectEnvironmentSlug(fromEnvironment) || !api.ValidProjectEnvironmentSlug(toEnvironment) {
		return plan, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid environment", "source and target must be lowercase project environment slugs")
	}
	if fromEnvironment == toEnvironment {
		return plan, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Environments must differ", "source and target must be different project environments")
	}

	project, to, toConfig, problem := s.loadProjectEnvironmentConfig(ctx, acct, projectSlug, toEnvironment)
	if problem != nil {
		return plan, problem
	}
	plan.ProjectID = project.ID
	_, from, fromConfig, problem := s.loadProjectEnvironmentConfig(ctx, acct, projectSlug, fromEnvironment)
	if problem != nil {
		return plan, problem
	}
	configChanges, err := projectEnvironmentConfigDiff(fromConfig.Values, toConfig.Values)
	if err != nil {
		return plan, api.ErrInternal("could not compare environment configurations")
	}
	configDiff := api.ProjectEnvironmentConfigDiffResponse{
		ProjectSlug: project.Slug, FromEnvironment: from.Slug, ToEnvironment: to.Slug,
		FromVersion: fromConfig.Version, ToVersion: toConfig.Version,
		FromHash: configHashOrEmpty(fromConfig), ToHash: configHashOrEmpty(toConfig),
		Changes: configChanges,
	}

	apps, err := s.store.AppsForProject(ctx, acct.ID, project.ID)
	if err != nil {
		return plan, api.ErrCapacity("could not list project workloads")
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })

	changes := make([]api.ProjectEnvironmentPromotionChange, 0, len(apps))
	blockingReasons := make([]string, 0)
	for _, app := range apps {
		plan.Apps[app.Slug] = app
		source, sourceErr := s.store.LiveDeploymentForScope(ctx, app.ID, fromEnvironment)
		if sourceErr != nil && !errors.Is(sourceErr, state.ErrNotFound) {
			return plan, api.ErrCapacity("could not inspect source environment deployments")
		}
		target, targetErr := s.store.LiveDeploymentForScope(ctx, app.ID, toEnvironment)
		if targetErr != nil && !errors.Is(targetErr, state.ErrNotFound) {
			return plan, api.ErrCapacity("could not inspect target environment deployments")
		}
		if targetErr == nil {
			plan.Targets[app.Slug] = target
		}
		if sourceErr == nil {
			plan.Sources[app.Slug] = source
		}
		change := projectEnvironmentPromotionChange(app, source, sourceErr == nil, target, targetErr == nil)
		if change.Kind == "source_missing" {
			blockingReasons = append(blockingReasons,
				fmt.Sprintf("workload %q has no live deployment in %s", app.Slug, fromEnvironment))
		}
		changes = append(changes, change)
	}

	promotionHash, err := projectEnvironmentPromotionHash(project.Slug, from.Slug, to.Slug, configDiff, changes)
	if err != nil {
		return plan, api.ErrInternal("could not create promotion identity")
	}
	promotionToken, err := projectEnvironmentPromotionToken(acct.ID, project.ID, project.Slug,
		from.Slug, to.Slug, configDiff.FromHash, configDiff.ToHash, promotionHash)
	if err != nil {
		return plan, api.ErrInternal("could not create promotion token")
	}
	plan.Preview = api.ProjectEnvironmentPromotionPreviewResponse{
		ProjectSlug: project.Slug, FromEnvironment: from.Slug, ToEnvironment: to.Slug,
		ToEnvironmentProtected: to.Protected, ApprovalRequired: to.Protected,
		CanPromote: len(blockingReasons) == 0, BlockingReasons: blockingReasons,
		ConfigDiff: configDiff, Changes: changes,
		PromotionHash: promotionHash, PromotionToken: promotionToken,
	}
	return plan, nil
}

func (s *server) promoteProjectEnvironment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	projectSlug := r.PathValue("slug")
	toEnvironment := strings.TrimSpace(r.PathValue("environment"))
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 255 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Idempotency-Key required", "environment promotions require an Idempotency-Key of 1..255 characters"))
		return
	}
	var req api.PromoteProjectEnvironmentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	fromEnvironment := strings.TrimSpace(req.FromEnvironment)
	promotionToken := strings.TrimSpace(req.PromotionToken)
	if promotionToken == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Promotion token required", "promotion_token is required"))
		return
	}
	wire, err := decodeProjectEnvironmentPromotionToken(promotionToken)
	if err != nil || wire.AccountID != acct.ID || wire.ProjectSlug != projectSlug || wire.FromEnvironment != fromEnvironment || wire.ToEnvironment != toEnvironment {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Promotion token does not match", "preview the exact source and target environments again"))
		return
	}
	existing, existingWorkloads, lookupErr := s.store.ProjectEnvironmentPromotionByIdempotencyKey(r.Context(), acct.ID, projectSlug, idempotencyKey)
	if lookupErr != nil && !errors.Is(lookupErr, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrCapacity("could not load environment promotion"))
		return
	}
	if lookupErr == nil {
		if existing.ProjectID != wire.ProjectID || existing.FromEnvironment != fromEnvironment ||
			existing.ToEnvironment != toEnvironment || existing.PromotionHash != wire.PromotionHash {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Idempotency-Key already used", "use a new key for a different environment promotion"))
			return
		}
		if existing.Status == "succeeded" {
			writeJSON(w, http.StatusOK, projectEnvironmentPromotionResponse(existing, existingWorkloads))
			return
		}
		plan, problem := s.buildProjectEnvironmentPromotionPlan(r.Context(), acct, projectSlug, fromEnvironment, toEnvironment)
		if problem == nil {
			problem = validateProjectEnvironmentPromotionResume(wire, existing, existingWorkloads, plan)
		}
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		response, problem := s.executeProjectEnvironmentPromotion(r.Context(), acct, existing, existingWorkloads, plan)
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		writeJSON(w, http.StatusOK, response)
		return
	}
	plan, problem := s.buildProjectEnvironmentPromotionPlan(r.Context(), acct, projectSlug, fromEnvironment, toEnvironment)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if wire.ProjectID != plan.ProjectID || wire.FromConfigHash != plan.Preview.ConfigDiff.FromHash ||
		wire.ToConfigHash != plan.Preview.ConfigDiff.ToHash || wire.PromotionHash != plan.Preview.PromotionHash {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Promotion preview is stale", "the live releases or environment configuration changed; preview again"))
		return
	}
	if !plan.Preview.CanPromote {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Promotion is blocked", strings.Join(plan.Preview.BlockingReasons, "; ")))
		return
	}
	if problem := projectEnvironmentPromotionExecutionProblem(plan); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if plan.Preview.ApprovalRequired {
		approvalToken := strings.TrimSpace(req.ApprovalToken)
		if approvalToken == "" {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalRequired,
				"Protected environment approval required", "approve this exact promotion before executing it"))
			return
		}
		if _, err := s.store.ProjectEnvironmentApprovalByToken(r.Context(), acct.ID, projectSlug, toEnvironment,
			hashProjectEnvironmentApprovalMaterial(promotionToken), hashProjectEnvironmentApprovalMaterial(approvalToken)); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
				"Invalid environment approval", "the approval is expired or does not match this exact promotion"))
			return
		}
	}
	if problem := s.revalidateProjectEnvironmentPromotionPlan(r.Context(), acct, projectSlug, fromEnvironment, toEnvironment, plan); problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	promotion, workloads, err := s.store.CreateProjectEnvironmentPromotion(r.Context(), state.ProjectEnvironmentPromotion{
		AccountID: acct.ID, ProjectID: plan.ProjectID, ProjectSlug: projectSlug,
		FromEnvironment: fromEnvironment, ToEnvironment: toEnvironment,
		PromotionHash: plan.Preview.PromotionHash, IdempotencyKey: idempotencyKey, Status: "running",
		VerificationStatus: "pending",
	}, projectEnvironmentPromotionWorkloads(plan))
	if err != nil {
		if !errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrCapacity("could not create environment promotion"))
			return
		}
		promotion, workloads, err = s.store.ProjectEnvironmentPromotionByIdempotencyKey(r.Context(), acct.ID, projectSlug, idempotencyKey)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not load environment promotion"))
			return
		}
		if promotion.ProjectID != wire.ProjectID || promotion.FromEnvironment != fromEnvironment ||
			promotion.ToEnvironment != toEnvironment || promotion.PromotionHash != wire.PromotionHash {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Idempotency-Key already used", "use a new key for a different environment promotion"))
			return
		}
		if promotion.Status == "succeeded" {
			writeJSON(w, http.StatusOK, projectEnvironmentPromotionResponse(promotion, workloads))
			return
		}
		plan, problem = s.buildProjectEnvironmentPromotionPlan(r.Context(), acct, projectSlug, fromEnvironment, toEnvironment)
		if problem == nil {
			problem = validateProjectEnvironmentPromotionResume(wire, promotion, workloads, plan)
		}
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
	}
	response, problem := s.executeProjectEnvironmentPromotion(r.Context(), acct, promotion, workloads, plan)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (s *server) getProjectEnvironmentPromotionStatus(w http.ResponseWriter, r *http.Request, acct state.Account) {
	promotion, workloads, err := s.store.ProjectEnvironmentPromotionByID(r.Context(), acct.ID, r.PathValue("slug"), r.PathValue("environment"), r.PathValue("promotion"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Environment promotion not found", "no promotion exists with that id"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load environment promotion"))
		return
	}
	writeJSON(w, http.StatusOK, projectEnvironmentPromotionStatusResponse(promotion, workloads))
}

func (s *server) rollbackProjectEnvironmentPromotion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" || len(idempotencyKey) > 255 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Idempotency-Key required", "promotion rollbacks require an Idempotency-Key of 1..255 characters"))
		return
	}
	promotion, workloads, err := s.store.ProjectEnvironmentPromotionByID(r.Context(), acct.ID, r.PathValue("slug"), r.PathValue("environment"), r.PathValue("promotion"))
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Environment promotion not found", "no promotion exists with that id"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load environment promotion"))
		return
	}
	if promotion.Status == "running" {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Promotion is still running", "wait for the promotion to finish before requesting a rollback"))
		return
	}
	started, err := s.store.StartProjectEnvironmentPromotionRollback(r.Context(), acct.ID, promotion.ID, idempotencyKey)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Rollback idempotency key already used", "retry with the original rollback key or choose a new promotion"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not start environment promotion rollback"))
		return
	}
	if started.RollbackStatus == "rolled_back" {
		writeJSON(w, http.StatusOK, projectEnvironmentPromotionStatusResponse(started, workloads))
		return
	}

	if err := s.applyProjectEnvironmentPromotionRollback(r.Context(), acct, promotion, workloads); err != nil {
		_, _, loadErr := s.store.ProjectEnvironmentPromotionByID(r.Context(), acct.ID, promotion.ProjectSlug, promotion.ToEnvironment, promotion.ID)
		if loadErr != nil {
			api.WriteProblem(w, api.ErrCapacity("could not load environment rollback result"))
			return
		}
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Rollback refused", err.Error()))
		} else {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation, "Rollback failed", err.Error()))
		}
		return
	}
	completed, finalWorkloads, err := s.store.ProjectEnvironmentPromotionByID(r.Context(), acct.ID, promotion.ProjectSlug, promotion.ToEnvironment, promotion.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load environment rollback result"))
		return
	}
	s.audit.Emit(r.Context(), "project.environment.promotion_rolled_back", &acct.ID, map[string]any{
		"promotion_id": promotion.ID, "project_slug": promotion.ProjectSlug,
		"from_environment": promotion.FromEnvironment, "to_environment": promotion.ToEnvironment,
		"workload_count": len(finalWorkloads), "rollback_idempotency_key": idempotencyKey,
	})
	writeJSON(w, http.StatusOK, projectEnvironmentPromotionStatusResponse(completed, finalWorkloads))
}

func (s *server) applyProjectEnvironmentPromotionRollback(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload) error {
	apps, err := s.store.AppsForProject(ctx, acct.ID, promotion.ProjectID)
	if err != nil {
		return fmt.Errorf("could not list project workloads for rollback: %w", err)
	}
	appsBySlug := make(map[string]state.App, len(apps))
	for _, app := range apps {
		appsBySlug[app.Slug] = app
	}
	for _, workload := range workloads {
		if workload.RollbackStatus == "restored" || workload.RollbackStatus == "cleared" ||
			workload.RollbackStatus == "unchanged" || workload.RollbackStatus == "skipped" {
			continue
		}
		app, ok := appsBySlug[workload.WorkloadSlug]
		if !ok {
			return s.recordProjectEnvironmentPromotionRollbackFailure(ctx, acct, promotion, workload,
				errors.New("workload no longer belongs to the project"))
		}
		rollbackStatus, restoredID, rollbackErr := rollbackProjectEnvironmentPromotionWorkload(
			ctx, s.store, promotion, workload, app.ID)
		if rollbackErr != nil {
			return s.recordProjectEnvironmentPromotionRollbackFailure(ctx, acct, promotion, workload, rollbackErr)
		}
		if _, err := s.store.UpdateProjectEnvironmentPromotionRollbackWorkload(ctx, acct.ID, promotion.ID,
			workload.ID, rollbackStatus, restoredID, ""); err != nil {
			return fmt.Errorf("could not update environment rollback checkpoint: %w", err)
		}
	}

	now := time.Now().UTC()
	if _, err := s.store.UpdateProjectEnvironmentPromotionRollback(ctx, acct.ID, promotion.ID, "rolled_back", "", &now); err != nil {
		return fmt.Errorf("could not complete environment promotion rollback: %w", err)
	}
	return nil
}

func (s *server) recordProjectEnvironmentPromotionRollbackFailure(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, workload state.ProjectEnvironmentPromotionWorkload, cause error) error {
	message := cause.Error()
	if _, err := s.store.UpdateProjectEnvironmentPromotionRollbackWorkload(ctx, acct.ID, promotion.ID, workload.ID, "failed", "", message); err != nil {
		return fmt.Errorf("could not record environment rollback failure: %w", err)
	}
	now := time.Now().UTC()
	if _, err := s.store.UpdateProjectEnvironmentPromotionRollback(ctx, acct.ID, promotion.ID, "rollback_failed", message, &now); err != nil {
		return fmt.Errorf("could not complete environment rollback failure: %w", err)
	}
	return cause
}

func rollbackProjectEnvironmentPromotionWorkload(ctx context.Context, store state.Store, promotion state.ProjectEnvironmentPromotion, workload state.ProjectEnvironmentPromotionWorkload, appID string) (string, string, error) {
	current, currentErr := store.LiveDeploymentForScope(ctx, appID, promotion.ToEnvironment)
	if currentErr != nil && !errors.Is(currentErr, state.ErrNotFound) {
		return "", "", fmt.Errorf("could not inspect current target deployment: %w", currentErr)
	}
	hasCurrent := currentErr == nil
	previousID := workload.PreviousTargetDeploymentID
	marker := projectEnvironmentPromotionDeploymentReason(promotion.ID)

	var candidate state.Deployment
	if workload.TargetDeploymentID != "" && workload.TargetDeploymentID != previousID {
		candidate, currentErr = store.DeploymentByID(ctx, workload.TargetDeploymentID)
		if currentErr != nil {
			return "", "", fmt.Errorf("could not load promotion deployment: %w", currentErr)
		}
		if candidate.AppID != appID || candidate.Scope != promotion.ToEnvironment || candidate.Reason != marker {
			return "", "", fmt.Errorf("%w: recorded deployment is not owned by this promotion", state.ErrConflict)
		}
	} else if hasCurrent && current.Reason == marker {
		candidate = current
	}

	if previousID != "" {
		previous, err := store.DeploymentByID(ctx, previousID)
		if err != nil {
			return "", "", fmt.Errorf("could not load previous target deployment: %w", err)
		}
		if previous.AppID != appID || previous.Scope != promotion.ToEnvironment || previous.DeletedAt != nil {
			return "", "", fmt.Errorf("%w: previous target deployment is not a restorable target", state.ErrConflict)
		}
		if hasCurrent && current.ID == previous.ID {
			return "restored", previous.ID, nil
		}
		if candidate.ID == "" || !hasCurrent || current.ID != candidate.ID || candidate.Status != state.DeployLive {
			return "", "", fmt.Errorf("%w: target changed after promotion; refusing to overwrite it", state.ErrConflict)
		}
		if err := store.MarkDeploymentLive(ctx, previous.ID); err != nil {
			return "", "", fmt.Errorf("could not restore previous target deployment: %w", err)
		}
		return "restored", previous.ID, nil
	}

	if !hasCurrent {
		return "cleared", "", nil
	}
	if candidate.ID == "" || current.ID != candidate.ID || candidate.Status != state.DeployLive {
		return "", "", fmt.Errorf("%w: target changed after promotion; refusing to clear it", state.ErrConflict)
	}
	if err := store.UpdateDeploymentStatus(ctx, candidate.ID, state.DeployFailed, "environment promotion rollback"); err != nil {
		return "", "", fmt.Errorf("could not clear promotion deployment: %w", err)
	}
	return "cleared", "", nil
}

func projectEnvironmentPromotionWorkloads(plan projectEnvironmentPromotionPlan) []state.ProjectEnvironmentPromotionWorkload {
	workloads := make([]state.ProjectEnvironmentPromotionWorkload, 0, len(plan.Preview.Changes))
	for _, change := range plan.Preview.Changes {
		status := "pending"
		if change.Kind == "unchanged" {
			status = "unchanged"
		}
		workloads = append(workloads, state.ProjectEnvironmentPromotionWorkload{
			WorkloadSlug: change.WorkloadSlug, WorkloadName: change.WorkloadName,
			SourceDeploymentID:         change.SourceDeploymentID,
			PreviousTargetDeploymentID: change.TargetDeploymentID,
			TargetDeploymentID:         change.TargetDeploymentID, Status: status,
			VerificationStatus: "pending",
		})
	}
	return workloads
}

func validateProjectEnvironmentPromotionResume(wire projectEnvironmentPromotionTokenWire, promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload, plan projectEnvironmentPromotionPlan) *api.Problem {
	if wire.ProjectID != plan.ProjectID || wire.FromConfigHash != plan.Preview.ConfigDiff.FromHash || wire.ToConfigHash != plan.Preview.ConfigDiff.ToHash {
		return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Promotion configuration is stale", "the environment configuration changed; start a new promotion")
	}
	changes := make(map[string]api.ProjectEnvironmentPromotionChange, len(plan.Preview.Changes))
	for _, change := range plan.Preview.Changes {
		changes[change.WorkloadSlug] = change
	}
	for _, workload := range workloads {
		change, ok := changes[workload.WorkloadSlug]
		if !ok || change.SourceDeploymentID != workload.SourceDeploymentID {
			return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
				"Promotion source is stale", "a source workload changed; start a new promotion")
		}
		if workload.Status == "promoted" {
			if change.TargetDeploymentID != workload.TargetDeploymentID {
				return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
					"Promotion target changed", "the target workload changed after promotion; inspect the promotion before retrying")
			}
			continue
		}
		if target := plan.Targets[workload.WorkloadSlug]; target.ID != "" && target.Reason == projectEnvironmentPromotionDeploymentReason(promotion.ID) {
			continue
		}
		if change.TargetDeploymentID != workload.PreviousTargetDeploymentID {
			return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
				"Promotion target is stale", "the target workload changed; start a new promotion")
		}
	}
	return nil
}

func (s *server) executeProjectEnvironmentPromotion(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload, plan projectEnvironmentPromotionPlan) (api.ProjectEnvironmentPromotionResponse, *api.Problem) {
	if promotion.Status == "succeeded" {
		return projectEnvironmentPromotionResponse(promotion, workloads), nil
	}
	if _, err := s.store.UpdateProjectEnvironmentPromotion(ctx, acct.ID, promotion.ID, "running", "", nil); err != nil {
		return api.ProjectEnvironmentPromotionResponse{}, api.ErrCapacity("could not update environment promotion")
	}
	changes := make(map[string]api.ProjectEnvironmentPromotionChange, len(plan.Preview.Changes))
	for _, change := range plan.Preview.Changes {
		changes[change.WorkloadSlug] = change
	}
	for _, workload := range workloads {
		if workload.Status == "promoted" || workload.Status == "unchanged" {
			continue
		}
		change, ok := changes[workload.WorkloadSlug]
		if !ok {
			return s.failProjectEnvironmentPromotion(ctx, acct, promotion, workload, "workload is no longer in the promotion plan")
		}
		if existing, found, err := projectEnvironmentPromotionDeployment(ctx, s.store, plan.Apps[workload.WorkloadSlug].ID, promotion.ToEnvironment, promotion.ID); err != nil {
			return api.ProjectEnvironmentPromotionResponse{}, api.ErrCapacity("could not inspect environment promotion checkpoint")
		} else if found {
			updated, updateErr := s.store.UpdateProjectEnvironmentPromotionWorkload(ctx, acct.ID, promotion.ID, workload.ID, "promoted", existing.ID, "")
			if updateErr != nil {
				return api.ProjectEnvironmentPromotionResponse{}, api.ErrCapacity("could not update environment promotion checkpoint")
			}
			workload = updated
			continue
		}
		promoted, err := promoteProjectEnvironmentDeployment(ctx, s.store, plan.Sources[workload.WorkloadSlug], promotion.ToEnvironment, promotion.ID)
		if err != nil {
			return s.failProjectEnvironmentPromotion(ctx, acct, promotion, workload, "could not promote workload "+change.WorkloadSlug)
		}
		if _, err := s.store.UpdateProjectEnvironmentPromotionWorkload(ctx, acct.ID, promotion.ID, workload.ID, "promoted", promoted.ID, ""); err != nil {
			return api.ProjectEnvironmentPromotionResponse{}, api.ErrCapacity("could not update environment promotion checkpoint")
		}
	}

	verificationStarted := time.Now().UTC()
	if _, err := s.store.UpdateProjectEnvironmentPromotionVerification(ctx, acct.ID, promotion.ID,
		"verifying", "", &verificationStarted, nil); err != nil {
		return api.ProjectEnvironmentPromotionResponse{}, api.ErrCapacity("could not start environment promotion verification")
	}
	if err := s.verifyProjectEnvironmentPromotion(ctx, acct, promotion, plan); err != nil {
		verificationCompleted := time.Now().UTC()
		message := err.Error()
		_, _ = s.store.UpdateProjectEnvironmentPromotionVerification(ctx, acct.ID, promotion.ID,
			"failed", message, nil, &verificationCompleted)
		_, _ = s.store.UpdateProjectEnvironmentPromotion(ctx, acct.ID, promotion.ID, "failed", message, &verificationCompleted)
		if rollbackErr := s.autoRollbackProjectEnvironmentPromotion(ctx, acct, promotion); rollbackErr != nil {
			return api.ProjectEnvironmentPromotionResponse{}, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Promotion verification failed", message+"; automatic rollback failed: "+rollbackErr.Error())
		}
		return api.ProjectEnvironmentPromotionResponse{}, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Promotion verification failed", message+"; promotion was automatically rolled back")
	}
	now := time.Now().UTC()
	updated, err := s.store.UpdateProjectEnvironmentPromotion(ctx, acct.ID, promotion.ID, "succeeded", "", &now)
	if err != nil {
		return api.ProjectEnvironmentPromotionResponse{}, api.ErrCapacity("could not complete environment promotion")
	}
	_, finalWorkloads, err := s.store.ProjectEnvironmentPromotionByID(ctx, acct.ID, promotion.ProjectSlug, promotion.ToEnvironment, promotion.ID)
	if err != nil {
		return api.ProjectEnvironmentPromotionResponse{}, api.ErrCapacity("could not load environment promotion result")
	}
	s.audit.Emit(ctx, "project.environment.promoted", &acct.ID, map[string]any{
		"promotion_id": promotion.ID, "project_slug": promotion.ProjectSlug,
		"from_environment": promotion.FromEnvironment, "to_environment": promotion.ToEnvironment,
		"promotion_hash": promotion.PromotionHash, "workload_count": len(finalWorkloads),
	})
	return projectEnvironmentPromotionResponse(updated, finalWorkloads), nil
}

func (s *server) verifyProjectEnvironmentPromotion(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, plan projectEnvironmentPromotionPlan) error {
	_, workloads, err := s.store.ProjectEnvironmentPromotionByID(ctx, acct.ID, promotion.ProjectSlug, promotion.ToEnvironment, promotion.ID)
	if err != nil {
		return fmt.Errorf("could not load promotion verification checkpoints: %w", err)
	}
	for _, workload := range workloads {
		app, ok := plan.Apps[workload.WorkloadSlug]
		if !ok {
			return fmt.Errorf("workload %q is no longer in the project", workload.WorkloadSlug)
		}
		source, ok := plan.Sources[workload.WorkloadSlug]
		if !ok {
			return fmt.Errorf("workload %q has no source deployment", workload.WorkloadSlug)
		}
		if workload.TargetDeploymentID == "" {
			return fmt.Errorf("workload %q has no target deployment", workload.WorkloadSlug)
		}
		target, err := s.store.DeploymentByID(ctx, workload.TargetDeploymentID)
		if err != nil {
			return fmt.Errorf("could not load target deployment for workload %q: %w", workload.WorkloadSlug, err)
		}
		if target.AppID != app.ID || target.Scope != promotion.ToEnvironment || target.Status != state.DeployLive {
			return fmt.Errorf("workload %q target deployment is not live in %s", workload.WorkloadSlug, promotion.ToEnvironment)
		}
		if workload.Status == "promoted" && target.Reason != projectEnvironmentPromotionDeploymentReason(promotion.ID) {
			return fmt.Errorf("workload %q target deployment is not owned by this promotion", workload.WorkloadSlug)
		}
		if workload.Status == "promoted" && !sameProjectEnvironmentPromotionArtifact(source, target) {
			return fmt.Errorf("workload %q target artifact does not match the source release", workload.WorkloadSlug)
		}
		if _, err := s.store.UpdateProjectEnvironmentPromotionVerificationWorkload(ctx, acct.ID, promotion.ID,
			workload.ID, "verified", ""); err != nil {
			return fmt.Errorf("could not record verification for workload %q: %w", workload.WorkloadSlug, err)
		}
	}
	completed := time.Now().UTC()
	if _, err := s.store.UpdateProjectEnvironmentPromotionVerification(ctx, acct.ID, promotion.ID,
		"verified", "", nil, &completed); err != nil {
		return fmt.Errorf("could not complete environment promotion verification: %w", err)
	}
	return nil
}

func sameProjectEnvironmentPromotionArtifact(source, target state.Deployment) bool {
	if source.Kind != target.Kind || source.SourceSHA256 != target.SourceSHA256 ||
		source.ImageDigest != target.ImageDigest || source.CommitSHA != target.CommitSHA {
		return false
	}
	if source.RootfsKey != "" || target.RootfsKey != "" {
		return source.RootfsKey != "" && source.RootfsKey == target.RootfsKey && source.RootfsBytes == target.RootfsBytes
	}
	return source.RootfsPath != "" && source.RootfsPath == target.RootfsPath && source.RootfsBytes == target.RootfsBytes
}

func (s *server) autoRollbackProjectEnvironmentPromotion(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion) error {
	key := "auto-verification/" + promotion.ID
	started, err := s.store.StartProjectEnvironmentPromotionRollback(ctx, acct.ID, promotion.ID, key)
	if err != nil {
		return fmt.Errorf("could not start automatic rollback: %w", err)
	}
	if started.RollbackStatus == "rolled_back" {
		return nil
	}
	current, workloads, err := s.store.ProjectEnvironmentPromotionByID(ctx, acct.ID, promotion.ProjectSlug, promotion.ToEnvironment, promotion.ID)
	if err != nil {
		return fmt.Errorf("could not load automatic rollback state: %w", err)
	}
	if err := s.applyProjectEnvironmentPromotionRollback(ctx, acct, current, workloads); err != nil {
		return err
	}
	_, finalWorkloads, err := s.store.ProjectEnvironmentPromotionByID(ctx, acct.ID, promotion.ProjectSlug, promotion.ToEnvironment, promotion.ID)
	if err != nil {
		return fmt.Errorf("could not load automatic rollback result: %w", err)
	}
	s.audit.Emit(ctx, "project.environment.promotion_auto_rolled_back", &acct.ID, map[string]any{
		"promotion_id": promotion.ID, "project_slug": promotion.ProjectSlug,
		"from_environment": promotion.FromEnvironment, "to_environment": promotion.ToEnvironment,
		"workload_count": len(finalWorkloads), "reason": "verification_failed",
	})
	return nil
}

func (s *server) failProjectEnvironmentPromotion(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, workload state.ProjectEnvironmentPromotionWorkload, message string) (api.ProjectEnvironmentPromotionResponse, *api.Problem) {
	_, _ = s.store.UpdateProjectEnvironmentPromotionWorkload(ctx, acct.ID, promotion.ID, workload.ID, "failed", workload.TargetDeploymentID, message)
	_, _ = s.store.UpdateProjectEnvironmentPromotion(ctx, acct.ID, promotion.ID, "failed", message, nil)
	return api.ProjectEnvironmentPromotionResponse{}, api.ErrCapacity(message)
}

func projectEnvironmentPromotionResponse(promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload) api.ProjectEnvironmentPromotionResponse {
	results := make([]api.ProjectEnvironmentPromotionWorkloadResponse, 0, len(workloads))
	for _, workload := range workloads {
		results = append(results, api.ProjectEnvironmentPromotionWorkloadResponse{
			WorkloadSlug: workload.WorkloadSlug, WorkloadName: workload.WorkloadName,
			Status: workload.Status, SourceDeploymentID: workload.SourceDeploymentID,
			TargetDeploymentID: workload.TargetDeploymentID,
		})
	}
	return api.ProjectEnvironmentPromotionResponse{
		PromotionID: promotion.ID, ProjectSlug: promotion.ProjectSlug,
		FromEnvironment: promotion.FromEnvironment, ToEnvironment: promotion.ToEnvironment,
		PromotionHash: promotion.PromotionHash, Workloads: results,
	}
}

func projectEnvironmentPromotionStatusResponse(promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload) api.ProjectEnvironmentPromotionStatusResponse {
	items := make([]api.ProjectEnvironmentPromotionStatusWorkloadResponse, 0, len(workloads))
	for _, workload := range workloads {
		items = append(items, api.ProjectEnvironmentPromotionStatusWorkloadResponse{
			WorkloadSlug: workload.WorkloadSlug, WorkloadName: workload.WorkloadName,
			Status: workload.Status, SourceDeploymentID: workload.SourceDeploymentID,
			PreviousTargetDeploymentID: workload.PreviousTargetDeploymentID,
			TargetDeploymentID:         workload.TargetDeploymentID, Error: workload.Error,
			RollbackStatus: workload.RollbackStatus, RestoredTargetDeploymentID: workload.RestoredTargetDeploymentID,
			RollbackError: workload.RollbackError, VerificationStatus: workload.VerificationStatus,
			VerificationError: workload.VerificationError,
		})
	}
	return api.ProjectEnvironmentPromotionStatusResponse{
		PromotionID: promotion.ID, ProjectSlug: promotion.ProjectSlug,
		FromEnvironment: promotion.FromEnvironment, ToEnvironment: promotion.ToEnvironment,
		PromotionHash: promotion.PromotionHash, Status: promotion.Status, Error: promotion.Error,
		RollbackStatus: promotion.RollbackStatus, RollbackError: promotion.RollbackError,
		RollbackStartedAt:   formatOptionalTime(promotion.RollbackStartedAt),
		RollbackCompletedAt: formatOptionalTime(promotion.RollbackCompletedAt),
		VerificationStatus:  promotion.VerificationStatus, VerificationError: promotion.VerificationError,
		VerificationStartedAt:   formatOptionalTime(promotion.VerificationStartedAt),
		VerificationCompletedAt: formatOptionalTime(promotion.VerificationCompletedAt),
		CreatedAt:               promotion.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:               promotion.UpdatedAt.UTC().Format(time.RFC3339Nano),
		CompletedAt: func() string {
			if promotion.CompletedAt == nil {
				return ""
			}
			return promotion.CompletedAt.UTC().Format(time.RFC3339Nano)
		}(),
		Workloads: items,
	}
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func projectEnvironmentPromotionExecutionProblem(plan projectEnvironmentPromotionPlan) *api.Problem {
	for _, change := range plan.Preview.Changes {
		if change.Kind == "unchanged" {
			continue
		}
		source, ok := plan.Sources[change.WorkloadSlug]
		if !ok {
			return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
				"Promotion preview is stale", "a source workload disappeared; preview again")
		}
		if source.RootfsPath == "" && source.RootfsKey == "" {
			return api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Promotion artifact unavailable", fmt.Sprintf("workload %q has no immutable rootfs artifact", change.WorkloadSlug))
		}
		if source.CanaryTotalSteps > 0 || state.IsServiceRollout(source) {
			return api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Promotion is blocked", fmt.Sprintf("workload %q has an active rollout", change.WorkloadSlug))
		}
	}
	return nil
}

func (s *server) revalidateProjectEnvironmentPromotionPlan(ctx context.Context, acct state.Account, projectSlug, fromEnvironment, toEnvironment string, plan projectEnvironmentPromotionPlan) *api.Problem {
	current, problem := s.buildProjectEnvironmentPromotionPlan(ctx, acct, projectSlug, fromEnvironment, toEnvironment)
	if problem != nil {
		return problem
	}
	if current.Preview.PromotionHash != plan.Preview.PromotionHash {
		return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
			"Promotion preview is stale", "the live releases or environment configuration changed; preview again")
	}
	return nil
}

func projectEnvironmentPromotionDeploymentReason(promotionID string) string {
	return "environment promotion/" + promotionID
}

func projectEnvironmentPromotionDeployment(ctx context.Context, store state.Store, appID, targetEnvironment, promotionID string) (state.Deployment, bool, error) {
	deployments, err := store.ListDeploymentsForApp(ctx, appID, 0, 0)
	if err != nil {
		return state.Deployment{}, false, err
	}
	reason := projectEnvironmentPromotionDeploymentReason(promotionID)
	for _, deployment := range deployments {
		if deployment.Scope == targetEnvironment && deployment.Reason == reason && deployment.Status == state.DeployLive {
			return deployment, true, nil
		}
	}
	return state.Deployment{}, false, nil
}

func promoteProjectEnvironmentDeployment(ctx context.Context, store state.Store, source state.Deployment, targetEnvironment, promotionID string) (state.Deployment, error) {
	rootfsPath, rootfsKey, rootfsBytes := source.RootfsPath, source.RootfsKey, source.RootfsBytes
	candidate := source
	candidate.ID = ""
	candidate.BuildID = ""
	candidate.Scope = targetEnvironment
	candidate.SourcePath = ""
	candidate.SourceRoot = ""
	candidate.SourceBytes = 0
	candidate.LogPath = ""
	candidate.RootfsPath = ""
	candidate.RootfsKey = ""
	candidate.RootfsBytes = 0
	candidate.Status = state.DeployPending
	candidate.Error = ""
	candidate.ErrorCode = ""
	candidate.ErrorHint = ""
	candidate.ErrorWhy = ""
	candidate.ErrorFix = ""
	candidate.ErrorRelevantLogs = nil
	candidate.CreatedAt = time.Time{}
	candidate.TrafficPercent = 100
	candidate.TrafficPercentExplicit = false
	candidate.RolloutState = "pending"
	candidate.RolloutStartedAt = nil
	candidate.RolloutCompletedAt = nil
	candidate.RolloutAbortedAt = nil
	candidate.RolloutAbortedReason = ""
	candidate.CanaryStep = 0
	candidate.CanaryTotalSteps = 0
	candidate.CanaryStepStartedAt = nil
	candidate.CanaryStages = nil
	candidate.StageState = nil
	candidate.DeployedVia = "api"
	candidate.Reason = projectEnvironmentPromotionDeploymentReason(promotionID)

	created, err := store.CreateDeployment(ctx, candidate)
	if err != nil {
		return state.Deployment{}, err
	}
	if err := store.SetDeploymentRootfs(ctx, created.ID, rootfsPath, rootfsKey, rootfsBytes); err != nil {
		return state.Deployment{}, err
	}
	layers, err := store.ListDeploymentSidecarLayers(ctx, source.ID)
	if err != nil {
		return state.Deployment{}, err
	}
	for _, layer := range layers {
		layer.DeploymentID = created.ID
		layer.CreatedAt = time.Time{}
		layer.UpdatedAt = time.Time{}
		if _, err := store.SetDeploymentSidecarLayer(ctx, layer); err != nil {
			return state.Deployment{}, err
		}
	}
	if err := store.MarkDeploymentLive(ctx, created.ID); err != nil {
		return state.Deployment{}, err
	}
	return store.DeploymentByID(ctx, created.ID)
}

func projectEnvironmentPromotionChange(app state.App, source state.Deployment, hasSource bool, target state.Deployment, hasTarget bool) api.ProjectEnvironmentPromotionChange {
	change := api.ProjectEnvironmentPromotionChange{
		WorkloadSlug: app.Slug, WorkloadName: app.WorkloadName,
	}
	if hasSource {
		change.SourceDeploymentID = source.ID
		change.SourceBuildID = source.BuildID
		change.SourceRevisionKind, change.SourceRevision = deploymentRevision(source)
	}
	if hasTarget {
		change.TargetDeploymentID = target.ID
		change.TargetBuildID = target.BuildID
		change.TargetRevisionKind, change.TargetRevision = deploymentRevision(target)
	}
	switch {
	case !hasSource:
		change.Kind = "source_missing"
	case !hasTarget:
		change.Kind = "create"
	case change.SourceRevisionKind == change.TargetRevisionKind && change.SourceRevision == change.TargetRevision:
		change.Kind = "unchanged"
	default:
		change.Kind = "update"
	}
	return change
}

func deploymentRevision(deployment state.Deployment) (kind, value string) {
	switch {
	case deployment.SourceSHA256 != "":
		return "source_sha256", deployment.SourceSHA256
	case deployment.ImageDigest != "":
		return "image_digest", deployment.ImageDigest
	case deployment.CommitSHA != "":
		return "commit_sha", deployment.CommitSHA
	default:
		return "deployment_id", deployment.ID
	}
}

func projectEnvironmentPromotionHash(projectSlug, fromEnvironment, toEnvironment string, configDiff api.ProjectEnvironmentConfigDiffResponse, changes []api.ProjectEnvironmentPromotionChange) (string, error) {
	identity := struct {
		ProjectSlug     string                                  `json:"project_slug"`
		FromEnvironment string                                  `json:"from_environment"`
		ToEnvironment   string                                  `json:"to_environment"`
		FromConfigHash  string                                  `json:"from_config_hash"`
		ToConfigHash    string                                  `json:"to_config_hash"`
		Changes         []api.ProjectEnvironmentPromotionChange `json:"changes"`
	}{
		ProjectSlug: projectSlug, FromEnvironment: fromEnvironment, ToEnvironment: toEnvironment,
		FromConfigHash: configDiff.FromHash, ToConfigHash: configDiff.ToHash, Changes: changes,
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func projectEnvironmentPromotionToken(accountID, projectID, projectSlug, fromEnvironment, toEnvironment, fromConfigHash, toConfigHash, promotionHash string) (string, error) {
	wire := projectEnvironmentPromotionTokenWire{
		Version: 1, AccountID: accountID, ProjectID: projectID, ProjectSlug: projectSlug,
		FromEnvironment: fromEnvironment, ToEnvironment: toEnvironment,
		FromConfigHash: fromConfigHash, ToConfigHash: toConfigHash,
		PromotionHash: promotionHash, IssuedAt: timeNow().UTC().Unix(),
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeProjectEnvironmentPromotionToken(encoded string) (projectEnvironmentPromotionTokenWire, error) {
	var wire projectEnvironmentPromotionTokenWire
	if strings.TrimSpace(encoded) == "" {
		return wire, errors.New("empty promotion token")
	}
	var raw []byte
	var err error
	for _, encoding := range []*base64.Encoding{
		base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding,
	} {
		raw, err = encoding.DecodeString(encoded)
		if err == nil {
			break
		}
	}
	if err != nil {
		return wire, fmt.Errorf("promotion token base64: %w", err)
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return wire, fmt.Errorf("promotion token json: %w", err)
	}
	if wire.Version != 1 || wire.AccountID == "" || wire.ProjectID == "" || wire.ProjectSlug == "" ||
		wire.FromEnvironment == "" || wire.ToEnvironment == "" || wire.PromotionHash == "" {
		return wire, errors.New("promotion token is incomplete")
	}
	return wire, nil
}
