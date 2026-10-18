package mcpreadiness

import (
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) ReminderDefaults(w http.ResponseWriter, r *http.Request, account state.Account) {
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
	id := r.PathValue("id")
	if uuid.Validate(id) != nil || r.URL.RawQuery != "" {
		problem(w, state.ErrInvalidArgument)
		return
	}
	source, ok := h.Store.(state.MCPReadinessReminderDefaultsStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("reminder defaults store unavailable"))
		return
	}
	current, err := source.GetMCPReadinessReminderDefaults(r.Context(), account.ID, app.ID, id)
	if err != nil {
		problem(w, err)
		return
	}
	if r.Method == http.MethodGet {
		httpkit.WriteJSON(w, http.StatusOK, current)
		return
	}
	h.setReminderDefaults(w, r, account, app, id, source)
}
func (h *Handler) setReminderDefaults(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, id string, source state.MCPReadinessReminderDefaultsStore) {
	var request api.SetMCPReadinessReminderDefaultsRequest
	if !decode(w, r, &request) {
		return
	}
	if api.ValidateMCPReadinessReminderDefaultsRequest(request) != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	out, err := source.SetMCPReadinessReminderDefaults(r.Context(), account.ID, app.ID, id, *request.ExpectedVersion, *request.ReminderPolicy)
	if err != nil {
		problem(w, err)
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), "mcp.readiness_reminder_defaults.updated", &account.ID, map[string]any{"app_id": app.ID, "rule_id": id, "version": out.Version, "interval_seconds": out.ReminderPolicy.IntervalSeconds, "max_reminders": out.ReminderPolicy.MaxReminders})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
