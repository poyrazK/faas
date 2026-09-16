package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

const (
	projectEnvironmentPromotionHistoryLimitDefault = 50
	projectEnvironmentPromotionHistoryLimitMax     = 100
)

// projectEnvironmentPromotionHistoryLister is optional on the large Store
// interface so narrow embedders remain source-compatible. PgStore and
// MemStore both implement the indexed, cursor-shaped path.
type projectEnvironmentPromotionHistoryLister interface {
	ListProjectEnvironmentPromotionsBefore(ctx context.Context, accountID, projectSlug, targetEnvironment, sourceEnvironment, status string, before time.Time, beforeID string, limit int) ([]state.ProjectEnvironmentPromotion, error)
}

func (s *server) getProjectEnvironmentReleases(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	environmentSlug := r.PathValue("environment")
	environment, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, environmentSlug)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, environmentSlug))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not load project environment"))
		}
		return
	}
	apps, err := s.store.AppsForProject(r.Context(), acct.ID, project.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list project workloads"))
		return
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].Slug < apps[j].Slug })

	workloads := make([]api.ProjectEnvironmentReleaseWorkloadResponse, 0, len(apps))
	for _, app := range apps {
		workload := api.ProjectEnvironmentReleaseWorkloadResponse{
			WorkloadSlug: app.Slug,
			WorkloadName: app.WorkloadName,
			Status:       "not_deployed",
		}
		deployment, err := s.store.LiveDeploymentForScope(r.Context(), app.ID, environment.Slug)
		if errors.Is(err, state.ErrNotFound) {
			workloads = append(workloads, workload)
			continue
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not inspect project environment releases"))
			return
		}
		workload.Status = "live"
		workload.DeploymentID = deployment.ID
		workload.BuildID = deployment.BuildID
		workload.ImageDigest = deployment.ImageDigest
		workload.SourceURL = deployment.SourceURL
		workload.CommitSHA = deployment.CommitSHA
		workload.SourceSHA256 = deployment.SourceSHA256
		workload.TrafficPercent = deployment.TrafficPercent
		workload.CreatedAt = deployment.CreatedAt.UTC().Format(time.RFC3339Nano)
		workloads = append(workloads, workload)
	}
	writeJSON(w, http.StatusOK, api.ProjectEnvironmentReleaseListResponse{
		ProjectSlug: project.Slug,
		Environment: environment.Slug,
		Workloads:   workloads,
	})
}

func (s *server) listProjectEnvironmentPromotions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	environmentSlug := r.PathValue("environment")
	environment, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, environmentSlug)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, environmentSlug))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not load project environment"))
		}
		return
	}

	limit, ok := parseProjectEnvironmentPromotionHistoryLimit(w, r)
	if !ok {
		return
	}
	before, beforeID, ok := parseProjectEnvironmentPromotionHistoryCursor(w, r)
	if !ok {
		return
	}
	sourceEnvironment := strings.TrimSpace(r.URL.Query().Get("from"))
	if sourceEnvironment != "" && !api.ValidProjectEnvironmentSlug(sourceEnvironment) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid source environment", "from must be a lowercase project environment slug"))
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status != "" && status != "running" && status != "succeeded" && status != "failed" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid promotion status", "status must be running, succeeded, or failed"))
		return
	}
	lister, ok := s.store.(projectEnvironmentPromotionHistoryLister)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project environment promotion history is unavailable"))
		return
	}
	rows, err := lister.ListProjectEnvironmentPromotionsBefore(r.Context(), acct.ID, project.Slug,
		environment.Slug, sourceEnvironment, status, before, beforeID, limit+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list project environment promotions"))
		return
	}
	response := api.ProjectEnvironmentPromotionListResponse{
		Items: make([]api.ProjectEnvironmentPromotionSummaryResponse, 0, len(rows)),
	}
	for _, promotion := range rows {
		if len(response.Items) == limit {
			break
		}
		response.Items = append(response.Items, projectEnvironmentPromotionSummaryResponse(promotion))
	}
	if len(rows) > limit && len(response.Items) > 0 {
		last := rows[limit-1]
		response.NextBefore = cursor.Encode(cursor.Key{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	writeJSON(w, http.StatusOK, response)
}

func parseProjectEnvironmentPromotionHistoryLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := projectEnvironmentPromotionHistoryLimitDefault
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid limit", "limit must be a positive integer"))
			return 0, false
		}
		if n > projectEnvironmentPromotionHistoryLimitMax {
			n = projectEnvironmentPromotionHistoryLimitMax
		}
		limit = n
	}
	return limit, true
}

func parseProjectEnvironmentPromotionHistoryCursor(w http.ResponseWriter, r *http.Request) (time.Time, string, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get("before"))
	if raw == "" {
		return time.Time{}, "", true
	}
	key, err := cursor.Decode(raw)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid before", "before must be an opaque cursor returned as next_before"))
		return time.Time{}, "", false
	}
	if _, err := uuid.Parse(key.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid before", "before must be an opaque cursor returned as next_before"))
		return time.Time{}, "", false
	}
	return key.CreatedAt, key.ID, true
}

func projectEnvironmentPromotionSummaryResponse(promotion state.ProjectEnvironmentPromotion) api.ProjectEnvironmentPromotionSummaryResponse {
	return api.ProjectEnvironmentPromotionSummaryResponse{
		PromotionID:             promotion.ID,
		ProjectSlug:             promotion.ProjectSlug,
		FromEnvironment:         promotion.FromEnvironment,
		ToEnvironment:           promotion.ToEnvironment,
		PromotionHash:           promotion.PromotionHash,
		Status:                  promotion.Status,
		Error:                   promotion.Error,
		CreatedAt:               promotion.CreatedAt.UTC().Format(time.RFC3339Nano),
		UpdatedAt:               promotion.UpdatedAt.UTC().Format(time.RFC3339Nano),
		CompletedAt:             formatOptionalTime(promotion.CompletedAt),
		RollbackStatus:          promotion.RollbackStatus,
		RollbackError:           promotion.RollbackError,
		VerificationStatus:      promotion.VerificationStatus,
		VerificationError:       promotion.VerificationError,
		VerificationCompletedAt: formatOptionalTime(promotion.VerificationCompletedAt),
	}
}
