package main

import (
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appstandards"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) getApplicationStandardEnrollment(w http.ResponseWriter, r *http.Request, _ state.Account) {
	_, orgID, ok := s.applicationStandardsStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return
	}
	app, ok := s.applicationStandardLiveApp(w, r, orgID)
	if !ok {
		return
	}
	store, ok := s.store.(state.ApplicationStandardEnrollmentStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standards enrollment is unavailable"))
		return
	}
	enrollment, err := store.GetApplicationStandardEnrollment(r.Context(), orgID, app.ID)
	if err != nil {
		writeApplicationStandardError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, applicationStandardEnrollmentResponse(enrollment))
}

func applicationStandardEnrollmentResponse(e state.ApplicationStandardEnrollment) api.ApplicationStandardEnrollment {
	local := e.LocalSettings
	if local == nil {
		local = appstandards.Settings{}
	}
	result := api.ApplicationStandardEnrollment{AppID: e.AppID, OrgID: e.OrgID, ProjectID: e.ProjectID,
		LocalSettings: local, AdditionalLogDestinations: append([]string{}, e.AdditionalLogDestinations...),
		Adoptions: append([]appstandards.Adoption{}, e.Adoptions...), MaterializedFields: append([]appstandards.Field{}, e.MaterializedFields...),
		DesiredRevision: e.DesiredRevision, PersistedRevision: e.PersistedRevision, ObservedRevision: e.ObservedRevision,
		State: e.State, ErrorCode: e.ErrorCode, UpdatedAt: e.UpdatedAt}
	if e.PersistedRevision > 0 && e.EffectiveHash != "" {
		result.InstalledEffective, result.InstalledEffectiveHash = &e.Effective, e.EffectiveHash
		result.InstalledExceptionExpiresAt = e.ExceptionExpiresAt
	}
	return result
}

func standardPathUUID(w http.ResponseWriter, r *http.Request, key string) (string, bool) {
	id, err := uuid.Parse(r.PathValue(key))
	if err != nil || id == uuid.Nil {
		api.WriteProblem(w, api.ErrValidation(key+" must be a nonzero UUID"))
		return "", false
	}
	return id.String(), true
}

func (s *server) applicationStandardLiveApp(w http.ResponseWriter, r *http.Request, orgID string) (state.App, bool) {
	id, ok := standardPathUUID(w, r, "app")
	if !ok {
		return state.App{}, false
	}
	app, err := s.store.AppByID(r.Context(), id)
	if errors.Is(err, state.ErrNotFound) || err == nil && (app.OrgID != orgID || app.Status == state.AppDeleted) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Application not found", "No live application exists in this organization with that ID."))
		return state.App{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("application standards enrollment is unavailable"))
		return state.App{}, false
	}
	return app, true
}
