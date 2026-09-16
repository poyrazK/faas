package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// projectEnvironmentPromotionTokenWire is deliberately independent from the
// source-upload plan token. Promotion preview is read-only today; a future
// execute endpoint can validate this identity against the current live
// deployments and configuration hashes before mutating anything.
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

func (s *server) previewProjectEnvironmentPromotion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	projectSlug := r.PathValue("slug")
	toEnvironment := strings.TrimSpace(r.PathValue("environment"))
	fromEnvironment := strings.TrimSpace(r.URL.Query().Get("from"))
	if fromEnvironment == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Source environment required", "from is required"))
		return
	}
	if !api.ValidProjectEnvironmentSlug(fromEnvironment) || !api.ValidProjectEnvironmentSlug(toEnvironment) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid environment", "source and target must be lowercase project environment slugs"))
		return
	}
	if fromEnvironment == toEnvironment {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Environments must differ", "source and target must be different project environments"))
		return
	}

	project, to, toConfig, problem := s.loadProjectEnvironmentConfig(r.Context(), acct, projectSlug, toEnvironment)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	_, from, fromConfig, problem := s.loadProjectEnvironmentConfig(r.Context(), acct, projectSlug, fromEnvironment)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	configChanges, err := projectEnvironmentConfigDiff(fromConfig.Values, toConfig.Values)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not compare environment configurations"))
		return
	}
	configDiff := api.ProjectEnvironmentConfigDiffResponse{
		ProjectSlug: project.Slug, FromEnvironment: from.Slug, ToEnvironment: to.Slug,
		FromVersion: fromConfig.Version, ToVersion: toConfig.Version,
		FromHash: configHashOrEmpty(fromConfig), ToHash: configHashOrEmpty(toConfig),
		Changes: configChanges,
	}

	apps, err := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list project workloads"))
		return
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })

	changes := make([]api.ProjectEnvironmentPromotionChange, 0, len(apps))
	blockingReasons := make([]string, 0)
	for _, app := range apps {
		source, sourceErr := s.store.LiveDeploymentForScope(r.Context(), app.ID, fromEnvironment)
		if sourceErr != nil && !errors.Is(sourceErr, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not inspect source environment deployments"))
			return
		}
		target, targetErr := s.store.LiveDeploymentForScope(r.Context(), app.ID, toEnvironment)
		if targetErr != nil && !errors.Is(targetErr, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrCapacity("could not inspect target environment deployments"))
			return
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
		api.WriteProblem(w, api.ErrInternal("could not create promotion identity"))
		return
	}
	promotionToken, err := projectEnvironmentPromotionToken(acct.ID, project.ID, project.Slug,
		from.Slug, to.Slug, configDiff.FromHash, configDiff.ToHash, promotionHash)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal("could not create promotion token"))
		return
	}
	writeJSON(w, http.StatusOK, api.ProjectEnvironmentPromotionPreviewResponse{
		ProjectSlug: project.Slug, FromEnvironment: from.Slug, ToEnvironment: to.Slug,
		ToEnvironmentProtected: to.Protected, ApprovalRequired: to.Protected,
		CanPromote: len(blockingReasons) == 0, BlockingReasons: blockingReasons,
		ConfigDiff: configDiff, Changes: changes,
		PromotionHash: promotionHash, PromotionToken: promotionToken,
	})
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
