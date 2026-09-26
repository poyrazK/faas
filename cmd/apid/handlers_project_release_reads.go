package main

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) projectReleaseReadContext(w http.ResponseWriter, r *http.Request, acct state.Account) (state.Project, state.ProjectReleaseSetReader, bool) {
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return project, nil, false
	}
	environment := r.PathValue("environment")
	if !api.ValidProjectEnvironmentSlug(environment) {
		api.WriteProblem(w, api.ErrValidation("invalid project environment"))
		return project, nil, false
	}
	if _, err := s.store.ProjectEnvironmentBySlug(r.Context(), acct.ID, project.ID, environment); err != nil {
		s.writeProjectReleaseReadError(w, err)
		return project, nil, false
	}
	reader, ok := s.store.(state.ProjectReleaseSetReader)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("project release inventory is unavailable"))
	}
	return project, reader, ok
}

func (s *server) writeProjectReleaseReadError(w http.ResponseWriter, err error) {
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "project environment or release set not found")
		return
	}
	api.WriteProblem(w, api.ErrCapacity("could not read project release inventory"))
}

func (s *server) getProjectReleaseSet(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, reader, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	id := r.PathValue("release")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteProblem(w, api.ErrValidation("release must be a UUID"))
		return
	}
	release, err := reader.ProjectReleaseSetByID(r.Context(), acct.ID, project.ID, r.PathValue("environment"), id)
	if err != nil {
		s.writeProjectReleaseReadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectReleaseSetResponse(release))
}

func (s *server) getActiveProjectReleaseSet(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, reader, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	release, err := reader.ActiveProjectReleaseSet(r.Context(), acct.ID, project.ID, r.PathValue("environment"))
	if err != nil {
		s.writeProjectReleaseReadError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, projectReleaseSetResponse(release))
}

func (s *server) listProjectReleaseSets(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, reader, ok := s.projectReleaseReadContext(w, r, acct)
	if !ok {
		return
	}
	limit, ok := parseProjectReleasePageLimit(w, r)
	if !ok {
		return
	}
	before, beforeID, ok := parseProjectEnvironmentPromotionHistoryCursor(w, r)
	if !ok {
		return
	}
	rows, err := reader.ListProjectReleaseSetsBefore(r.Context(), acct.ID, project.ID, r.PathValue("environment"), before, beforeID, limit+1)
	if err != nil {
		s.writeProjectReleaseReadError(w, err)
		return
	}
	response := api.ProjectReleaseSetListResponse{Items: make([]api.ProjectReleaseSetResponse, 0, len(rows))}
	if len(rows) > limit {
		last := rows[limit-1]
		response.NextBefore = cursor.Encode(cursor.Key{CreatedAt: last.CreatedAt, ID: last.ID})
		rows = rows[:limit]
	}
	for _, row := range rows {
		response.Items = append(response.Items, projectReleaseSetResponse(row))
	}
	writeJSON(w, http.StatusOK, response)
}

func parseProjectReleasePageLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	limit := api.ProjectReleaseSetPageDefault
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > api.ProjectReleaseSetPageMax {
			api.WriteProblem(w, api.ErrValidation("limit must be between 1 and "+strconv.Itoa(api.ProjectReleaseSetPageMax)))
			return 0, false
		}
		limit = value
	}
	return limit, true
}

func projectReleaseSetResponse(release state.ProjectReleaseSet) api.ProjectReleaseSetResponse {
	response := api.ProjectReleaseSetResponse{
		ID: release.ID, AccountID: release.AccountID, ProjectID: release.ProjectID,
		Environment: release.EnvironmentSlug, Active: release.Active, TTLSeconds: release.TTLSeconds,
		ExpiresAt: release.ExpiresAt, CreatedAt: release.CreatedAt,
		Members: make([]api.ProjectReleaseSetMemberResponse, 0, len(release.Members)),
	}
	for _, member := range release.Members {
		response.Members = append(response.Members, api.ProjectReleaseSetMemberResponse{AppID: member.AppID, DeploymentID: member.DeploymentID})
	}
	return response
}

func (s *server) activeProjectReleaseState(ctx context.Context, accountID, projectID, environment string) (*api.ProjectReleaseSetResponse, *api.Problem) {
	reader, ok := s.store.(state.ProjectReleaseSetReader)
	if !ok {
		return nil, api.ErrCapacity("project release inventory is unavailable")
	}
	release, err := reader.ActiveProjectReleaseSet(ctx, accountID, projectID, environment)
	if errors.Is(err, state.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, api.ErrCapacity("could not read active project release")
	}
	response := projectReleaseSetResponse(release)
	return &response, nil
}
