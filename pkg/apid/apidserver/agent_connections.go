package apidserver

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/apid/agentconnections"
	"github.com/onebox-faas/faas/pkg/state"
)

// agentConnectionDeps wires the connected-agents handlers
// (ADR-20261030133644) to apid's key revocation and audit.
func (s *server) agentConnectionDeps() agentconnections.Deps {
	return agentconnections.Deps{
		Store:    s.store,
		Log:      s.log,
		Prefixes: s.listedKeyPrefixes,
		Revoke: func(r *http.Request, acct state.Account, keyID string) error {
			_, err := s.revokeAPIKeyWithReason(r, acct, keyID, "agent_disconnected")
			return err
		},
		Audit: func(ctx context.Context, kind, accountID string, data map[string]any) {
			s.audit.Emit(ctx, kind, &accountID, data)
		},
	}
}
