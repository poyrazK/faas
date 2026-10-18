package mcpreadiness

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h *Handler) PagerDuty(w http.ResponseWriter, r *http.Request, account state.Account) {
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
	source, ok := h.Store.(state.MCPReadinessPagerDutyStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("PagerDuty delivery store unavailable"))
		return
	}
	current, err := source.GetMCPReadinessPagerDuty(r.Context(), account.ID, app.ID, id)
	if err != nil {
		problem(w, err)
		return
	}
	if r.Method == http.MethodGet {
		httpkit.WriteJSON(w, http.StatusOK, current)
		return
	}
	h.setPagerDuty(w, r, account, app, source, id)
}
func (h *Handler) setPagerDuty(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, source state.MCPReadinessPagerDutyStore, id string) {
	var request api.SetMCPReadinessPagerDutyRequest
	if !decode(w, r, &request) {
		return
	}
	if api.ValidateMCPReadinessPagerDutyRequest(request) != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	config := state.MCPReadinessPagerDutyConfig{Enabled: *request.Enabled, Region: request.Region}
	if config.Enabled {
		sum := sha256.Sum256([]byte(request.IntegrationKey))
		config.Fingerprint = hex.EncodeToString(sum[:])
		var ok bool
		config.IntegrationKeySealed, ok = h.pagerDutySecret(w, r, request)
		if !ok {
			return
		}
	}
	out, err := source.SetMCPReadinessPagerDuty(r.Context(), account.ID, app.ID, id, *request.ExpectedVersion, config)
	if err != nil {
		problem(w, err)
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), "mcp.readiness_pagerduty.updated", &account.ID, map[string]any{"app_id": app.ID, "rule_id": id, "version": out.Version, "enabled": out.Enabled, "provider": "pagerduty", "region": out.Region})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
func (h *Handler) pagerDutySecret(w http.ResponseWriter, r *http.Request, request api.SetMCPReadinessPagerDutyRequest) ([]byte, bool) {
	if h.PagerDutySeal == nil {
		api.WriteProblem(w, api.ErrCapacity("PagerDuty encryption unavailable"))
		return nil, false
	}
	sealed, p := h.PagerDutySeal(r.Context(), request.IntegrationKey)
	if p != nil {
		api.WriteProblem(w, p)
		return nil, false
	}
	return sealed, true
}
