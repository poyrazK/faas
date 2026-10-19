package mcpreadiness

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) Incidents(w http.ResponseWriter, r *http.Request, account state.Account) {
	w.Header().Set("Cache-Control", "no-store")
	app, err := h.Store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != account.ID {
		problem(w, state.ErrNotFound)
		return
	}
	if h.Loader != nil {
		if _, ok := h.Loader.LoadApp(w, r, account, app.Slug); !ok {
			return
		}
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	for k, v := range q {
		if len(v) != 1 || k != "scope" && k != "before" && k != "limit" {
			problem(w, state.ErrInvalidArgument)
			return
		}
	}
	scope := q.Get("scope")
	if q.Has("scope") && api.ValidateScope(scope) != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	limit := 20
	if q.Has("limit") {
		limit, err = strconv.Atoi(q.Get("limit"))
		if err != nil {
			problem(w, state.ErrInvalidArgument)
			return
		}
	}
	if limit < 1 || limit > 50 {
		problem(w, state.ErrInvalidArgument)
		return
	}
	if _, _, err = api.ParseMCPReadinessIncidentCursor(q.Get("before")); err != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	source, ok := h.Store.(state.MCPReadinessIncidentStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("incident store unavailable"))
		return
	}
	page, err := source.ListMCPReadinessIncidents(r.Context(), account.ID, app.ID, scope, q.Get("before"), limit)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, page)
}
