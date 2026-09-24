package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// updateProjectEnvironmentRoutes replaces one workload's scoped route
// contract. Application-wide routes remain the fallback until the first write
// or an environment clone snapshots them.
func (s *server) updateProjectEnvironmentRoutes(w http.ResponseWriter, r *http.Request, acct state.Account) {
	project, environment, _, problem := s.loadProjectEnvironmentConfig(r.Context(), acct, r.PathValue("slug"), r.PathValue("environment"))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	app, problem := s.projectEnvironmentRoutesWorkload(r, acct, project)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	req, problem := decodeProjectEnvironmentRoutesRequest(r)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	policy, err := s.store.PutProjectEnvironmentRoutePolicy(r.Context(), state.ProjectEnvironmentRoutePolicy{
		AccountID: acct.ID, ProjectID: project.ID, AppID: app.ID,
		EnvironmentSlug: environment.Slug, OnlyAllowDeclaredRoutes: *req.OnlyAllowDeclaredRoutes,
		DeclaredRoutes: projectEnvironmentStateRoutes(*req.DeclaredRoutes),
	})
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, projectEnvironmentNotFound(project.Slug, environment.Slug))
		} else {
			api.WriteProblem(w, api.ErrCapacity("could not store environment routes"))
		}
		return
	}
	s.audit.Emit(r.Context(), "project.environment.routes.updated", &acct.ID, map[string]any{
		"project_id": project.ID, "environment": environment.Slug, "app_id": app.ID,
		"only_allow_declared_routes": policy.OnlyAllowDeclaredRoutes, "route_count": len(policy.DeclaredRoutes),
	})
	writeJSON(w, http.StatusOK, api.ProjectEnvironmentRoutePolicyResponse{
		Ownership: "environment", OnlyAllowDeclaredRoutes: policy.OnlyAllowDeclaredRoutes,
		DeclaredRoutes: projectEnvironmentDeclaredRoutes(policy.DeclaredRoutes),
	})
}

func (s *server) projectEnvironmentRoutesWorkload(r *http.Request, acct state.Account, project state.Project) (state.App, *api.Problem) {
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("workload"))
	if errors.Is(err, state.ErrNotFound) || (err == nil && (app.AccountID != acct.ID || app.ProjectID != project.ID || app.Status == state.AppDeleted)) {
		return state.App{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Workload not found", "workload does not belong to this project")
	}
	if err != nil {
		return state.App{}, api.ErrCapacity("could not load project workload")
	}
	return app, nil
}

func decodeProjectEnvironmentRoutesRequest(r *http.Request) (api.UpdateProjectEnvironmentRoutePolicyRequest, *api.Problem) {
	var req api.UpdateProjectEnvironmentRoutePolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		return req, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error())
	}
	if req.OnlyAllowDeclaredRoutes == nil || req.DeclaredRoutes == nil {
		return req, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Incomplete route policy", "only_allow_declared_routes and declared_routes are required")
	}
	if problem := validateProjectEnvironmentRoutes(*req.DeclaredRoutes); problem != nil {
		return req, problem
	}
	if *req.OnlyAllowDeclaredRoutes && len(*req.DeclaredRoutes) == 0 {
		return req, api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation,
			"Explicit routes required", "environment-owned route enforcement requires declared_routes; an application-wide OpenAPI document is not an isolated environment contract")
	}
	return req, nil
}

func projectEnvironmentStateRoutes(rows []api.DeclaredRoute) []state.DeclaredRoute {
	out := make([]state.DeclaredRoute, 0, len(rows))
	for _, row := range rows {
		methods := make([]string, 0, len(row.Methods))
		for _, method := range row.Methods {
			methods = append(methods, strings.ToUpper(strings.TrimSpace(method)))
		}
		out = append(out, state.DeclaredRoute{Path: strings.TrimSpace(row.Path), Methods: methods})
	}
	return out
}

func validateProjectEnvironmentRoutes(routes []api.DeclaredRoute) *api.Problem {
	if len(routes) > 50 {
		return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Too many declared routes", "declared_routes may contain at most 50 entries")
	}
	validMethods := map[string]struct{}{"GET": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {}, "OPTIONS": {}, "HEAD": {}, "CONNECT": {}, "TRACE": {}}
	for _, route := range routes {
		path := strings.TrimSpace(route.Path)
		if path == "" || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "?#") || len(route.Methods) == 0 {
			return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid declared route", fmt.Sprintf("declared route %q needs an absolute path and at least one HTTP method", route.Path))
		}
		for _, method := range route.Methods {
			if _, ok := validMethods[strings.ToUpper(strings.TrimSpace(method))]; !ok {
				return api.NewProblem(http.StatusUnprocessableEntity, api.CodeValidation, "Invalid declared route method", fmt.Sprintf("method %q on declared route %q is not supported", method, path))
			}
		}
	}
	return nil
}
