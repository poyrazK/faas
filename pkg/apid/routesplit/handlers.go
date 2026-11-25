package routesplit

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Handlers serve /v1/apps/{slug}/route-splits.
type Handlers struct {
	Service *Service
	// LoadApp resolves {slug} for the account and writes the problem when
	// it cannot.
	LoadApp func(w http.ResponseWriter, r *http.Request, acct state.Account, slug string) (state.App, bool)
	// Enabled reports whether route resource peaks are released to the
	// account; splits are off with them.
	Enabled func(acct state.Account) bool
}

func (h Handlers) app(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool) {
	w.Header().Set("Cache-Control", "no-store")
	if h.Enabled != nil && !h.Enabled(acct) {
		httpkit.NotFound(w, "route resource peaks are not enabled for this account")
		return state.App{}, false
	}
	return h.LoadApp(w, r, acct, r.PathValue("slug"))
}

// Create is POST /v1/apps/{slug}/route-splits.
func (h Handlers) Create(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := h.app(w, r, acct)
	if !ok {
		return
	}
	var req api.CreateRouteSplitRequest
	if err := httpkit.DecodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.Kind != "memory" && req.Kind != "cpu" {
		api.WriteProblem(w, api.ErrValidation("kind must be memory or cpu"))
		return
	}
	split, prob := h.Service.Apply(r.Context(), acct, app, req)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	httpkit.WriteJSON(w, http.StatusAccepted, Response(split, app.Slug))
}

// List is GET /v1/apps/{slug}/route-splits.
func (h Handlers) List(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := h.app(w, r, acct)
	if !ok {
		return
	}
	splits, err := h.Service.Store.RouteSplitsForApp(r.Context(), app.AccountID, app.ID, api.RouteSplitsListMax)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the app's splits"))
		return
	}
	out := api.RouteSplitsResponse{Splits: make([]api.RouteSplitResponse, 0, len(splits))}
	for _, split := range splits {
		out.Splits = append(out.Splits, Response(split, app.Slug))
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// Get is GET /v1/apps/{slug}/route-splits/{id}.
func (h Handlers) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, split, ok := h.split(w, r, acct)
	if !ok {
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, Response(split, app.Slug))
}

// Undo is DELETE /v1/apps/{slug}/route-splits/{id}.
func (h Handlers) Undo(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, split, ok := h.split(w, r, acct)
	if !ok {
		return
	}
	out, prob := h.Service.Undo(r.Context(), acct, app, split)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, Response(out, app.Slug))
}

func (h Handlers) split(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, state.RouteSplit, bool) {
	app, ok := h.app(w, r, acct)
	if !ok {
		return state.App{}, state.RouteSplit{}, false
	}
	split, err := h.Service.Store.RouteSplit(r.Context(), app.AccountID, r.PathValue("id"))
	if errors.Is(err, state.ErrNotFound) || (err == nil && split.SourceAppID != app.ID) {
		httpkit.NotFound(w, "no such route split")
		return state.App{}, state.RouteSplit{}, false
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the split"))
		return state.App{}, state.RouteSplit{}, false
	}
	return app, split, true
}
