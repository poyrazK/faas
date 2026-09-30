package main

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) applicationStandardResourceStore(w http.ResponseWriter, r *http.Request, action authz.OrgAction) (state.ApplicationStandardResourceStore, string, bool) {
	_, orgID, ok := s.applicationStandardsStore(w, r, action)
	if !ok {
		return nil, "", false
	}
	store, ok := s.store.(state.ApplicationStandardResourceStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("application standard resource storage is unavailable"))
		return nil, "", false
	}
	return store, orgID, true
}

func (s *server) createApplicationStandardLogDestination(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, orgID, ok := s.applicationStandardResourceStore(w, r, authz.OrgActionManageApplicationStandards)
	if !ok {
		return
	}
	input, problem := applicationStandardLogDestinationInput(r, orgID, acct.ID)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	row, err := store.CreateApplicationStandardLogDestination(r.Context(), input)
	if err != nil {
		writeApplicationStandardResourceError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "application_standard.log_destination_created", &acct.ID, map[string]any{"org_id": orgID, "destination_id": row.ID, "config_hash": row.ConfigHash})
	writeJSON(w, http.StatusCreated, row.ApplicationStandardLogDestination)
}

func applicationStandardLogDestinationInput(r *http.Request, orgID, actorID string) (state.ApplicationStandardLogDestinationCreate, *api.Problem) {
	var req api.CreateApplicationStandardLogDestinationRequest
	if err := decodeJSON(r, &req); err != nil {
		return state.ApplicationStandardLogDestinationCreate{}, api.ErrValidation("invalid log destination JSON body")
	}
	if problem := validateAppLogDrainAuthHeader(req.AuthHeader); problem != nil {
		return state.ApplicationStandardLogDestinationCreate{}, problem
	}
	if problem := validateAppLogDrainURL(req.TargetURL); problem != nil {
		return state.ApplicationStandardLogDestinationCreate{}, problem
	}
	if problem := resolveAndCheckEgress(r.Context(), req.TargetURL); problem != nil {
		return state.ApplicationStandardLogDestinationCreate{}, problem
	}
	sealed, err := sealAppLogDrainAuthHeader(req.AuthHeader)
	if err != nil {
		return state.ApplicationStandardLogDestinationCreate{}, api.ErrCapacity("could not seal destination credentials")
	}
	return state.ApplicationStandardLogDestinationCreate{OrgID: orgID, ActorID: actorID, Name: req.Name, Kind: req.Kind, TargetURL: req.TargetURL, AuthHeaderSealed: sealed}, nil
}

func (s *server) getApplicationStandardLogDestination(w http.ResponseWriter, r *http.Request, _ state.Account) {
	store, orgID, ok := s.applicationStandardResourceStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return
	}
	row, err := store.GetApplicationStandardLogDestination(r.Context(), orgID, r.PathValue("resource"))
	if err != nil {
		writeApplicationStandardResourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row.ApplicationStandardLogDestination)
}

func (s *server) listApplicationStandardLogDestinations(w http.ResponseWriter, r *http.Request, _ state.Account) {
	store, orgID, ok := s.applicationStandardResourceStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return
	}
	after, limit, ok := applicationStandardResourcePage(w, r)
	if !ok {
		return
	}
	rows, err := store.ListApplicationStandardLogDestinations(r.Context(), orgID, after, limit+1)
	if err != nil {
		writeApplicationStandardResourceError(w, err)
		return
	}
	result := api.ApplicationStandardLogDestinationList{Destinations: []api.ApplicationStandardLogDestination{}}
	if len(rows) > limit {
		result.NextPageAfter = rows[limit-1].ID
		rows = rows[:limit]
	}
	for _, row := range rows {
		result.Destinations = append(result.Destinations, row.ApplicationStandardLogDestination)
	}
	writeJSON(w, http.StatusOK, result)
}

func (s *server) createApplicationStandardPublisher(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, orgID, ok := s.applicationStandardResourceStore(w, r, authz.OrgActionManageApplicationStandards)
	if !ok {
		return
	}
	var req api.CreateApplicationStandardPublisherRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid publisher JSON body"))
		return
	}
	der, err := base64.StdEncoding.DecodeString(req.PublicKeyDER)
	if err != nil {
		api.WriteProblem(w, api.ErrValidation("public_key_der must be base64 ECDSA P-256 SubjectPublicKeyInfo"))
		return
	}
	row, err := store.CreateApplicationStandardPublisher(r.Context(), state.ApplicationStandardPublisherCreate{OrgID: orgID, ActorID: acct.ID, Name: req.Name, PublicKeyDER: der})
	if err != nil {
		writeApplicationStandardResourceError(w, err)
		return
	}
	s.audit.Emit(r.Context(), "application_standard.publisher_created", &acct.ID, map[string]any{"org_id": orgID, "publisher_id": row.ID, "fingerprint": row.Fingerprint})
	writeJSON(w, http.StatusCreated, row)
}

func (s *server) getApplicationStandardPublisher(w http.ResponseWriter, r *http.Request, _ state.Account) {
	store, orgID, ok := s.applicationStandardResourceStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return
	}
	row, err := store.GetApplicationStandardPublisher(r.Context(), orgID, r.PathValue("resource"))
	if err != nil {
		writeApplicationStandardResourceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *server) listApplicationStandardPublishers(w http.ResponseWriter, r *http.Request, _ state.Account) {
	store, orgID, ok := s.applicationStandardResourceStore(w, r, authz.OrgActionViewApplicationStandards)
	if !ok {
		return
	}
	after, limit, ok := applicationStandardResourcePage(w, r)
	if !ok {
		return
	}
	rows, err := store.ListApplicationStandardPublishers(r.Context(), orgID, after, limit+1)
	if err != nil {
		writeApplicationStandardResourceError(w, err)
		return
	}
	result := api.ApplicationStandardPublisherList{Publishers: rows}
	if len(rows) > limit {
		result.NextPageAfter = rows[limit-1].ID
		result.Publishers = rows[:limit]
	}
	writeJSON(w, http.StatusOK, result)
}

func applicationStandardResourcePage(w http.ResponseWriter, r *http.Request) (string, int, bool) {
	limit := api.ApplicationStandardMaxListPage
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > limit {
			api.WriteProblem(w, api.ErrValidation("limit is outside the resource page bounds"))
			return "", 0, false
		}
		limit = parsed
	}
	return r.URL.Query().Get("after"), limit, true
}

func writeApplicationStandardResourceError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, state.ErrInvalidArgument):
		api.WriteProblem(w, api.ErrValidation("invalid application standard resource"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Standard resource not found", "No standard resource exists in this organization with that ID."))
	default:
		api.WriteProblem(w, api.ErrCapacity("application standard resource operation failed"))
	}
}
