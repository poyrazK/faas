package ciapi

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/ci"
	"github.com/onebox-faas/faas/pkg/state"
)

func (h Handler) Health(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !h.available(w, acct) {
		return
	}
	store, ok := h.Store.(ci.HealthReader)
	if !ok {
		httpkit.NotFound(w, "CI health is not available")
		return
	}
	health, err := store.CIHealth(r.Context(), acct.ID)
	if err != nil {
		writeError(w, err)
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, map[string]any{
		"health": health,
		"gates": map[string]bool{"internal": h.Config.Enabled, "github": h.Config.GitHubEnabled,
			"dispatch": h.Config.DispatchEnabled, "worker": h.Config.WorkerEnabled, "transport": h.Config.TransportEnabled,
			"retention": h.Config.RetentionEnabled, "automation": h.Config.AutomationEnabled,
			"github_checks": h.Config.GitHubChecksEnabled, "execution_checks": h.Config.ExecutionChecksEnabled,
			"execution_dependencies_ready": h.ExecutionReady},
	})
}
