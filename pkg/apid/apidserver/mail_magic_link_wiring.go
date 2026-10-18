package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) queueMagicLink(ctx context.Context, account state.Account, email string) bool {
	if s.magicLinkMailOutboxRequested == nil || !s.magicLinkMailOutboxRequested() {
		return false
	}
	if s.deletionMailOutbox == nil {
		s.log.Error("magic_link.outbox_unavailable")
		return true
	}
	if err := s.deletionMailOutbox.QueueMagicLink(ctx, account.ID, email); err != nil {
		s.log.Error("magic_link.outbox_save_failed", "account_id", account.ID)
	}
	return true
}

func (s *server) consumeMagicLink(ctx context.Context, hash []byte) (string, *api.Problem) {
	if s.magicLinkCredentials != nil {
		result, err := s.magicLinkCredentials.Redeem(ctx, hash)
		if err == nil {
			if result.RevokedSessions > 0 || result.RevokedAPIKeys > 0 {
				s.audit.Emit(ctx, "auth.pre_verification_credentials_revoked", &result.AccountID, map[string]any{
					"sessions": result.RevokedSessions, "api_keys": result.RevokedAPIKeys,
				})
			}
			return result.AccountID, nil
		}
		if !errors.Is(err, mailoutbox.ErrMagicLinkUnknown) {
			if errors.Is(err, mailoutbox.ErrMagicLinkInvalid) {
				return "", api.ErrVerificationLinkInvalid()
			}
			s.log.Error("magic_link.transaction_failed")
			return "", api.NewProblem(http.StatusInternalServerError, "internal_error", "Internal Error", "failed to verify sign-in link")
		}
	}
	// Only old inline links enter this table. Durable reset and magic-link
	// credentials have separate purposes and never reach this fallback.
	id, err := s.store.ConsumeLoginToken(ctx, hash)
	if err != nil {
		return "", api.ErrVerificationLinkInvalid()
	}
	return id, nil
}

func (s *server) magicLinkAccount(ctx context.Context, hash []byte) (state.Account, *api.Problem) {
	id, problem := s.consumeMagicLink(ctx, hash)
	if problem != nil {
		return state.Account{}, problem
	}
	account, err := s.store.AccountByID(ctx, id)
	if err != nil {
		return state.Account{}, api.ErrVerificationLinkInvalid()
	}
	account, err = s.claimAccountByEmailOwnership(ctx, account)
	if err != nil {
		s.log.Error("auth.verify.mark_email_verified", "account", id)
		return state.Account{}, api.ErrInternal("Gregale could not verify this email address.")
	}
	return account, nil
}
