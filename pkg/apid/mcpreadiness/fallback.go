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

func (h *Handler) Fallback(w http.ResponseWriter, r *http.Request, account state.Account) {
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
	source, ok := h.Store.(state.MCPReadinessFallbackStore)
	if !ok {
		api.WriteProblem(w, api.ErrInternal("fallback delivery store unavailable"))
		return
	}
	current, err := source.GetMCPReadinessFallback(r.Context(), account.ID, app.ID, id)
	if err != nil {
		problem(w, err)
		return
	}
	if r.Method == http.MethodGet {
		httpkit.WriteJSON(w, http.StatusOK, current)
		return
	}
	h.setFallback(w, r, account, app, source, id)
}
func (h *Handler) setFallback(w http.ResponseWriter, r *http.Request, account state.Account, app state.App, source state.MCPReadinessFallbackStore, id string) {
	var request api.SetMCPReadinessFallbackRequest
	if !decode(w, r, &request) {
		return
	}
	if api.ValidateMCPReadinessFallbackRequest(request) != nil {
		problem(w, state.ErrInvalidArgument)
		return
	}
	config, ok := h.fallbackConfig(w, r, request)
	if !ok {
		return
	}
	out, err := source.SetMCPReadinessFallback(r.Context(), account.ID, app.ID, id, *request.ExpectedVersion, config)
	if err != nil {
		problem(w, err)
		return
	}
	if h.Audit != nil {
		h.Audit(r.Context(), "mcp.readiness_fallback.updated", &account.ID, map[string]any{"app_id": app.ID, "rule_id": id, "version": out.Version, "enabled": out.Enabled, "after_failures": out.AfterFailures, "provider": out.Provider, "region": out.Region})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}
func (h *Handler) fallbackSecret(w http.ResponseWriter, r *http.Request, request api.SetMCPReadinessFallbackRequest) ([]byte, bool) {
	if h.FallbackURL == nil || h.FallbackSeal == nil {
		api.WriteProblem(w, api.ErrCapacity("fallback signing configuration unavailable"))
		return nil, false
	}
	if p := h.FallbackURL(r.Context(), request.WebhookURL); p != nil {
		api.WriteProblem(w, p)
		return nil, false
	}
	sealed, p := h.FallbackSeal(r.Context(), request.WebhookSecret)
	if p != nil {
		api.WriteProblem(w, p)
		return nil, false
	}
	return sealed, true
}

func (h *Handler) fallbackConfig(w http.ResponseWriter, r *http.Request, request api.SetMCPReadinessFallbackRequest) (state.MCPReadinessFallbackConfig, bool) {
	config := state.MCPReadinessFallbackConfig{Provider: request.Provider, Enabled: *request.Enabled, WebhookURL: request.WebhookURL, AfterFailures: request.AfterFailures, Region: request.Region}
	if !config.Enabled {
		return config, true
	}
	if request.Provider == "pagerduty" {
		sum := sha256.Sum256([]byte(request.IntegrationKey))
		config.Fingerprint = hex.EncodeToString(sum[:])
		sealed, ok := h.pagerDutySecret(w, r, api.SetMCPReadinessPagerDutyRequest{IntegrationKey: request.IntegrationKey})
		config.IntegrationKeySealed = sealed
		return config, ok
	}
	sealed, ok := h.fallbackSecret(w, r, request)
	config.WebhookSecretSealed = sealed
	return config, ok
}
