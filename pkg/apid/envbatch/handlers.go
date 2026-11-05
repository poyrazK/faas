// Package envbatch owns the atomic non-secret configuration endpoint.
package envbatch

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

type Deps struct {
	Store   state.EnvBatchStore
	Resolve func(http.ResponseWriter, *http.Request, state.Account) (state.App, string, bool)
	Guard   func(*http.Request, state.App, string) bool
	Actor   func(*http.Request, state.Account) string
	After   func(*http.Request, state.Account, state.App, string, api.EnvBatchRequest, api.EnvBatchResponse)
}

func Apply(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, account state.Account) {
		app, scope, ok := d.Resolve(w, r, account)
		if !ok {
			return
		}
		if d.Store == nil {
			api.WriteProblem(w, api.ErrCapacity("atomic configuration is unavailable"))
			return
		}
		var request api.EnvBatchRequest
		if httpkit.DecodeJSON(r, &request) != nil {
			api.WriteProblem(w, api.ErrValidation("invalid variable batch"))
			return
		}
		limits, knownPlan := api.LimitsFor(account.Plan)
		if !knownPlan {
			api.WriteProblem(w, api.ErrCapacity("unknown plan"))
			return
		}
		if problem := request.Validate(limits); problem != nil {
			api.WriteProblem(w, problem)
			return
		}
		out, err := d.Store.ApplyEnvBatch(r.Context(), state.EnvBatchChange{AccountID: account.ID, AppID: app.ID, Scope: scope,
			Actor: d.Actor(r, account), Guard: d.Guard(r, app, scope), Limits: limits, Request: request})
		if err != nil {
			writeError(w, err, limits)
			return
		}
		if d.After != nil {
			d.After(r, account, app, scope, request, out)
		}
		httpkit.WriteJSON(w, http.StatusOK, out)
	}
}

func writeError(w http.ResponseWriter, err error, limits api.Limits) {
	var quota *state.EnvBatchQuotaError
	switch {
	case errors.Is(err, state.ErrEnvBatchRevisionConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeEnvBatchRevisionConflict, "Configuration changed", "Review the current revision before planning another batch"))
	case errors.Is(err, state.ErrEnvBatchRequestConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeEnvBatchRequestConflict, "Request ID already used", "Reuse the original request body or select a new request ID"))
	case errors.As(err, &quota):
		api.WriteProblem(w, api.ErrPlanLimitEnvVars(limits, quota.Observed))
	case errors.Is(err, state.ErrEnvironmentGitManaged):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, "environment_field_git_managed", "Configuration is managed", "Use the owning environment GitOps workflow"))
	case errors.Is(err, state.ErrNotFound):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "App not found", "The app is no longer available"))
	default:
		api.WriteProblem(w, api.ErrCapacity("could not commit the configuration batch; retry the original request ID"))
	}
}
