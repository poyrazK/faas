package mcpreadiness

import (
	"net/http"
	"net/url"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) pagerDutyAcknowledgementScope(w http.ResponseWriter, r *http.Request, account state.Account) (state.MCPReadinessPagerDutyAcknowledgementStore, state.App, string, bool) {
	w.Header().Set("Cache-Control", "no-store")
	app, err := h.Store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != account.ID {
		problem(w, state.ErrNotFound)
		return nil, app, "", false
	}
	if h.Loader != nil {
		if _, ok := h.Loader.LoadApp(w, r, account, app.Slug); !ok {
			return nil, app, "", false
		}
	}
	id := r.PathValue("id")
	if uuid.Validate(id) != nil {
		problem(w, state.ErrInvalidArgument)
		return nil, app, "", false
	}
	source, ok := h.Store.(state.MCPReadinessPagerDutyAcknowledgementStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("PagerDuty acknowledgement store unavailable"))
		return nil, app, "", false
	}
	return source, app, id, true
}
func (h *Handler) PagerDutyAcknowledgements(w http.ResponseWriter, r *http.Request, account state.Account) {
	source, app, id, ok := h.pagerDutyAcknowledgementScope(w, r, account)
	if !ok {
		return
	}
	destination, valid := pagerDutyAcknowledgementQuery(r)
	if !valid {
		problem(w, state.ErrInvalidArgument)
		return
	}
	current, err := source.GetMCPReadinessPagerDutyAcknowledgements(r.Context(), account.ID, app.ID, id, destination)
	if err != nil {
		problem(w, err)
		return
	}
	if r.Method == http.MethodGet {
		httpkit.WriteJSON(w, http.StatusOK, current)
		return
	}
	h.setPagerDutyAcknowledgements(w, r, account, app, id, source)
}
func (h *Handler) setPagerDutyAcknowledgements(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, id string, source state.MCPReadinessPagerDutyAcknowledgementStore) {
	var request api.SetMCPReadinessPagerDutyAcknowledgementsRequest
	if !decode(w, r, &request) {
		return
	}
	if api.ValidateMCPReadinessPagerDutyAcknowledgementsRequest(request) != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	config := state.MCPReadinessPagerDutyAcknowledgementConfig{SyncUnacknowledgements: request.SyncUnacknowledgements, Destination: request.Destination, Enabled: *request.Enabled, ServiceID: request.ServiceID}
	if config.Enabled {
		if request.ExpectedPagerDutyVersion != nil {
			config.ExpectedPagerDutyVersion = *request.ExpectedPagerDutyVersion
		}
		if request.ExpectedFallbackVersion != nil {
			config.ExpectedFallbackVersion = *request.ExpectedFallbackVersion
		}
		if h.PagerDutyAcknowledgementSeal == nil {
			api.WriteProblem(w, api.ErrCapacity("PagerDuty webhook encryption unavailable"))
			return
		}
		var p *api.Problem
		config.SigningSecretSealed, p = h.PagerDutyAcknowledgementSeal(r.Context(), request.SigningSecret)
		if p != nil {
			api.WriteProblem(w, p)
			return
		}
	}
	out, err := source.SetMCPReadinessPagerDutyAcknowledgements(r.Context(), account.ID, app.ID, id, *request.ExpectedVersion, config)
	if err != nil {
		problem(w, err)
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), "mcp.readiness_pagerduty_acknowledgements.updated", &account.ID, map[string]any{"app_id": app.ID, "rule_id": id, "sync_unacknowledgements": out.SyncUnacknowledgements, "version": out.Version, "enabled": out.Enabled, "pagerduty_version": out.PagerDutyVersion, "destination": out.Destination, "fallback_version": out.FallbackVersion, "region": out.Region, "service_id": out.ServiceID})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
func (h *Handler) PagerDutyAcknowledgementEvents(w http.ResponseWriter, r *http.Request, account state.Account) {
	source, app, id, ok := h.pagerDutyAcknowledgementScope(w, r, account)
	if !ok {
		return
	}
	query, limit, _, err := attentionQuery(r, id)
	if err != nil {
		problem(w, err)
		return
	}
	out, err := source.ListMCPReadinessPagerDutyAcknowledgementEvents(r.Context(), account.ID, app.ID, id, query.Get("before"), limit)
	if err != nil {
		problem(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

func pagerDutyAcknowledgementQuery(r *http.Request) (string, bool) {
	if r.Method != http.MethodGet {
		return "", r.URL.RawQuery == ""
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) > 1 {
		return "", false
	}
	if len(q) == 0 {
		return "", true
	}
	d := q.Get("destination")
	return d, len(q["destination"]) == 1 && (d == "primary" || d == "fallback")
}
