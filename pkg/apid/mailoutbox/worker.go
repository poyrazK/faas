package mailoutbox

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
)

func (s *Service) claim(ctx context.Context) (q.PlatformMailProducerOutbox, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return q.PlatformMailProducerOutbox{}, err
	}
	defer rollback(ctx, tx)
	if err := s.queries.ShareRecovery(ctx, tx, s.namespace); err != nil {
		return q.PlatformMailProducerOutbox{}, err
	}
	if err := s.queries.Maintain(ctx, tx, q.MaintainParams{Namespace: s.namespace, MaxAttempts: api.InternalMailProducerOutboxMaxAttempts, BatchSize: api.InternalMailProducerOutboxMaintenanceBatch}); err != nil {
		return q.PlatformMailProducerOutbox{}, err
	}
	origin := ""
	keys := []string{}
	resetOrigin := ""
	resetKeys := []string{}
	if s.verificationEnabled() {
		origin = s.verification.origin
		for key := range s.verification.readers {
			keys = append(keys, key)
		}
	}
	if s.passwordResetEnabled() {
		resetOrigin = s.passwordReset.origin
		for key := range s.passwordReset.readers {
			resetKeys = append(resetKeys, key)
		}
	}
	if err := s.queries.PurgeExpiredPasswordResetTokens(ctx, tx, api.InternalMailProducerOutboxMaintenanceBatch); err != nil {
		return q.PlatformMailProducerOutbox{}, err
	}
	r, err := s.queries.Claim(ctx, tx, q.ClaimParams{Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, VerificationEnabled: s.verificationEnabled(), VerificationOrigin: origin, VerificationKeys: keys, PasswordResetEnabled: s.passwordResetEnabled(), PasswordResetOrigin: resetOrigin, PasswordResetKeys: resetKeys, MaxAttempts: api.InternalMailProducerOutboxMaxAttempts, LeaseID: pgID(uuid.NewString()), LeaseSeconds: int32(api.InternalMailProducerOutboxLease / time.Second)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return r, err
	}
	if !s.Enabled() {
		return r, internalmail.ErrDisabled
	}
	if commitErr := tx.Commit(ctx); commitErr != nil {
		return r, commitErr
	}
	return r, err
}

func (s *Service) DeliverOne(ctx context.Context) error {
	if !s.Enabled() {
		return nil
	}
	pass, cancel := context.WithTimeout(ctx, api.InternalMailProducerOutboxPassTimeout)
	defer cancel()
	r, err := s.claim(pass)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.dispatch(pass, r)
}

// Dispatch locks account before outbox and retains no recovery fence across HTTP. The bounded HTTP request runs
// under those locks so restoration cannot commit between lifecycle review and
// admission. A lost response/commit is retried with the same immutable payload.
func (s *Service) dispatch(ctx context.Context, claimed q.PlatformMailProducerOutbox) error {
	if !s.Enabled() {
		return internalmail.ErrDisabled
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	recovery, err := s.queries.Recovery(ctx, tx, s.namespace)
	if err != nil {
		return err
	}
	if recovery.Held || recovery.RecoveryID != claimed.RecoveryID {
		return nil
	}
	account, err := s.queries.ShareAccount(ctx, tx, claimed.AccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	r, err := s.queries.LockIntent(ctx, tx, q.LockIntentParams{Namespace: s.namespace, ID: claimed.ID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	live, err := s.queries.LiveLease(ctx, tx, q.LiveLeaseParams{Namespace: s.namespace, ID: r.ID, LeaseID: claimed.LeaseID})
	if err != nil {
		return err
	}
	if !live {
		return internalmail.ErrLeaseLost
	}
	state, code := "", ""
	refund := false
	switch {
	case !strings.EqualFold(account.Email, r.RecipientAddress):
		state, code = "cancelled", "account_changed"
	case r.Template == "account_deletion_pending" && (account.Status != "deleted_pending" || account.DeletionRequestedAt != r.DeletionRequestedAt):
		state, code = "cancelled", "account_changed"
	case r.Template == VerificationTemplate && (account.Status != "active" || account.EmailVerifiedAt.Valid):
		state, code = "cancelled", "token_unavailable"
	case r.Template == PasswordResetTemplate && (account.Status != "active" || !account.EmailVerifiedAt.Valid):
		state, code = "cancelled", "token_unavailable"
	case !r.ExpiresAt.Time.After(time.Now()):
		state, code = "expired", "notification_expired"
	default:
		suppressed, err := s.queries.Suppressed(ctx, tx, r.RecipientAddress)
		if err != nil {
			return err
		}
		if suppressed {
			state, code = "suppressed", "platform_suppressed"
		} else {
			if !s.Enabled() {
				return internalmail.ErrDisabled
			}
			if r.ProducerID != s.producer || r.FromAddress != s.from || r.TemplateVersion != 1 {
				return internalmail.ErrInvalid
			}
			state, code, refund, err = s.handoff(ctx, tx, r, account.PasswordResetRevision)
			if err != nil {
				return err
			}
		}
	}
	_, err = s.queries.Settle(ctx, tx, q.SettleParams{Namespace: s.namespace, ID: r.ID, LeaseID: claimed.LeaseID, State: state, Code: code, RefundAttempt: refund, RetrySeconds: int32(retryDelay(r.Attempts) / time.Second)})
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Service) handoff(ctx context.Context, tx pgx.Tx, r q.PlatformMailProducerOutbox, passwordRevision int64) (string, string, bool, error) {
	sender := s.sender
	var message mail.Message
	switch r.Template {
	case VerificationTemplate, PasswordResetTemplate:
		var err error
		if r.Template == PasswordResetTemplate {
			message, err = s.passwordResetMessage(ctx, tx, r, passwordRevision)
			if s.passwordReset != nil {
				sender = s.passwordReset.sender
			}
		} else {
			message, err = s.verificationMessage(ctx, tx, r)
			if s.verification != nil {
				sender = s.verification.sender
			}
		}
		switch {
		case errors.Is(err, errVerificationTokenUnavailable):
			return "cancelled", "token_unavailable", false, nil
		case errors.Is(err, errVerificationPayloadUnavailable):
			return "failed", "payload_unavailable", false, nil
		case errors.Is(err, internalmail.ErrDisabled):
			return "queued", "maild_unavailable", true, nil
		case err != nil:
			return "", "", false, err
		}
	case "account_deletion_pending":
		subject, body := mail.AccountDeletionPendingBodyV1(r.RecipientAddress, r.DeletionRequestedAt.Time, r.RestoreUntil.Time)
		message = mail.Message{To: []string{r.RecipientAddress}, Subject: subject, TextBody: body, MessageID: "account-delete:" + uuidString(r.ID.Bytes)}
	default:
		return "", "", false, internalmail.ErrInvalid
	}
	err := sender.SendTimedWithRecovery(ctx, message, internalmail.MessageTiming{ExpiresAt: &r.ExpiresAt.Time}, uuidString(r.RecoveryID.Bytes))
	state, code := handoffOutcome(err, r.Attempts)
	return state, code, errors.Is(err, internalmail.ErrDisabled), nil
}

func handoffOutcome(err error, attempts int32) (string, string) {
	if err == nil {
		return "admitted", ""
	}
	if errors.Is(err, internalmail.ErrDisabled) {
		return "queued", "maild_unavailable"
	}
	if errors.Is(err, mail.ErrTransient) {
		if attempts >= api.InternalMailProducerOutboxMaxAttempts {
			return "failed", "attempt_limit"
		}
		return "queued", "maild_unavailable"
	}
	return "failed", "maild_rejected"
}

func retryDelay(attempt int32) time.Duration {
	delay := api.InternalMailProducerOutboxRetryBase
	for i := int32(1); i < attempt && delay < api.InternalMailProducerOutboxRetryMax; i++ {
		delay *= 2
	}
	if delay > api.InternalMailProducerOutboxRetryMax {
		return api.InternalMailProducerOutboxRetryMax
	}
	return delay
}

func (s *Service) Run(ctx context.Context, onError func(error)) {
	if !s.Enabled() {
		return
	}
	ticker := time.NewTicker(api.InternalMailProducerOutboxPoll)
	defer ticker.Stop()
	for {
		if !s.Enabled() {
			return
		}
		if err := s.DeliverOne(ctx); err != nil && ctx.Err() == nil && onError != nil {
			onError(err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
