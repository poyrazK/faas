package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)

// endPendingGrants ends the grants an account started that would outlive a
// recovery: an unconfirmed email change, org invitations it created, and CLI
// or agent codes approved but not yet exchanged (ADR 20261109120000). Every
// step that ends an account's sessions calls it, so a stolen session or a
// squatter cannot park one of them and redeem it after the owner recovers.
// Best-effort, like the session revocation beside it: a failure is logged.
func (s *server) endPendingGrants(ctx context.Context, accountID, reason string) {
	st, ok := s.store.(state.AccountPendingGrantsStore)
	if !ok || accountID == "" {
		return
	}
	n, err := st.EndAccountPendingGrants(ctx, accountID)
	if err != nil {
		s.log.Warn("account.pending_grants_end_failed", "account_id", accountID, "reason", reason, "err", err)
		return
	}
	if n.Any() {
		s.audit.Emit(ctx, "account.pending_grants_ended", &accountID, map[string]any{
			"reason":        reason,
			"email_changes": n.EmailChanges,
			"invitations":   n.Invitations,
			"cli_codes":     n.CliCodes,
		})
	}
}
