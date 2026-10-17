// Package deploymentread owns deployment metadata reads and companion projection.
package deploymentread

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Handler receives the authenticated account from existing scope/MFA middleware.
type Handler struct {
	Store    state.Store
	Response func(context.Context, state.Deployment, state.App) api.DeploymentResponse
}

func (h Handler) owned(ctx context.Context, id string, account state.Account) (state.Deployment, state.App, error) {
	if h.Store == nil {
		return state.Deployment{}, state.App{}, errors.New("store unavailable")
	}
	deployment, err := h.Store.DeploymentByID(ctx, id)
	if err != nil {
		return state.Deployment{}, state.App{}, err
	}
	app, err := h.Store.AppByID(ctx, deployment.AppID)
	if err != nil {
		return state.Deployment{}, state.App{}, err
	}
	if app.AccountID != account.ID {
		return state.Deployment{}, state.App{}, state.ErrNotFound
	}
	return deployment, app, nil
}

// Get preserves the existing deployment GET's missing/error and serialization behavior.
func (h Handler) Get(w http.ResponseWriter, r *http.Request, account state.Account) {
	deployment, app, err := h.owned(r.Context(), r.PathValue("id"), account)
	if err != nil {
		httpkit.NotFound(w, "no such deployment")
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, h.Response(r.Context(), deployment, app))
}

// Companions returns declared configuration only, without probing any workload.
func (h Handler) Companions(w http.ResponseWriter, r *http.Request, account state.Account) {
	if r.URL.RawQuery != "" {
		api.WriteProblem(w, api.ErrValidation("companion metadata is scoped by the exact deployment ID; query scope is not supported"))
		return
	}
	deployment, app, err := h.owned(r.Context(), r.PathValue("id"), account)
	if errors.Is(err, state.ErrNotFound) {
		httpkit.NotFound(w, "no such deployment")
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("deployment declarations could not be read"))
		return
	}
	status, reason, companions := projectCompanions(deployment.Sidecars)
	httpkit.WriteJSON(w, http.StatusOK, api.DeploymentCompanionsResponse{AccountID: account.ID, AppID: app.ID, AppSlug: app.Slug, DeploymentID: deployment.ID, Scope: deployment.Scope, State: status, Reason: reason, Companions: companions})
}
