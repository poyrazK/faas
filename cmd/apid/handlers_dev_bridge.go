package main

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func (s *server) devBridgeStore(w http.ResponseWriter) (state.DevBridgeStore, bool) {
	store, ok := s.store.(state.DevBridgeStore)
	if !ok || !s.devBridgeEnabled {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "dev_bridge_unavailable", "Dev Bridge unavailable", "the development bridge preview is not enabled"))
		return nil, false
	}
	return store, true
}

func (s *server) createDevBridge(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.devBridgeStore(w)
	if !ok {
		return
	}
	var request api.CreateDevBridgeRequest
	if err := decodeJSON(r, &request); err != nil {
		api.WriteProblem(w, devBridgeValidation("invalid request"))
		return
	}
	scope, prob := s.resolveDevBridgeScope(r.Context(), acct, request)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	now := time.Now().UTC()
	var dependencies []api.DevBridgeDependency
	for _, id := range scope.DependencyAppIDs {
		dependency, err := s.store.AppByID(r.Context(), id)
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("resolve bridge dependencies"))
			return
		}
		dependencies = append(dependencies, api.DevBridgeDependency{AppID: id, Name: dependency.Slug})
	}
	session, credentials, err := devbridge.NewSession(scope, now, now.Add(api.DevBridgeSessionTTL))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("create bridge credentials"))
		return
	}
	if err := store.CreateDevBridge(r.Context(), session); err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, "dev_bridge_limit", "Bridge session limit", "revoke an existing bridge before creating another"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("persist bridge session"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, api.CreateDevBridgeResponse{Session: session, Credentials: credentials,
		EnvironmentURL: "https://" + gateway.BuildEnvironmentHost(wire.DeployWildcardSuffix, scope.EnvironmentID, scope.TargetAppID), Dependencies: dependencies})
}

func devBridgeValidation(detail string) *api.Problem {
	return api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid bridge", detail)
}

func (s *server) resolveDevBridgeScope(ctx context.Context, acct state.Account, request api.CreateDevBridgeRequest) (devbridge.Scope, *api.Problem) {
	if strings.TrimSpace(request.DeveloperID) == "" || len(request.DeveloperID) > 128 || len(request.Dependencies) > api.DevBridgeMaxDependencies {
		return devbridge.Scope{}, devBridgeValidation("developer_id is required and dependencies must be bounded")
	}
	app, err := s.store.AppBySlug(ctx, request.App)
	if err != nil || app.AccountID != acct.ID || app.ProjectID == "" {
		return devbridge.Scope{}, api.NewProblem(404, "dev_bridge_target_not_found", "Bridge target unavailable", "select an owned project app")
	}
	env, err := s.store.ProjectEnvironmentBySlug(ctx, acct.ID, app.ProjectID, request.Environment)
	if err != nil || env.Protected || env.Slug == "production" || env.Slug == "default" {
		return devbridge.Scope{}, devBridgeValidation("select an unprotected non-production environment")
	}
	scope := devbridge.Scope{AccountID: acct.ID, DeveloperID: request.DeveloperID, ProjectID: app.ProjectID, EnvironmentID: env.ID, TargetAppID: app.ID}
	if request.Dependencies == nil {
		for _, binding := range app.Manifest.ServiceBindings {
			request.Dependencies = append(request.Dependencies, binding.Service)
		}
	}
	if len(request.Dependencies) > api.DevBridgeMaxDependencies {
		return devbridge.Scope{}, devBridgeValidation("too many declared dependencies")
	}
	inventory, err := s.store.AppsForProject(ctx, acct.ID, app.ProjectID)
	if err != nil {
		return devbridge.Scope{}, api.ErrCapacity("resolve project services")
	}
	seen := map[string]bool{}
	for _, slug := range request.Dependencies {
		var dependency state.App
		for _, candidate := range inventory {
			if candidate.Slug == slug || (candidate.WorkloadName != "" && candidate.WorkloadName == slug) {
				dependency = candidate
				break
			}
		}
		if dependency.ID == "" || dependency.AccountID != acct.ID || dependency.ProjectID != app.ProjectID {
			return devbridge.Scope{}, devBridgeValidation("dependencies must belong to the same project")
		}
		if !seen[dependency.ID] {
			scope.DependencyAppIDs = append(scope.DependencyAppIDs, dependency.ID)
			seen[dependency.ID] = true
		}
	}
	sort.Strings(scope.DependencyAppIDs)
	return scope, nil
}

func (s *server) getDevBridge(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.devBridgeStore(w)
	if !ok {
		return
	}
	session, err := store.DevBridgeByID(r.Context(), acct.ID, r.PathValue("id"))
	if err != nil {
		s.notFound(w, "no such bridge session")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, session)
}

func (s *server) revokeDevBridge(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.devBridgeStore(w)
	if !ok {
		return
	}
	if err := store.RevokeDevBridge(r.Context(), acct.ID, r.PathValue("id"), time.Now()); err != nil {
		s.notFound(w, "no such bridge session")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
