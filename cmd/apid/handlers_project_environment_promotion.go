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
	"strconv"
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
	Version          int    `json:"v"`
	AccountID        string `json:"account_id"`
	ProjectID        string `json:"project_id"`
	ProjectSlug      string `json:"project_slug"`
	FromEnvironment  string `json:"from_environment"`
	ToEnvironment    string `json:"to_environment"`
	FromConfigHash   string `json:"from_config_hash"`
	ToConfigHash     string `json:"to_config_hash"`
	FromReleaseSetID string `json:"from_release_set_id,omitempty"`
	ToReleaseSetID   string `json:"to_release_set_id,omitempty"`
	SyncConfig       bool   `json:"sync_config,omitempty"`
	PromotionHash    string `json:"promotion_hash"`
	IssuedAt         int64  `json:"issued_at"`
}

type projectEnvironmentPromotionPlan struct {
	ProjectID             string
	Preview               api.ProjectEnvironmentPromotionPreviewResponse
	Apps                  map[string]state.App
	Sources               map[string]state.Deployment
	Targets               map[string]state.Deployment
	FromReleaseSet        *state.ProjectReleaseSet
	ToReleaseSet          *state.ProjectReleaseSet
	ReleaseGraphMode      bool
	ReleaseTTLSeconds     int
	FallbackTargetMembers []state.ProjectReleaseMember
	SyncConfig            bool
	SourceConfig          state.ProjectEnvironmentConfig
	TargetConfig          state.ProjectEnvironmentConfig
}

func (s *server) previewProjectEnvironmentPromotion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	projectSlug := r.PathValue("slug")
	fromEnvironment := strings.TrimSpace(r.URL.Query().Get("from"))
	toEnvironment := strings.TrimSpace(r.PathValue("environment"))
	syncConfig, err := parseProjectEnvironmentPromotionSyncConfig(r.URL.Query().Get("sync_config"))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid config sync option", "sync_config must be true or false"))
		return
	}
	plan, problem := s.buildProjectEnvironmentPromotionPlan(r.Context(), acct, projectSlug, fromEnvironment, toEnvironment, syncConfig)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, plan.Preview)
}

func parseProjectEnvironmentPromotionSyncConfig(raw string) (bool, error) {
	if strings.TrimSpace(raw) == "" {
		return false, nil
	}
	return strconv.ParseBool(strings.TrimSpace(raw))
}

func (s *server) buildProjectEnvironmentPromotionPlan(ctx context.Context, acct state.Account, projectSlug, fromEnvironment, toEnvironment string, syncConfig bool) (projectEnvironmentPromotionPlan, *api.Problem) {
	plan := projectEnvironmentPromotionPlan{
		Apps:       make(map[string]state.App),
		Sources:    make(map[string]state.Deployment),
		Targets:    make(map[string]state.Deployment),
		SyncConfig: syncConfig,
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
	plan.SourceConfig, plan.TargetConfig = fromConfig, toConfig
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
	fromReleaseSet, problem := s.activeProjectEnvironmentReleaseSet(ctx, acct.ID, project.ID, fromEnvironment)
	if problem != nil {
		return plan, problem
	}
	toReleaseSet, problem := s.activeProjectEnvironmentReleaseSet(ctx, acct.ID, project.ID, toEnvironment)
	if problem != nil {
		return plan, problem
	}
	if fromReleaseSet.ID != "" {
		plan.FromReleaseSet = &fromReleaseSet
	}
	if toReleaseSet.ID != "" {
		plan.ToReleaseSet = &toReleaseSet
	}
	if syncConfig && toReleaseSet.ID == "" {
		blockingReasons = append(blockingReasons, "configuration sync requires an active target release graph so config and workload cutover can commit atomically")
	}
	plan.ReleaseGraphMode = plan.FromReleaseSet != nil || plan.ToReleaseSet != nil
	if plan.ReleaseGraphMode {
		switch {
		case plan.FromReleaseSet != nil && plan.ToReleaseSet != nil:
			plan.ReleaseTTLSeconds = min(plan.FromReleaseSet.TTLSeconds, plan.ToReleaseSet.TTLSeconds)
		case plan.FromReleaseSet != nil:
			plan.ReleaseTTLSeconds = plan.FromReleaseSet.TTLSeconds
		case plan.ToReleaseSet != nil:
			plan.ReleaseTTLSeconds = plan.ToReleaseSet.TTLSeconds
		}
		if plan.ReleaseTTLSeconds <= 0 || plan.ReleaseTTLSeconds > api.RevisionPinMaxTTLSeconds {
			blockingReasons = append(blockingReasons, "release graph TTL is outside the supported revision-pin window")
		}
	}
	for _, app := range apps {
		plan.Apps[app.Slug] = app
		if plan.ReleaseGraphMode && app.Manifest.RevisionPinTTLSeconds < plan.ReleaseTTLSeconds {
			blockingReasons = append(blockingReasons, fmt.Sprintf(
				"workload %q revision pin TTL is shorter than the promoted release graph TTL", app.Slug))
		}
		source, sourceErr := projectEnvironmentPromotionSelectedDeployment(ctx, s.store, app, fromEnvironment, plan.FromReleaseSet)
		if sourceErr != nil && !errors.Is(sourceErr, state.ErrNotFound) {
			return plan, api.ErrCapacity("could not inspect source environment deployments")
		}
		target, targetErr := projectEnvironmentPromotionSelectedDeployment(ctx, s.store, app, toEnvironment, plan.ToReleaseSet)
		if targetErr != nil && !errors.Is(targetErr, state.ErrNotFound) {
			return plan, api.ErrCapacity("could not inspect target environment deployments")
		}
		if targetErr == nil {
			plan.Targets[app.Slug] = target
		}
		if sourceErr == nil {
			plan.Sources[app.Slug] = source
		}
		if plan.ReleaseGraphMode && plan.FromReleaseSet == nil {
			fallback, reason, routeErr := projectEnvironmentPromotionFallbackRoute(ctx, s.store, app, fromEnvironment)
			if routeErr != nil {
				return plan, api.ErrCapacity("could not inspect source environment routes")
			}
			if reason != "" {
				blockingReasons = append(blockingReasons, reason)
			} else if fallback.ID == "" {
				blockingReasons = append(blockingReasons, fmt.Sprintf(
					"workload %q has no 100%% weighted source route in %s; publish a source release set first",
					app.Slug, fromEnvironment))
			}
		}
		if plan.ReleaseGraphMode && plan.ToReleaseSet == nil {
			fallback, reason, routeErr := projectEnvironmentPromotionFallbackRoute(ctx, s.store, app, toEnvironment)
			if routeErr != nil {
				return plan, api.ErrCapacity("could not inspect target environment routes")
			}
			if reason != "" {
				blockingReasons = append(blockingReasons, reason)
			} else if fallback.ID != "" {
				plan.FallbackTargetMembers = append(plan.FallbackTargetMembers,
					state.ProjectReleaseMember{AppID: app.ID, DeploymentID: fallback.ID})
			}
		}
		change := projectEnvironmentPromotionChange(app, source, sourceErr == nil, target, targetErr == nil)
		if change.Kind == "source_missing" {
			blockingReasons = append(blockingReasons,
				fmt.Sprintf("workload %q has no live deployment in %s", app.Slug, fromEnvironment))
		}
		changes = append(changes, change)
	}

	promotionHash, err := projectEnvironmentPromotionHash(project.Slug, from.Slug, to.Slug, configDiff, syncConfig,
		fromReleaseSet.ID, toReleaseSet.ID, changes)
	if err != nil {
		return plan, api.ErrInternal("could not create promotion identity")
	}
	promotionToken, err := projectEnvironmentPromotionToken(acct.ID, project.ID, project.Slug,
		from.Slug, to.Slug, configDiff.FromHash, configDiff.ToHash,
		fromReleaseSet.ID, toReleaseSet.ID, syncConfig, promotionHash)
	if err != nil {
		return plan, api.ErrInternal("could not create promotion token")
	}
	plan.Preview = api.ProjectEnvironmentPromotionPreviewResponse{
		ProjectSlug: project.Slug, FromEnvironment: from.Slug, ToEnvironment: to.Slug,
		SyncConfig:             syncConfig,
		ToEnvironmentProtected: to.Protected, ApprovalRequired: to.Protected,
		CanPromote: len(blockingReasons) == 0, BlockingReasons: blockingReasons,
		ConfigDiff: configDiff, Changes: changes,
		FromReleaseSet:   projectEnvironmentPromotionReleaseSetResponse(plan.FromReleaseSet),
		ToReleaseSet:     projectEnvironmentPromotionReleaseSetResponse(plan.ToReleaseSet),
		ReleaseGraphMode: plan.ReleaseGraphMode, ReleaseTTLSeconds: plan.ReleaseTTLSeconds,
		PromotionHash: promotionHash, PromotionToken: promotionToken,
	}
	return plan, nil
}

func (s *server) activeProjectEnvironmentReleaseSet(ctx context.Context, accountID, projectID, environment string) (state.ProjectReleaseSet, *api.Problem) {
	reader, ok := s.store.(state.ProjectReleaseSetReader)
	if !ok {
		return state.ProjectReleaseSet{}, api.ErrCapacity("project release inventory is unavailable")
	}
	release, err := reader.ActiveProjectReleaseSet(ctx, accountID, projectID, environment)
	if errors.Is(err, state.ErrNotFound) {
		return state.ProjectReleaseSet{}, nil
	}
	if err != nil {
		return state.ProjectReleaseSet{}, api.ErrCapacity("could not inspect project release graph")
	}
	return release, nil
}

func projectEnvironmentPromotionReleaseSetResponse(release *state.ProjectReleaseSet) *api.ProjectReleaseSetResponse {
	if release == nil {
		return nil
	}
	response := projectReleaseSetResponse(*release)
	return &response
}

func projectEnvironmentPromotionSelectedDeployment(ctx context.Context, store state.Store, app state.App, environment string, release *state.ProjectReleaseSet) (state.Deployment, error) {
	if release == nil {
		return store.LiveDeploymentForScope(ctx, app.ID, environment)
	}
	for _, member := range release.Members {
		if member.AppID != app.ID {
			continue
		}
		deployment, err := store.DeploymentByID(ctx, member.DeploymentID)
		if err != nil {
			return state.Deployment{}, err
		}
		if deployment.AppID != app.ID || deployment.Scope != environment || deployment.Status != state.DeployLive {
			return state.Deployment{}, state.ErrConflict
		}
		return deployment, nil
	}
	return state.Deployment{}, state.ErrConflict
}

func (s *server) buildProjectEnvironmentPromotionResumePlan(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload) (projectEnvironmentPromotionPlan, *api.Problem) {
	plan := projectEnvironmentPromotionPlan{
		ProjectID: promotion.ProjectID, Apps: make(map[string]state.App),
		Sources: make(map[string]state.Deployment), Targets: make(map[string]state.Deployment),
		ReleaseGraphMode: promotion.ReleaseGraphMode, ReleaseTTLSeconds: promotion.ReleaseTTLSeconds,
		SyncConfig: promotion.SyncConfig,
	}
	project, to, toConfig, problem := s.loadProjectEnvironmentConfig(ctx, acct, promotion.ProjectSlug, promotion.ToEnvironment)
	if problem != nil {
		return plan, problem
	}
	if project.ID != promotion.ProjectID {
		return plan, promotionResumeConflict("project identity changed; start a new promotion")
	}
	_, from, fromConfig, problem := s.loadProjectEnvironmentConfig(ctx, acct, promotion.ProjectSlug, promotion.FromEnvironment)
	if problem != nil {
		return plan, problem
	}
	plan.SourceConfig, plan.TargetConfig = fromConfig, toConfig
	configChanges, err := projectEnvironmentConfigDiff(fromConfig.Values, toConfig.Values)
	if err != nil {
		return plan, api.ErrInternal("could not compare environment configurations")
	}
	configDiff := api.ProjectEnvironmentConfigDiffResponse{
		ProjectSlug: project.Slug, FromEnvironment: from.Slug, ToEnvironment: to.Slug,
		FromVersion: fromConfig.Version, ToVersion: toConfig.Version,
		FromHash: configHashOrEmpty(fromConfig), ToHash: configHashOrEmpty(toConfig), Changes: configChanges,
	}
	reader, ok := s.store.(state.ProjectReleaseSetReader)
	if !ok && promotion.ReleaseGraphMode {
		return plan, api.ErrCapacity("project release inventory is unavailable")
	}
	if promotion.SourceReleaseSetID != "" {
		release, err := reader.ProjectReleaseSetByID(ctx, acct.ID, project.ID, from.Slug, promotion.SourceReleaseSetID)
		if err != nil {
			return plan, promotionResumeConflict("source release graph is no longer available; start a new promotion")
		}
		plan.FromReleaseSet = &release
	}
	if promotion.PreviousTargetReleaseSetID != "" {
		release, err := reader.ProjectReleaseSetByID(ctx, acct.ID, project.ID, to.Slug, promotion.PreviousTargetReleaseSetID)
		if err != nil {
			return plan, promotionResumeConflict("previous target release graph is no longer available; inspect the promotion")
		}
		plan.ToReleaseSet = &release
	}
	apps, err := s.store.AppsForProject(ctx, acct.ID, project.ID)
	if err != nil {
		return plan, api.ErrCapacity("could not list project workloads")
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })
	workloadBySlug := make(map[string]state.ProjectEnvironmentPromotionWorkload, len(workloads))
	for _, workload := range workloads {
		if _, exists := workloadBySlug[workload.WorkloadSlug]; exists {
			return plan, promotionResumeConflict("promotion has duplicate workload checkpoints")
		}
		workloadBySlug[workload.WorkloadSlug] = workload
	}
	if len(workloadBySlug) != len(apps) {
		return plan, promotionResumeConflict("project workloads changed after the promotion started")
	}
	changes := make([]api.ProjectEnvironmentPromotionChange, 0, len(apps))
	for _, app := range apps {
		workload, exists := workloadBySlug[app.Slug]
		if !exists {
			return plan, promotionResumeConflict("project workloads changed after the promotion started")
		}
		plan.Apps[app.Slug] = app
		source, err := s.store.DeploymentByID(ctx, workload.SourceDeploymentID)
		if err != nil || source.AppID != app.ID || source.Scope != from.Slug {
			return plan, promotionResumeConflict("a source deployment is no longer available; inspect the promotion")
		}
		plan.Sources[app.Slug] = source
		var previousTarget state.Deployment
		hasPreviousTarget := workload.PreviousTargetDeploymentID != ""
		if hasPreviousTarget {
			previousTarget, err = s.store.DeploymentByID(ctx, workload.PreviousTargetDeploymentID)
			if err != nil || previousTarget.AppID != app.ID || previousTarget.Scope != to.Slug {
				return plan, promotionResumeConflict("a previous target deployment is no longer available; inspect the promotion")
			}
		}
		if hasPreviousTarget {
			plan.Targets[app.Slug] = previousTarget
		}
		if promotion.ReleaseGraphMode {
			if staged, found, err := projectEnvironmentPromotionDeployment(ctx, s.store, app.ID, to.Slug, promotion.ID); err != nil {
				return plan, api.ErrCapacity("could not inspect environment promotion checkpoint")
			} else if found {
				if !sameProjectEnvironmentPromotionArtifact(source, staged) {
					return plan, promotionResumeConflict("a staged deployment no longer matches the source release")
				}
				plan.Targets[app.Slug] = staged
				if workload.Status == "promoted" && workload.TargetDeploymentID != staged.ID {
					return plan, promotionResumeConflict("a staged workload checkpoint changed; inspect the promotion")
				}
			} else if workload.Status == "promoted" {
				return plan, promotionResumeConflict("a promoted workload deployment is no longer available")
			}
		}
		changes = append(changes, projectEnvironmentPromotionChange(app, source, true, previousTarget, hasPreviousTarget))
		if plan.ReleaseGraphMode && plan.ToReleaseSet == nil && workload.PreviousTargetTrafficPercent == 100 {
			plan.FallbackTargetMembers = append(plan.FallbackTargetMembers,
				state.ProjectReleaseMember{AppID: app.ID, DeploymentID: workload.PreviousTargetDeploymentID})
		}
	}
	promotionHash, err := projectEnvironmentPromotionHash(project.Slug, from.Slug, to.Slug, configDiff, promotion.SyncConfig,
		promotion.SourceReleaseSetID, promotion.PreviousTargetReleaseSetID, changes)
	if err != nil {
		return plan, api.ErrInternal("could not recreate promotion identity")
	}
	if promotionHash != promotion.PromotionHash {
		return plan, promotionResumeConflict("the original promotion snapshot no longer matches; inspect the promotion")
	}
	plan.Preview = api.ProjectEnvironmentPromotionPreviewResponse{
		ProjectSlug: project.Slug, FromEnvironment: from.Slug, ToEnvironment: to.Slug,
		SyncConfig:             promotion.SyncConfig,
		ToEnvironmentProtected: to.Protected, ApprovalRequired: to.Protected, CanPromote: true,
		ConfigDiff: configDiff, Changes: changes,
		FromReleaseSet:   projectEnvironmentPromotionReleaseSetResponse(plan.FromReleaseSet),
		ToReleaseSet:     projectEnvironmentPromotionReleaseSetResponse(plan.ToReleaseSet),
		ReleaseGraphMode: plan.ReleaseGraphMode, ReleaseTTLSeconds: plan.ReleaseTTLSeconds,
		PromotionHash: promotionHash,
	}
	return plan, nil
}

func promotionResumeConflict(detail string) *api.Problem {
	return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
		"Promotion snapshot is stale", detail)
}

func projectEnvironmentPromotionFallbackRoute(ctx context.Context, store state.Store, app state.App, environment string) (state.Deployment, string, error) {
	deployments, err := store.LiveDeployments(ctx, app.ID)
	if err != nil {
		return state.Deployment{}, "", err
	}
	var weighted []state.Deployment
	for _, deployment := range deployments {
		if deployment.Scope == environment && deployment.Status == state.DeployLive && deployment.TrafficPercent > 0 {
			weighted = append(weighted, deployment)
		}
	}
	if len(weighted) == 0 {
		return state.Deployment{}, "", nil
	}
	if len(weighted) != 1 || weighted[0].TrafficPercent != 100 {
		return state.Deployment{}, fmt.Sprintf(
			"workload %q has a split weighted route in %s; graph promotion requires a single 100%% fallback route",
			app.Slug, environment), nil
	}
	return weighted[0], "", nil
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
		var plan projectEnvironmentPromotionPlan
		var problem *api.Problem
		if existing.ReleaseGraphMode {
			plan, problem = s.buildProjectEnvironmentPromotionResumePlan(r.Context(), acct, existing, existingWorkloads)
		} else {
			plan, problem = s.buildProjectEnvironmentPromotionPlan(r.Context(), acct, projectSlug, fromEnvironment, toEnvironment, wire.SyncConfig)
		}
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
	plan, problem := s.buildProjectEnvironmentPromotionPlan(r.Context(), acct, projectSlug, fromEnvironment, toEnvironment, wire.SyncConfig)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if wire.ProjectID != plan.ProjectID || wire.SyncConfig != plan.SyncConfig || wire.FromConfigHash != plan.Preview.ConfigDiff.FromHash ||
		wire.ToConfigHash != plan.Preview.ConfigDiff.ToHash ||
		wire.FromReleaseSetID != projectEnvironmentPromotionReleaseSetID(plan.FromReleaseSet) ||
		wire.ToReleaseSetID != projectEnvironmentPromotionReleaseSetID(plan.ToReleaseSet) ||
		wire.PromotionHash != plan.Preview.PromotionHash {
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
	if problem := s.revalidateProjectEnvironmentPromotionPlan(r.Context(), acct, projectSlug, fromEnvironment, toEnvironment, plan); problem != nil {
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
		approval, err := s.store.ConsumeProjectEnvironmentApproval(r.Context(), acct.ID, projectSlug, toEnvironment,
			hashProjectEnvironmentApprovalMaterial(promotionToken), hashProjectEnvironmentApprovalMaterial(approvalToken), time.Now().UTC())
		if err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
				"Invalid environment approval", "the approval is expired or does not match this exact promotion"))
			return
		}
		s.audit.Emit(r.Context(), "project.environment.approval.consumed", &acct.ID, map[string]any{
			"project_slug": projectSlug,
			"environment":  toEnvironment,
			"approval_id":  approval.ID,
			"token_kind":   approval.TokenKind,
			"consumed_at":  approval.ConsumedAt.UTC().Format(time.RFC3339),
		})
	}
	promotionRecord := state.ProjectEnvironmentPromotion{
		AccountID: acct.ID, ProjectID: plan.ProjectID, ProjectSlug: projectSlug,
		FromEnvironment: fromEnvironment, ToEnvironment: toEnvironment,
		PromotionHash: plan.Preview.PromotionHash, IdempotencyKey: idempotencyKey, Status: "running",
		VerificationStatus: "pending", ReleaseGraphMode: plan.ReleaseGraphMode,
		SourceReleaseSetID:         projectEnvironmentPromotionReleaseSetID(plan.FromReleaseSet),
		PreviousTargetReleaseSetID: projectEnvironmentPromotionReleaseSetID(plan.ToReleaseSet),
		ReleaseTTLSeconds:          plan.ReleaseTTLSeconds,
		SyncConfig:                 plan.SyncConfig,
	}
	if plan.SyncConfig {
		promotionRecord.SourceConfigHash = plan.Preview.ConfigDiff.FromHash
		promotionRecord.PreviousTargetConfigHash = plan.Preview.ConfigDiff.ToHash
		promotionRecord.SourceConfigSnapshot = projectEnvironmentConfigSnapshot(plan.SourceConfig)
		promotionRecord.PreviousTargetConfigSnapshot = projectEnvironmentConfigSnapshot(plan.TargetConfig)
	}
	promotion, workloads, err := s.store.CreateProjectEnvironmentPromotion(r.Context(), promotionRecord,
		projectEnvironmentPromotionWorkloads(plan))
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
		if promotion.ReleaseGraphMode {
			plan, problem = s.buildProjectEnvironmentPromotionResumePlan(r.Context(), acct, promotion, workloads)
		} else {
			plan, problem = s.buildProjectEnvironmentPromotionPlan(r.Context(), acct, projectSlug, fromEnvironment, toEnvironment, wire.SyncConfig)
		}
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
			s.log.WarnContext(r.Context(), "project environment rollback stopped because target changed", "promotion_id", promotion.ID, "err", err)
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Rollback stopped because the target changed",
				"A target deployment changed after promotion. Review the current deployment state before retrying.").
				WithHint("Refresh the promotion status and confirm the intended target before trying again."))
		} else {
			api.WriteProblem(w, customerCapacityProblem(s.log, "roll back environment promotion",
				"Environment rollback temporarily unavailable",
				"Gregale could not complete the environment rollback.",
				"Check the promotion status before retrying; contact support if it remains incomplete.", err))
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
	if promotion.ReleaseGraphMode {
		return s.applyProjectEnvironmentPromotionGraphRollback(ctx, acct, promotion, workloads)
	}
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

func (s *server) applyProjectEnvironmentPromotionGraphRollback(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload) error {
	reader, ok := s.store.(state.ProjectReleaseSetReader)
	if !ok {
		return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion, errors.New("release graph inventory is unavailable"))
	}
	publisher, ok := s.store.(state.ProjectReleaseSetPromotionStore)
	if !ok {
		return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion, errors.New("atomic release graph rollback is unavailable"))
	}
	apps, err := s.store.AppsForProject(ctx, acct.ID, promotion.ProjectID)
	if err != nil {
		return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion, fmt.Errorf("could not list project workloads: %w", err))
	}
	appsBySlug := make(map[string]state.App, len(apps))
	for _, app := range apps {
		appsBySlug[app.Slug] = app
	}
	promotedMembers := make([]state.ProjectReleaseMember, 0, len(workloads))
	previousMembers := make([]state.ProjectReleaseMember, 0, len(workloads))
	fallbackMembers := make([]state.ProjectReleaseMember, 0, len(workloads))
	for _, workload := range workloads {
		app, exists := appsBySlug[workload.WorkloadSlug]
		if !exists {
			return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion,
				fmt.Errorf("%w: workload no longer belongs to the project", state.ErrConflict))
		}
		if workload.TargetDeploymentID == "" {
			// A failed promotion may not have staged every member. In that
			// case it has not published a complete graph, so there is no route
			// graph to undo.
			continue
		}
		promotedMembers = append(promotedMembers, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: workload.TargetDeploymentID})
		if workload.PreviousTargetDeploymentID != "" {
			previousMembers = append(previousMembers, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: workload.PreviousTargetDeploymentID})
			if workload.PreviousTargetTrafficPercent == 100 {
				fallbackMembers = append(fallbackMembers, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: workload.PreviousTargetDeploymentID})
			}
		}
	}
	var previousGraph *state.ProjectReleaseSet
	if promotion.PreviousTargetReleaseSetID != "" {
		graph, err := reader.ProjectReleaseSetByID(ctx, acct.ID, promotion.ProjectID, promotion.ToEnvironment, promotion.PreviousTargetReleaseSetID)
		if err != nil {
			return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion,
				fmt.Errorf("%w: previous target release graph is unavailable", state.ErrConflict))
		}
		previousGraph = &graph
		previousMembers = append([]state.ProjectReleaseMember(nil), graph.Members...)
	}
	active, problem := s.activeProjectEnvironmentReleaseSet(ctx, acct.ID, promotion.ProjectID, promotion.ToEnvironment)
	if problem != nil {
		return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion, errors.New("could not inspect active target release graph"))
	}
	if previousGraph != nil && active.ID != "" && projectReleaseSetMatchesMembers(active, previousMembers) {
		// Recover if the rollback graph was activated immediately before the
		// process stopped and its promotion checkpoint was not saved.
		promotion, err = s.store.UpdateProjectEnvironmentPromotionReleaseSets(ctx, acct.ID, promotion.ID, "", active.ID)
		if err != nil {
			return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion, errors.New("could not record restored release graph"))
		}
		return s.completeProjectEnvironmentPromotionGraphRollback(ctx, acct, promotion, workloads)
	}
	if previousGraph == nil && active.ID == "" && projectPromotionFallbackMatches(ctx, s.store, appsBySlug, promotion.ToEnvironment, workloads) {
		return s.completeProjectEnvironmentPromotionGraphRollback(ctx, acct, promotion, workloads)
	}
	if promotion.TargetReleaseSetID == "" {
		if active.ID == "" || !projectReleaseSetMatchesMembers(active, promotedMembers) {
			return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion,
				fmt.Errorf("%w: promoted release graph cannot be identified", state.ErrConflict))
		}
		promotion, err = s.store.UpdateProjectEnvironmentPromotionReleaseSets(ctx, acct.ID, promotion.ID, active.ID, "")
		if err != nil {
			return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion, errors.New("could not record promoted release graph"))
		}
	}
	if active.ID != promotion.TargetReleaseSetID || !projectReleaseSetMatchesMembers(active, promotedMembers) {
		return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion,
			fmt.Errorf("%w: active target release graph changed after promotion", state.ErrConflict))
	}
	if previousGraph != nil {
		var restored state.ProjectReleaseSet
		if promotion.SyncConfig {
			configPublisher, ok := s.store.(state.ProjectEnvironmentPromotionReleaseSetStore)
			if !ok {
				return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion, errors.New("atomic promotion config rollback is unavailable"))
			}
			restored, err = configPublisher.RollbackProjectEnvironmentPromotionReleaseSet(ctx, acct.ID, promotion.ID,
				previousGraph.TTLSeconds, previousGraph.Members)
		} else {
			restored, err = publisher.PublishProjectReleaseSetIfActive(ctx, acct.ID, promotion.ProjectID,
				promotion.ToEnvironment, promotion.TargetReleaseSetID, nil, previousGraph.TTLSeconds, previousGraph.Members)
		}
		if err != nil {
			return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion,
				fmt.Errorf("%w: could not atomically restore previous release graph", state.ErrConflict))
		}
		promotion, err = s.store.UpdateProjectEnvironmentPromotionReleaseSets(ctx, acct.ID, promotion.ID, "", restored.ID)
		if err != nil {
			return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion, errors.New("could not record restored release graph"))
		}
	} else {
		if err := publisher.DeactivateProjectReleaseSetIfActive(ctx, acct.ID, promotion.ProjectID,
			promotion.ToEnvironment, promotion.TargetReleaseSetID, fallbackMembers); err != nil {
			return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion,
				fmt.Errorf("%w: target route changed; refusing to remove the promoted graph", state.ErrConflict))
		}
	}
	return s.completeProjectEnvironmentPromotionGraphRollback(ctx, acct, promotion, workloads)
}

func projectPromotionFallbackMatches(ctx context.Context, store state.Store, appsBySlug map[string]state.App, environment string, workloads []state.ProjectEnvironmentPromotionWorkload) bool {
	expected := make(map[string]string, len(workloads))
	for _, workload := range workloads {
		app, ok := appsBySlug[workload.WorkloadSlug]
		if !ok {
			return false
		}
		if workload.PreviousTargetTrafficPercent == 100 && workload.PreviousTargetDeploymentID != "" {
			expected[app.ID] = workload.PreviousTargetDeploymentID
		}
	}
	for _, app := range appsBySlug {
		deployments, err := store.LiveDeployments(ctx, app.ID)
		if err != nil {
			return false
		}
		var weighted []state.Deployment
		for _, deployment := range deployments {
			if deployment.Scope == environment && deployment.Status == state.DeployLive && deployment.TrafficPercent > 0 {
				weighted = append(weighted, deployment)
			}
		}
		want := expected[app.ID]
		if want == "" {
			if len(weighted) != 0 {
				return false
			}
		} else if len(weighted) != 1 || weighted[0].ID != want || weighted[0].TrafficPercent != 100 {
			return false
		}
	}
	return true
}

func (s *server) completeProjectEnvironmentPromotionGraphRollback(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload) error {
	for _, workload := range workloads {
		status, restoredID := "cleared", ""
		if workload.PreviousTargetDeploymentID != "" {
			status, restoredID = "restored", workload.PreviousTargetDeploymentID
		}
		if workload.Status == "unchanged" {
			status = "unchanged"
		}
		if _, err := s.store.UpdateProjectEnvironmentPromotionRollbackWorkload(ctx, acct.ID, promotion.ID,
			workload.ID, status, restoredID, ""); err != nil {
			return s.recordProjectEnvironmentPromotionGraphRollbackFailure(ctx, acct, promotion, errors.New("could not record release graph rollback checkpoint"))
		}
	}
	now := time.Now().UTC()
	if _, err := s.store.UpdateProjectEnvironmentPromotionRollback(ctx, acct.ID, promotion.ID, "rolled_back", "", &now); err != nil {
		return fmt.Errorf("could not complete environment release graph rollback: %w", err)
	}
	return nil
}

func (s *server) recordProjectEnvironmentPromotionGraphRollbackFailure(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, cause error) error {
	message := projectEnvironmentRollbackFailureMessage(cause)
	if _, err := s.store.UpdateProjectEnvironmentPromotionRollback(ctx, acct.ID, promotion.ID, "rollback_failed", message, nil); err != nil {
		return fmt.Errorf("could not record release graph rollback failure: %w", err)
	}
	return cause
}

func (s *server) recordProjectEnvironmentPromotionRollbackFailure(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, workload state.ProjectEnvironmentPromotionWorkload, cause error) error {
	message := projectEnvironmentRollbackFailureMessage(cause)
	s.log.ErrorContext(ctx, "project environment workload rollback failed",
		"promotion_id", promotion.ID, "workload", workload.WorkloadSlug, "err", cause)
	if _, err := s.store.UpdateProjectEnvironmentPromotionRollbackWorkload(ctx, acct.ID, promotion.ID, workload.ID, "failed", "", message); err != nil {
		return fmt.Errorf("could not record environment rollback failure: %w", err)
	}
	now := time.Now().UTC()
	if _, err := s.store.UpdateProjectEnvironmentPromotionRollback(ctx, acct.ID, promotion.ID, "rollback_failed", message, &now); err != nil {
		return fmt.Errorf("could not complete environment rollback failure: %w", err)
	}
	return cause
}

func projectEnvironmentRollbackFailureMessage(err error) string {
	if errors.Is(err, state.ErrConflict) {
		return "A target deployment changed after promotion; review its current state before retrying rollback."
	}
	if err != nil && strings.Contains(err.Error(), "no longer belongs to the project") {
		return "A workload is no longer in this project; refresh the promotion before retrying rollback."
	}
	return "Gregale could not restore this workload during rollback. Check the promotion status or contact support."
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
		previousTrafficPercent := 0
		if target, ok := plan.Targets[change.WorkloadSlug]; ok {
			previousTrafficPercent = target.TrafficPercent
		}
		workloads = append(workloads, state.ProjectEnvironmentPromotionWorkload{
			WorkloadSlug: change.WorkloadSlug, WorkloadName: change.WorkloadName,
			SourceDeploymentID:           change.SourceDeploymentID,
			PreviousTargetDeploymentID:   change.TargetDeploymentID,
			PreviousTargetTrafficPercent: previousTrafficPercent,
			TargetDeploymentID:           change.TargetDeploymentID, Status: status,
			VerificationStatus: "pending",
		})
	}
	return workloads
}

func validateProjectEnvironmentPromotionResume(wire projectEnvironmentPromotionTokenWire, promotion state.ProjectEnvironmentPromotion, workloads []state.ProjectEnvironmentPromotionWorkload, plan projectEnvironmentPromotionPlan) *api.Problem {
	configMatches := wire.ToConfigHash == plan.Preview.ConfigDiff.ToHash
	if promotion.SyncConfig && promotion.TargetConfigVersion != 0 {
		configMatches = wire.ToConfigHash == promotion.PreviousTargetConfigHash &&
			plan.Preview.ConfigDiff.ToHash == promotion.SourceConfigHash &&
			plan.TargetConfig.Version == promotion.TargetConfigVersion
	}
	if wire.ProjectID != plan.ProjectID || wire.SyncConfig != promotion.SyncConfig || wire.SyncConfig != plan.SyncConfig ||
		wire.FromConfigHash != plan.Preview.ConfigDiff.FromHash || !configMatches ||
		wire.FromReleaseSetID != projectEnvironmentPromotionReleaseSetID(plan.FromReleaseSet) ||
		wire.ToReleaseSetID != projectEnvironmentPromotionReleaseSetID(plan.ToReleaseSet) ||
		wire.PromotionHash != plan.Preview.PromotionHash {
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
			if plan.ReleaseGraphMode {
				target := plan.Targets[workload.WorkloadSlug]
				if target.ID != workload.TargetDeploymentID || target.Reason != projectEnvironmentPromotionDeploymentReason(promotion.ID) {
					return promotionResumeConflict("a promoted workload deployment changed; inspect the promotion")
				}
			} else if change.TargetDeploymentID != workload.TargetDeploymentID {
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
		var promoted state.Deployment
		var promoteErr error
		if plan.ReleaseGraphMode {
			promoted, promoteErr = promoteProjectEnvironmentDeploymentDark(ctx, s.store, plan.Sources[workload.WorkloadSlug], promotion.ToEnvironment, promotion.ID)
		} else {
			promoted, promoteErr = promoteProjectEnvironmentDeployment(ctx, s.store, plan.Sources[workload.WorkloadSlug], promotion.ToEnvironment, promotion.ID)
		}
		if promoteErr != nil {
			return s.failProjectEnvironmentPromotion(ctx, acct, promotion, workload, "could not promote workload "+change.WorkloadSlug)
		}
		if _, err := s.store.UpdateProjectEnvironmentPromotionWorkload(ctx, acct.ID, promotion.ID, workload.ID, "promoted", promoted.ID, ""); err != nil {
			return api.ProjectEnvironmentPromotionResponse{}, api.ErrCapacity("could not update environment promotion checkpoint")
		}
	}
	if plan.ReleaseGraphMode {
		updated, problem := s.activateProjectEnvironmentPromotionGraph(ctx, acct, promotion, plan)
		if problem != nil {
			return api.ProjectEnvironmentPromotionResponse{}, problem
		}
		promotion = updated
	}

	verificationStarted := time.Now().UTC()
	if _, err := s.store.UpdateProjectEnvironmentPromotionVerification(ctx, acct.ID, promotion.ID,
		"verifying", "", &verificationStarted, nil); err != nil {
		return api.ProjectEnvironmentPromotionResponse{}, api.ErrCapacity("could not start environment promotion verification")
	}
	if err := s.verifyProjectEnvironmentPromotion(ctx, acct, promotion, plan); err != nil {
		verificationCompleted := time.Now().UTC()
		message := projectEnvironmentVerificationMessage(err)
		s.log.ErrorContext(ctx, "project environment promotion verification failed",
			"promotion_id", promotion.ID, "err", err)
		_, _ = s.store.UpdateProjectEnvironmentPromotionVerification(ctx, acct.ID, promotion.ID,
			"failed", message, nil, &verificationCompleted)
		_, _ = s.store.UpdateProjectEnvironmentPromotion(ctx, acct.ID, promotion.ID, "failed", message, &verificationCompleted)
		if rollbackErr := s.autoRollbackProjectEnvironmentPromotion(ctx, acct, promotion); rollbackErr != nil {
			s.log.ErrorContext(ctx, "automatic environment promotion rollback failed",
				"promotion_id", promotion.ID, "err", rollbackErr)
			return api.ProjectEnvironmentPromotionResponse{}, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Promotion verification failed",
				message+" Automatic rollback could not be confirmed; check the promotion status before retrying.").
				WithHint("Review the promotion status and current target deployments. Contact support if rollback is incomplete.")
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

func (s *server) activateProjectEnvironmentPromotionGraph(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, plan projectEnvironmentPromotionPlan) (state.ProjectEnvironmentPromotion, *api.Problem) {
	if !promotion.ReleaseGraphMode || plan.ReleaseTTLSeconds <= 0 {
		return promotion, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Release graph promotion is invalid", "the promotion did not capture a valid release graph TTL")
	}
	publisher, ok := s.store.(state.ProjectReleaseSetPromotionStore)
	if !ok {
		return promotion, api.ErrCapacity("atomic project release activation is unavailable")
	}
	_, workloads, err := s.store.ProjectEnvironmentPromotionByID(ctx, acct.ID, promotion.ProjectSlug, promotion.ToEnvironment, promotion.ID)
	if err != nil {
		return promotion, api.ErrCapacity("could not load release graph promotion checkpoints")
	}
	members := make([]state.ProjectReleaseMember, 0, len(workloads))
	for _, workload := range workloads {
		app, found := plan.Apps[workload.WorkloadSlug]
		if !found || workload.TargetDeploymentID == "" || (workload.Status != "promoted" && workload.Status != "unchanged") {
			return promotion, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Release graph promotion is incomplete", "every workload must have a staged target before graph activation")
		}
		members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: workload.TargetDeploymentID})
	}
	if len(members) != len(plan.Apps) {
		return promotion, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Release graph promotion is incomplete", "the staged target does not include every project workload")
	}
	active, problem := s.activeProjectEnvironmentReleaseSet(ctx, acct.ID, promotion.ProjectID, promotion.ToEnvironment)
	if problem != nil {
		return promotion, problem
	}
	expectedID := promotion.PreviousTargetReleaseSetID
	var target state.ProjectReleaseSet
	switch {
	case promotion.TargetReleaseSetID != "" && active.ID == promotion.TargetReleaseSetID:
		if !projectReleaseSetMatchesMembers(active, members) {
			return promotion, s.failProjectEnvironmentPromotionGraph(ctx, acct, promotion, "the active target graph no longer matches this promotion")
		}
		target = active
	case promotion.TargetReleaseSetID == "" && active.ID != "" && projectReleaseSetMatchesMembers(active, members):
		// Recover the commit/checkpoint gap if the graph was published but
		// the durable promotion row was not yet updated.
		target = active
	case promotion.TargetReleaseSetID == "" && active.ID == expectedID:
		if promotion.SyncConfig {
			configPublisher, ok := s.store.(state.ProjectEnvironmentPromotionReleaseSetStore)
			if !ok {
				return promotion, api.ErrCapacity("atomic project release and config activation is unavailable")
			}
			target, err = configPublisher.PublishProjectEnvironmentPromotionReleaseSet(ctx, acct.ID, promotion.ID,
				plan.ReleaseTTLSeconds, members)
		} else {
			target, err = publisher.PublishProjectReleaseSetIfActive(ctx, acct.ID, promotion.ProjectID,
				promotion.ToEnvironment, expectedID, plan.FallbackTargetMembers, plan.ReleaseTTLSeconds, members)
		}
		if errors.Is(err, state.ErrConflict) {
			return promotion, s.failProjectEnvironmentPromotionGraph(ctx, acct, promotion, "the target route changed after preview; preview and promote again")
		}
		if err != nil {
			return promotion, api.ErrCapacity("could not atomically activate the promoted release graph")
		}
	default:
		return promotion, s.failProjectEnvironmentPromotionGraph(ctx, acct, promotion, "the active target release graph changed after preview; inspect the target before retrying")
	}
	updated, err := s.store.UpdateProjectEnvironmentPromotionReleaseSets(ctx, acct.ID, promotion.ID, target.ID, "")
	if err != nil {
		return promotion, api.ErrCapacity("release graph activated but its promotion checkpoint could not be saved")
	}
	return updated, nil
}

func (s *server) failProjectEnvironmentPromotionGraph(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, message string) *api.Problem {
	if _, err := s.store.UpdateProjectEnvironmentPromotion(ctx, acct.ID, promotion.ID, "failed", message, nil); err != nil {
		return api.ErrCapacity("release graph activation failed and its promotion status could not be saved")
	}
	return api.NewProblem(http.StatusConflict, api.CodeProjectEnvironmentApprovalInvalid,
		"Target release graph changed", message)
}

func projectReleaseSetMatchesMembers(release state.ProjectReleaseSet, members []state.ProjectReleaseMember) bool {
	if len(release.Members) != len(members) {
		return false
	}
	byApp := make(map[string]string, len(release.Members))
	for _, member := range release.Members {
		byApp[member.AppID] = member.DeploymentID
	}
	for _, member := range members {
		if byApp[member.AppID] != member.DeploymentID {
			return false
		}
	}
	return true
}

func projectEnvironmentVerificationMessage(err error) string {
	message := err.Error()
	if strings.HasPrefix(message, "could not ") {
		return "Gregale could not verify one or more deployment records."
	}
	return message
}

func (s *server) verifyProjectEnvironmentPromotion(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, plan projectEnvironmentPromotionPlan) error {
	_, workloads, err := s.store.ProjectEnvironmentPromotionByID(ctx, acct.ID, promotion.ProjectSlug, promotion.ToEnvironment, promotion.ID)
	if err != nil {
		return fmt.Errorf("could not load promotion verification checkpoints: %w", err)
	}
	if promotion.ReleaseGraphMode {
		if err := s.verifyProjectEnvironmentPromotionGraph(ctx, acct, promotion, plan, workloads); err != nil {
			return err
		}
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
	if promotion.ReleaseGraphMode {
		// Re-read after per-workload verification so a graph replacement during
		// the checks cannot be reported as a successful promotion.
		if err := s.verifyProjectEnvironmentPromotionGraph(ctx, acct, promotion, plan, workloads); err != nil {
			return err
		}
	}
	completed := time.Now().UTC()
	if _, err := s.store.UpdateProjectEnvironmentPromotionVerification(ctx, acct.ID, promotion.ID,
		"verified", "", nil, &completed); err != nil {
		return fmt.Errorf("could not complete environment promotion verification: %w", err)
	}
	return nil
}

func (s *server) verifyProjectEnvironmentPromotionGraph(ctx context.Context, acct state.Account, promotion state.ProjectEnvironmentPromotion, plan projectEnvironmentPromotionPlan, workloads []state.ProjectEnvironmentPromotionWorkload) error {
	if promotion.TargetReleaseSetID == "" {
		return errors.New("promoted release graph ID was not recorded")
	}
	active, problem := s.activeProjectEnvironmentReleaseSet(ctx, acct.ID, promotion.ProjectID, promotion.ToEnvironment)
	if problem != nil {
		return errors.New("could not load active promoted release graph")
	}
	if active.ID != promotion.TargetReleaseSetID {
		return errors.New("active target release graph changed during promotion verification")
	}
	members := make([]state.ProjectReleaseMember, 0, len(workloads))
	for _, workload := range workloads {
		app, ok := plan.Apps[workload.WorkloadSlug]
		if !ok || workload.TargetDeploymentID == "" {
			return fmt.Errorf("workload %q has no target graph member", workload.WorkloadSlug)
		}
		members = append(members, state.ProjectReleaseMember{AppID: app.ID, DeploymentID: workload.TargetDeploymentID})
	}
	if !projectReleaseSetMatchesMembers(active, members) {
		return errors.New("active target release graph does not match the promotion checkpoints")
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
		SyncConfig: promotion.SyncConfig, PromotionHash: promotion.PromotionHash,
		ReleaseGraph: projectEnvironmentPromotionReleaseGraph(promotion), Workloads: results,
	}
}

func projectEnvironmentPromotionReleaseGraph(promotion state.ProjectEnvironmentPromotion) *api.ProjectEnvironmentPromotionReleaseGraphResponse {
	if !promotion.ReleaseGraphMode {
		return nil
	}
	return &api.ProjectEnvironmentPromotionReleaseGraphResponse{
		SourceReleaseSetID:         promotion.SourceReleaseSetID,
		PreviousTargetReleaseSetID: promotion.PreviousTargetReleaseSetID,
		TargetReleaseSetID:         promotion.TargetReleaseSetID,
		RestoredTargetReleaseSetID: promotion.RestoredTargetReleaseSetID,
		TTLSeconds:                 promotion.ReleaseTTLSeconds,
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
		SyncConfig:    promotion.SyncConfig,
		PromotionHash: promotion.PromotionHash, Status: promotion.Status, Error: promotion.Error,
		RollbackStatus: promotion.RollbackStatus, RollbackError: promotion.RollbackError,
		RollbackStartedAt:   formatOptionalTime(promotion.RollbackStartedAt),
		RollbackCompletedAt: formatOptionalTime(promotion.RollbackCompletedAt),
		VerificationStatus:  promotion.VerificationStatus, VerificationError: promotion.VerificationError,
		VerificationStartedAt:   formatOptionalTime(promotion.VerificationStartedAt),
		VerificationCompletedAt: formatOptionalTime(promotion.VerificationCompletedAt),
		ReleaseGraph:            projectEnvironmentPromotionReleaseGraph(promotion),
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
	current, problem := s.buildProjectEnvironmentPromotionPlan(ctx, acct, projectSlug, fromEnvironment, toEnvironment, plan.SyncConfig)
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
	return promoteProjectEnvironmentDeploymentWithTraffic(ctx, store, source, targetEnvironment, promotionID, false)
}

func promoteProjectEnvironmentDeploymentDark(ctx context.Context, store state.Store, source state.Deployment, targetEnvironment, promotionID string) (state.Deployment, error) {
	return promoteProjectEnvironmentDeploymentWithTraffic(ctx, store, source, targetEnvironment, promotionID, true)
}

func promoteProjectEnvironmentDeploymentWithTraffic(ctx context.Context, store state.Store, source state.Deployment, targetEnvironment, promotionID string, dark bool) (state.Deployment, error) {
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
	if dark {
		candidate.TrafficPercent = 0
		candidate.TrafficPercentExplicit = true
	}
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
	if dark {
		stager, ok := store.(state.ProjectPromotionDeploymentStore)
		if !ok {
			return state.Deployment{}, fmt.Errorf("state: dark project promotion deployment staging is unavailable")
		}
		if err := stager.MarkDeploymentLiveDark(ctx, created.ID); err != nil {
			return state.Deployment{}, err
		}
	} else if err := store.MarkDeploymentLive(ctx, created.ID); err != nil {
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

func projectEnvironmentConfigSnapshot(config state.ProjectEnvironmentConfig) json.RawMessage {
	if len(config.Values) == 0 {
		return json.RawMessage(`{}`)
	}
	return append(json.RawMessage(nil), config.Values...)
}

func projectEnvironmentPromotionHash(
	projectSlug, fromEnvironment, toEnvironment string,
	configDiff api.ProjectEnvironmentConfigDiffResponse,
	syncConfig bool,
	fromReleaseSetID, toReleaseSetID string,
	changes []api.ProjectEnvironmentPromotionChange,
) (string, error) {
	identity := struct {
		ProjectSlug      string                                  `json:"project_slug"`
		FromEnvironment  string                                  `json:"from_environment"`
		ToEnvironment    string                                  `json:"to_environment"`
		FromConfigHash   string                                  `json:"from_config_hash"`
		ToConfigHash     string                                  `json:"to_config_hash"`
		SyncConfig       bool                                    `json:"sync_config,omitempty"`
		FromReleaseSetID string                                  `json:"from_release_set_id,omitempty"`
		ToReleaseSetID   string                                  `json:"to_release_set_id,omitempty"`
		Changes          []api.ProjectEnvironmentPromotionChange `json:"changes"`
	}{
		ProjectSlug: projectSlug, FromEnvironment: fromEnvironment, ToEnvironment: toEnvironment,
		FromConfigHash: configDiff.FromHash, ToConfigHash: configDiff.ToHash, SyncConfig: syncConfig,
		FromReleaseSetID: fromReleaseSetID, ToReleaseSetID: toReleaseSetID, Changes: changes,
	}
	raw, err := json.Marshal(identity)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func projectEnvironmentPromotionToken(accountID, projectID, projectSlug, fromEnvironment, toEnvironment, fromConfigHash, toConfigHash, fromReleaseSetID, toReleaseSetID string, syncConfig bool, promotionHash string) (string, error) {
	wire := projectEnvironmentPromotionTokenWire{
		Version: 1, AccountID: accountID, ProjectID: projectID, ProjectSlug: projectSlug,
		FromEnvironment: fromEnvironment, ToEnvironment: toEnvironment,
		FromConfigHash: fromConfigHash, ToConfigHash: toConfigHash,
		FromReleaseSetID: fromReleaseSetID, ToReleaseSetID: toReleaseSetID,
		SyncConfig:    syncConfig,
		PromotionHash: promotionHash, IssuedAt: timeNow().UTC().Unix(),
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func projectEnvironmentPromotionReleaseSetID(release *state.ProjectReleaseSet) string {
	if release == nil {
		return ""
	}
	return release.ID
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
