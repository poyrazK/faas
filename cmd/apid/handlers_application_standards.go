package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) applicationStandardsStore(w http.ResponseWriter, r *http.Request, action authz.OrgAction) (state.ApplicationStandardStore, string, bool) {
	if !s.requireOrgAction(w, r, action) {
		return nil, "", false
	}
	membership, ok := s.requireMembership(w, r)
	if !ok {
		return nil, "", false
	}
	store, ok := s.store.(state.ApplicationStandardStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standards storage is unavailable"))
		return nil, "", false
	}
	return store, membership.OrgID, true
}

func (s *server) publishApplicationStandardVersion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, orgID, ok := s.applicationStandardsStore(w, r, authz.OrgActionManageApplicationStandards)
	if !ok {
		return
	}
	var req api.CreateApplicationStandardVersionRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid standard JSON body"))
		return
	}
	version, err := store.PublishApplicationStandardVersion(r.Context(), state.ApplicationStandardPublish{OrgID: orgID, ActorID: acct.ID, Slug: r.PathValue("standard"), CreateApplicationStandardVersionRequest: req})
	if err != nil {
		writeApplicationStandardError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "application_standard.version_published", &acct.ID, map[string]any{"org_id": orgID, "standard_id": version.StandardID, "version": version.Version, "definition_hash": version.DefinitionHash})
	writeJSON(w, http.StatusCreated, version)
}

func (s *server) getApplicationStandardVersion(w http.ResponseWriter, r *http.Request, _ state.Account) {
	store, orgID, ok := s.applicationStandardsStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return
	}
	var version int64
	if number := r.URL.Query().Get("version"); number != "" {
		var err error
		version, err = strconv.ParseInt(number, 10, 64)
		if err != nil || version < 1 {
			api.WriteProblem(w, api.ErrValidation("version must be a positive integer"))
			return
		}
	}
	result, err := store.GetApplicationStandardVersion(r.Context(), orgID, r.PathValue("standard"), version)
	if err != nil {
		writeApplicationStandardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) listApplicationStandards(w http.ResponseWriter, r *http.Request, _ state.Account) {
	store, orgID, ok := s.applicationStandardsStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return
	}
	limit := api.ApplicationStandardMaxListPage
	if size := r.URL.Query().Get("limit"); size != "" {
		parsed, err := strconv.Atoi(size)
		if err != nil || parsed < 1 || parsed > limit {
			api.WriteProblem(w, api.ErrValidation("limit is outside the application standard page bounds"))
			return
		}
		limit = parsed
	}
	versions, err := store.ListApplicationStandards(r.Context(), orgID, r.URL.Query().Get("after"), limit+1)
	if err != nil {
		writeApplicationStandardError(w, err)
		return
	}
	result := api.ApplicationStandardList{Standards: versions}
	if len(versions) > limit {
		result.Standards = versions[:limit]
		result.NextPageAfter = versions[limit-1].Slug
	}
	writeJSON(w, http.StatusOK, result)
}

func writeApplicationStandardError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid application standard", err.Error()))
	case errors.Is(err, state.ErrConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "application_standard_version_stale", "Standard version changed", "Refresh the current version before publishing."))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Application standard not found", "No application standard exists in this organization at the requested version."))
	default:
		api.WriteProblem(w, api.ErrCapacity("application standard operation failed"))
	}
}
