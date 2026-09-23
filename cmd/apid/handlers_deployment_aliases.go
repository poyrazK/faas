package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) listDeploymentAliases(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	store, ok := s.deploymentAliasStore(w)
	if !ok {
		return
	}
	rows, err := store.ListDeploymentAliases(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list deployment aliases"))
		return
	}
	resp := api.DeploymentAliasListResponse{Items: make([]api.DeploymentAliasResponse, 0, len(rows))}
	for _, row := range rows {
		resp.Items = append(resp.Items, deploymentAliasResponse(row, app.ID, s.domain))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) setDeploymentAlias(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	name := r.PathValue("name")
	if _, ok := api.DeploymentAliasHostLabel(app.ID, name); !ok {
		api.WriteProblem(w, api.ErrValidation("alias name must fit in a 63-character hostname label"))
		return
	}
	var req api.SetDeploymentAliasRequest
	if err := decodeJSON(r, &req); err != nil || req.DeploymentID == "" {
		api.WriteProblem(w, api.ErrValidation("deployment_id is required"))
		return
	}
	store, ok := s.deploymentAliasStore(w)
	if !ok {
		return
	}
	alias, err := store.SetDeploymentAlias(r.Context(), app.ID, name, req.DeploymentID)
	if errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no routable deployment for this app")
		return
	}
	if errors.Is(err, state.ErrInvalidArgument) {
		api.WriteProblem(w, api.ErrValidation("invalid deployment alias name or hostname"))
		return
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict,
			"Alias hostname is already in use", "the generated alias host collides with an existing app hostname"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not set deployment alias"))
		return
	}
	s.audit.Emit(r.Context(), "deployment_alias.set", &acct.ID, map[string]any{
		"app_id": app.ID, "name": alias.Name, "deployment_id": alias.DeploymentID,
	})
	writeJSON(w, http.StatusOK, deploymentAliasResponse(alias, app.ID, s.domain))
}

func (s *server) deleteDeploymentAlias(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	name := r.PathValue("name")
	if !api.ValidDeploymentAliasName(name) {
		api.WriteProblem(w, api.ErrValidation("alias name must be a lowercase DNS label of at most 63 characters"))
		return
	}
	store, ok := s.deploymentAliasStore(w)
	if !ok {
		return
	}
	if err := store.DeleteDeploymentAlias(r.Context(), app.ID, name); errors.Is(err, state.ErrNotFound) {
		s.notFound(w, "no such deployment alias")
		return
	} else if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete deployment alias"))
		return
	}
	s.audit.Emit(r.Context(), "deployment_alias.deleted", &acct.ID, map[string]any{
		"app_id": app.ID, "name": name,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) deploymentAliasStore(w http.ResponseWriter) (state.DeploymentAliasStore, bool) {
	store, ok := s.store.(state.DeploymentAliasStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("deployment aliases are unavailable"))
	}
	return store, ok
}

func deploymentAliasResponse(alias state.DeploymentAlias, appID, domain string) api.DeploymentAliasResponse {
	resp := api.DeploymentAliasResponse{
		Name: alias.Name, DeploymentID: alias.DeploymentID, Revision: alias.Revision,
		CreatedAt: alias.CreatedAt, UpdatedAt: alias.UpdatedAt,
	}
	label, ok := api.DeploymentAliasHostLabel(appID, alias.Name)
	domain = strings.Trim(strings.TrimSpace(domain), ".")
	if ok && domain != "" {
		resp.Host = label + "." + domain
		resp.URL = "https://" + resp.Host
	}
	return resp
}
