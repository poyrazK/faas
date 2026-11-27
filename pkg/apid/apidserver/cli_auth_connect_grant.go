package apidserver

import (
	"net/http"

	"github.com/onebox-faas/faas/pkg/state"
)

// recordConnectGrant records that the person who approved a hosted MCP
// sign-in granted its Connect approval (ADR-20261127160000). It fails safe:
// without the row the connection keeps asking for approvals.
func (h *cliAuthHandlers) recordConnectGrant(r *http.Request, keyID, accountID string) {
	store, ok := h.srv.store.(state.AgentConnectGrantStore)
	if !ok {
		return
	}
	if err := store.CreateAPIKeyConnectGrant(r.Context(), keyID, accountID); err != nil {
		h.log.Error("cli_auth.connect_grant", "key", keyID, "err", err)
		return
	}
	h.srv.audit.Emit(r.Context(), "agent_approval.connect_granted", &accountID, map[string]any{
		"key_id":     keyID,
		"granted_by": accountID,
	})
}
