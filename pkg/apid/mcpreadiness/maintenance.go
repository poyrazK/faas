package mcpreadiness

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

// Maintenance shares the owned-app loader used by policy and incident routes.
// The router requires admin scope and MFA for changes; reads use read scopes.
func (h *Handler) Maintenance(w http.ResponseWriter, r *http.Request, account state.Account) {
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
	source, ok := h.Store.(state.MCPReadinessMaintenanceStore)
	if !ok {
		problem(w, state.ErrInvalidArgument)
		return
	}
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	for k, v := range query {
		if r.Method != http.MethodGet || len(v) != 1 || k != "scope" && k != "before" && k != "limit" {
			problem(w, state.ErrInvalidArgument)
			return
		}
	}
	if r.Method == http.MethodGet {
		scope := api.MCPConnectionScope(query.Get("scope"))
		if query.Has("scope") && api.ValidateScope(query.Get("scope")) != nil {
			problem(w, state.ErrInvalidArgument)
			return
		}
		limit := 20
		if query.Has("limit") {
			limit, err = strconv.Atoi(query.Get("limit"))
			if err != nil {
				problem(w, state.ErrInvalidArgument)
				return
			}
		}
		page, err := source.ListMCPReadinessMaintenance(r.Context(), account.ID, app.ID, scope, query.Get("before"), limit)
		if err != nil {
			problem(w, err)
			return
		}
		httpkit.WriteJSON(w, 200, page)
		return
	}
	now := time.Now().UTC()
	var window api.MCPReadinessMaintenanceWindow
	action := "mcp.readiness_maintenance.created"
	if id := r.PathValue("id"); id != "" {
		if uuid.Validate(id) != nil {
			problem(w, state.ErrInvalidArgument)
			return
		}
		var request api.CancelMCPReadinessMaintenanceRequest
		if !decode(w, r, &request) {
			return
		}
		window, err = source.CancelMCPReadinessMaintenance(r.Context(), account.ID, app.ID, id, request.Reason, now)
		action = "mcp.readiness_maintenance.cancelled"
	} else {
		var request api.CreateMCPReadinessMaintenanceRequest
		if !decode(w, r, &request) {
			return
		}
		window, err = source.CreateMCPReadinessMaintenance(r.Context(), account.ID, app.ID, request, now)
	}
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Maintenance window expired", "Expired windows remain in history and cannot be cancelled"))
		return
	}
	if err != nil {
		problem(w, err)
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), action, &account.ID, map[string]any{"app_id": app.ID, "maintenance_id": window.ID, "scope": window.Scope, "reason": window.Reason, "cancel_reason": window.CancelReason})
	}
	status := http.StatusCreated
	if action == "mcp.readiness_maintenance.cancelled" {
		status = http.StatusOK
	}
	httpkit.WriteJSON(w, status, window)
}

// Kept as a named type so callers can use their existing audit sink directly.
type MaintenanceAudit func(context.Context, string, *string, map[string]any)
