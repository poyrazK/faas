// Package agentinspect owns read-only native policy and app cost projections.
// adr: 20261103211147
package agentinspect

import (
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

type Deps struct {
	Store     state.Store
	Inventory func(*http.Request, state.AppResourceInventoryRequest) (state.AppResourceInventoryPage, error)
	Resolve   func(http.ResponseWriter, *http.Request, state.Account) (state.App, state.ProjectEnvironment, bool)
	Released  func(*http.Request, state.Account, state.App, string) bool
}

func Preview(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		w.Header().Set("Cache-Control", "private, no-store")
		q, problem := query(r, "operation", "environment")
		if problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		operation := q.Get("operation")
		if !api.ValidAppOperation(operation) {
			api.WriteProblem(w, api.ErrValidation("operation must be deploy, restart, scale or env_change"))
			return
		}
		app, env, ok := d.Resolve(w, r, account)
		if !ok {
			return
		}
		out := preview(d, r, account, app, env, operation)
		if problem := recheck(d, r, app, "GET /v1/apps/{slug}/operation-preview"); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

func query(r *http.Request, allowed ...string) (url.Values, *api.Problem) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, api.ErrValidation("invalid inspection query")
	}
	for key, values := range q {
		found := false
		for _, name := range allowed {
			found = found || key == name
		}
		if !found || len(values) != 1 {
			return nil, api.ErrValidation("unexpected or repeated inspection parameter")
		}
	}
	return q, nil
}

func preview(d Deps, r *http.Request, account state.Account, app state.App, env state.ProjectEnvironment, operation string) api.AppOperationPreview {
	pattern, scope := operationPolicy(operation)
	checks := map[string]api.AppOperationCheck{
		"scope": {Status: "allowed", Reason: "required_scope_present"}, "role": {Status: "allowed", Reason: "native_app_role_authorized"},
		"email": {Status: "allowed", Reason: "not_required"}, "account": {Status: "allowed", Reason: "account_in_good_standing"},
		"environment": {Status: "allowed", Reason: "selected_scope_compatible"}, "capability": {Status: "allowed", Reason: "native_route_released"},
		"quota":   {Status: "unknown", Reason: "exact_request_and_native_admission_required"},
		"request": {Status: "unknown", Reason: "apply_revalidates_parameters_and_current_state"},
	}
	if !authmw.HasScope(r, api.ScopeAdmin, scope) {
		checks["scope"] = api.AppOperationCheck{Status: "blocked", Reason: "scope_required"}
	}
	probe := r.Clone(r.Context())
	probe.Method = strings.SplitN(pattern, " ", 2)[0]
	actor, _, _, _ := authmw.PrincipalFrom(r)
	_, problem := appaccess.AuthorizeRoute(probe, d.Store, nil, actor, app, pattern)
	if problem != nil {
		checks["role"] = problemCheck(problem)
	}
	if operation == "deploy" && !actor.EmailVerified() {
		checks["email"] = api.AppOperationCheck{Status: "blocked", Reason: api.CodeEmailVerificationRequired}
	}
	if operation == "deploy" && !account.MayDeploy() {
		checks["account"] = problemCheck(account.DeployBlockedProblem())
	}
	if operation != "deploy" && !account.Active() {
		checks["account"] = problemCheck(account.InactiveProblem())
	}
	if d.Released != nil && !d.Released(r, account, app, pattern) {
		checks["capability"] = api.AppOperationCheck{Status: "blocked", Reason: "capability_not_released"}
	}
	if env.Protected {
		checks["environment"] = api.AppOperationCheck{Status: "blocked", Reason: "approval_required"}
	}
	if operation == "restart" {
		if env.ID != "" {
			checks["environment"] = api.AppOperationCheck{Status: "blocked", Reason: "restart_is_app_wide"}
		}
		if app.Status != state.AppActive {
			checks["application"] = api.AppOperationCheck{Status: "blocked", Reason: "app_not_active"}
		}
	}
	if operation == "scale" && env.ID != "" && state.IsProductionScope(env.Slug) {
		checks["environment"] = api.AppOperationCheck{Status: "blocked", Reason: "use_unscoped_production_settings"}
	}
	if _, ok := api.LimitsFor(account.Plan); !ok {
		checks["plan"] = api.AppOperationCheck{Status: "unknown", Reason: "plan_unavailable"}
	}
	scopeName := api.DefaultEnvScope
	if env.ID != "" {
		scopeName = env.Slug
	}
	return api.AppOperationPreview{SchemaVersion: 1, AppID: app.ID, Scope: scopeName, ProjectID: env.ProjectID, EnvironmentID: env.ID, Operation: operation,
		Authorization: decision(checks, "scope", "role", "email"), Admission: decision(checks), RequiredScopes: []string{scope}, Checks: checks, CheckedAt: time.Now().UTC()}
}

func operationPolicy(operation string) (string, string) {
	switch operation {
	case "env_change":
		return "POST /v1/apps/{slug}/env/batch", api.ScopeEnvWrite
	case "scale":
		return "PATCH /v1/apps/{slug}", api.ScopeDeployWrite
	case "restart":
		return "POST /v1/apps/{slug}/restart", api.ScopeDeployWrite
	default:
		return "POST /v1/apps/{slug}/deployments", api.ScopeDeployWrite
	}
}

func problemCheck(p *api.Problem) api.AppOperationCheck {
	status := "blocked"
	if p.Status >= 500 {
		status = "unknown"
	}
	return api.AppOperationCheck{Status: status, Reason: p.Code}
}

func decision(checks map[string]api.AppOperationCheck, names ...string) string {
	if len(names) == 0 {
		for name := range checks {
			names = append(names, name)
		}
	}
	result := "allowed"
	for _, name := range names {
		if checks[name].Status == "blocked" {
			return "blocked"
		}
		if checks[name].Status != "allowed" {
			result = "unknown"
		}
	}
	return result
}

func recheck(d Deps, r *http.Request, app state.App, pattern string) *api.Problem {
	fresh, err := d.Store.AppBySlug(r.Context(), app.Slug)
	if errors.Is(err, state.ErrNotFound) {
		return api.NewProblem(404, api.CodeNotFound, "Not found", "no such app")
	}
	if err != nil {
		return api.ErrCapacity("could not recheck application access")
	}
	if fresh.ID != app.ID || fresh.AccountID != app.AccountID || fresh.OrgID != app.OrgID || fresh.ProjectID != app.ProjectID {
		return api.NewProblem(404, api.CodeNotFound, "Not found", "application identity changed")
	}
	actor, _, _, _ := authmw.PrincipalFrom(r)
	_, p := appaccess.AuthorizeRoute(r.Clone(r.Context()), d.Store, nil, actor, fresh, pattern)
	return p
}
