package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) queuePasswordReset(ctx context.Context, account state.Account, email string) bool {
	if s.passwordResetMailOutboxRequested == nil || !s.passwordResetMailOutboxRequested() {
		return false
	}
	if s.deletionMailOutbox == nil {
		s.log.Error("password_reset.outbox_unavailable")
		return true
	}
	if err := s.deletionMailOutbox.QueuePasswordReset(ctx, account.ID, email); err != nil {
		// Never expose a storage error, token, key or account-discovery result.
		s.log.Error("password_reset.outbox_save_failed", "account_id", account.ID)
	}
	return true
}

func (s *server) applyPasswordReset(ctx context.Context, hash []byte, phc string) (string, *api.Problem) {
	if s.passwordChangedAlertRequested() {
		return s.applyPasswordResetWithAlert(ctx, hash, phc)
	}
	if s.passwordResetCredentials != nil {
		id, err := s.passwordResetCredentials.Redeem(ctx, hash, phc)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, mailoutbox.ErrPasswordResetUnknown) {
			if errors.Is(err, mailoutbox.ErrPasswordResetInvalid) {
				return "", api.ErrResetTokenInvalid()
			}
			s.log.Error("password_reset.transaction_failed")
			return "", api.NewProblem(http.StatusInternalServerError, "internal_error", "Internal Error", "failed to reset password")
		}
	}
	// Compatibility for links minted by the existing inline flow. New durable
	// reset hashes live only in password_reset_tokens, never login_tokens.
	id, err := s.store.ConsumeLoginToken(ctx, hash)
	if err != nil {
		return "", api.ErrResetTokenInvalid()
	}
	if err := s.store.SetAccountPassword(ctx, id, phc); err != nil {
		s.log.Error("reset.set_password")
		return "", api.NewProblem(http.StatusInternalServerError, "internal_error", "Internal Error", "failed to set password")
	}
	return id, nil
}
