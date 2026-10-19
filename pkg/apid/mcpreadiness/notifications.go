package mcpreadiness

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) Notifications(w http.ResponseWriter, r *http.Request, account state.Account) {
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
	source, ok := h.Store.(state.MCPReadinessNotificationStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("notification queue store unavailable"))
		return
	}
	if r.Method == http.MethodGet {
		h.notificationQueues(w, r, account, app, source)
		return
	}
	if r.URL.RawQuery != "" {
		problem(w, state.ErrInvalidArgument)
		return
	}
	h.notificationDecision(w, r, account, app, source)
}
func (h *Handler) notificationQueues(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, source state.MCPReadinessNotificationStore) {
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	for k, v := range q {
		if len(v) != 1 || k != "scope" && k != "after" && k != "limit" {
			problem(w, state.ErrInvalidArgument)
			return
		}
	}
	if q.Has("scope") && api.ValidateScope(q.Get("scope")) != nil {
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
	page, err := source.ListMCPReadinessNotificationQueues(r.Context(), account.ID, app.ID, q.Get("scope"), q.Get("after"), limit, time.Now().UTC())
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, page)
}
func (h *Handler) notificationDecision(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, source state.MCPReadinessNotificationStore) {
	var request api.MCPReadinessNotificationAction
	if !decode(w, r, &request) {
		return
	}
	receipt, err := source.ActMCPReadinessNotification(r.Context(), account.ID, app.ID, request, time.Now().UTC())
	if errors.Is(err, state.ErrConflict) {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConflict, "Notification action conflicts with current queue", "Refresh queue health. Actions require the current head version, an enabled rule, no active lease or maintenance suppression, and a matching request ID on retries."))
		return
	}
	if err != nil {
		problem(w, err)
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), "mcp.readiness_notification."+receipt.Action, &account.ID, map[string]any{"app_id": app.ID, "event_id": receipt.EventID, "destination": receipt.Destination, "request_id": receipt.RequestID, "reason": receipt.Reason, "delivery_version": receipt.DeliveryVersion})
	}
	httpkit.WriteJSON(w, http.StatusOK, receipt)
}
