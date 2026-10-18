package apidserver

import (
	"context"

	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/state"
)

// The chosen path is returned with the consume result, so a flag change after
// commit cannot send an inline duplicate of an already saved security alert.
func (s *server) consumeMFARecoveryWithAlert(ctx context.Context, account state.Account, presented []byte) (matched, lastCode bool, remaining int, outboxed bool, err error) {
	if s.mfaRecoveryAlertOutboxRequested == nil || !s.mfaRecoveryAlertOutboxRequested() {
		matched, _, remaining, err = consumeRecoveryCode(ctx, s.store, account.ID, presented)
		return matched, false, remaining, false, err
	}
	if s.deletionMailOutbox == nil {
		return false, false, 0, true, internalmail.ErrDisabled
	}
	matched, lastCode, remaining, err = s.deletionMailOutbox.ConsumeMFARecoveryAlert(ctx, account.ID, account.Email, presented)
	return matched, lastCode, remaining, true, err
}
