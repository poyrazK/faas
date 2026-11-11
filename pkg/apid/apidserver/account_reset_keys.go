package apidserver

import (
	"context"
	"fmt"
	"time"

	mailpkg "github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// A password reset revokes the keys sign-ins minted but keeps the keys the
// owner created by hand, which run CI and deploys. Someone who had the
// account before the reset may have created one of those too, so the owner
// is shown them: in a mail and on the account page (ADR 20261109120000).

// keysLeftAfterReset lists acct's live keys a reset did not revoke.
func (s *server) keysLeftAfterReset(ctx context.Context, acct state.Account) []state.APIKey {
	keys, err := s.store.ListAPIKeys(ctx, acct.ID)
	if err != nil {
		s.log.Warn("reset.list_keys", "account_id", acct.ID, "err", err)
		return nil
	}
	now := time.Now()
	var out []state.APIKey
	for _, k := range keys {
		if k.Status == "revoked" || interactiveSignInKeyLabels[k.Label] || (k.ExpiresAt != nil && !k.ExpiresAt.After(now)) {
			continue
		}
		out = append(out, k)
	}
	return out
}

// mailKeysLeftAfterReset mails the list, or nothing when there are none.
func (s *server) mailKeysLeftAfterReset(ctx context.Context, acct state.Account, keys []state.APIKey) {
	if len(keys) == 0 {
		return
	}
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		label := k.Label
		if label == "" {
			label = "unlabelled key " + k.ID
		}
		lines = append(lines, fmt.Sprintf("%s (created %s)", label, k.CreatedAt.UTC().Format("2006-01-02")))
	}
	subject, body := mailpkg.KeysAfterResetBody(acct.Email, lines, wire.DashboardAccountURL)
	s.sendSecurityNotice(ctx, acct, "reset_keys_review", subject, body)
}
