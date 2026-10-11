package main

import (
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listOperationDefinitions(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.operationStore(w)
	if !ok {
		return
	}
	dep, ok := s.operationDefinitionDeployment(w, r, acct)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	definitions, err := store.OperationDefinitionsForDeployment(r.Context(), acct.ID, dep.AppID, dep.ID)
	if err != nil {
		writeOperationError(w, err)
		return
	}
	page := api.OperationDefinitionsResponse{Definitions: []api.OperationDefinitionSummary{}}
	for _, definition := range definitions {
		d, spec := definition.OperationDefinitionResponse, definition.Spec
		page.Definitions = append(page.Definitions, api.OperationDefinitionSummary{ID: d.ID, AppID: d.AppID, Scope: d.Scope, Revision: d.Revision, DeploymentID: d.DeploymentID, ReleaseID: d.ReleaseID, Name: spec.Name, Method: spec.Method, Path: spec.Path, Owner: spec.Owner, ProgressStages: spec.ProgressStages, CompletionWebhookID: spec.CompletionWebhookID, Recovery: spec.Recovery, CreatedAt: d.CreatedAt})
	}
	sort.Slice(page.Definitions, func(i, j int) bool { return page.Definitions[i].Name < page.Definitions[j].Name })
	writeJSON(w, http.StatusOK, page)
}

func (s *server) getOperationDefinition(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.operationStore(w)
	if !ok {
		return
	}
	dep, ok := s.operationDefinitionDeployment(w, r, acct)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	definition, err := store.OperationDefinitionForDeployment(r.Context(), acct.ID, dep.AppID, dep.ID, r.PathValue("name"))
	if err != nil {
		writeOperationError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, definition.OperationDefinitionResponse)
}

func (s *server) getPlatformTenantSelfOperationIdentity(w http.ResponseWriter, r *http.Request, acct state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	tenant, ok := platformTenantSelfID(w, r)
	if !ok {
		return
	}
	if r.URL.RawQuery != "" {
		writeOperationError(w, state.ErrInvalidArgument)
		return
	}
	writeJSON(w, http.StatusOK, api.OperationTenantIdentity{AccountID: acct.ID, PlatformTenantID: tenant})
}
