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
	Sources   map[string]state.Deployment
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
		Sources: make(map[string]state.Deployment),
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
		source, sourceErr := s.store.LiveDeploymentForScope(ctx, app.ID, fromEnvironment)
		if sourceErr != nil && !errors.Is(sourceErr, state.ErrNotFound) {
			return plan, api.ErrCapacity("could not inspect source environment deployments")
		}
		target, targetErr := s.store.LiveDeploymentForScope(ctx, app.ID, toEnvironment)
		if targetErr != nil && !errors.Is(targetErr, state.ErrNotFound) {
			return plan, api.ErrCapacity("could not inspect target environment deployments")
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

	results := make([]api.ProjectEnvironmentPromotionWorkloadResponse, 0, len(plan.Preview.Changes))
	for _, change := range plan.Preview.Changes {
		result := api.ProjectEnvironmentPromotionWorkloadResponse{
			WorkloadSlug: change.WorkloadSlug, WorkloadName: change.WorkloadName,
			SourceDeploymentID: change.SourceDeploymentID, TargetDeploymentID: change.TargetDeploymentID,
		}
		if change.Kind == "unchanged" {
			result.Status = "unchanged"
			results = append(results, result)
			continue
		}
		promoted, err := promoteProjectEnvironmentDeployment(r.Context(), s.store, plan.Sources[change.WorkloadSlug], toEnvironment)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not promote workload "+change.WorkloadSlug))
			return
		}
		result.Status = "promoted"
		result.TargetDeploymentID = promoted.ID
		results = append(results, result)
	}
	s.audit.Emit(r.Context(), "project.environment.promoted", &acct.ID, map[string]any{
		"project_slug": projectSlug, "from_environment": fromEnvironment, "to_environment": toEnvironment,
		"promotion_hash": plan.Preview.PromotionHash, "workload_count": len(results),
	})
	writeJSON(w, http.StatusOK, api.ProjectEnvironmentPromotionResponse{
		ProjectSlug: projectSlug, FromEnvironment: fromEnvironment, ToEnvironment: toEnvironment,
		PromotionHash: plan.Preview.PromotionHash, Workloads: results,
	})
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

func promoteProjectEnvironmentDeployment(ctx context.Context, store state.Store, source state.Deployment, targetEnvironment string) (state.Deployment, error) {
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
	candidate.Reason = "environment promotion"

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
