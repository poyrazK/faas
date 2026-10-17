package apidserver

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/apid/mailoutbox"
	"github.com/onebox-faas/faas/pkg/internalmail"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) configureMailOutbox(ctx context.Context, pool *pgxpool.Pool, getenv func(string) string) error {
	// Saved reset bindings remain enforced after an issuance/delivery rollback.
	// Construction is pure and needs neither a feature flag nor encryption keys.
	s.passwordResetCredentials = mailoutbox.NewResetCredentials(pool)
	s.magicLinkCredentials = mailoutbox.NewMagicLinkCredentials(pool)
	s.magicLinkMailOutboxRequested = func() bool { return getenv(internalmail.MagicLinkOutboxEnableEnv) == "1" }
	s.passwordResetMailOutboxRequested = func() bool { return getenv(internalmail.PasswordResetOutboxEnableEnv) == "1" }
	outbox, err := mailoutbox.NewFromEnv(pool, getenv)
	if err != nil {
		return err
	}
	s.verificationMailOutboxRequested = func() bool { return getenv(internalmail.VerificationOutboxEnableEnv) == "1" }
	if err := outbox.ConfigureVerification(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigurePasswordReset(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigureMagicLink(ctx, getenv); err != nil {
		return err
	}
	if outbox != nil {
		if err := outbox.Ready(ctx); err != nil {
			return errors.New("apid: producer mail outbox schema is unavailable")
		}
	}
	s.deletionMailOutbox = outbox
	return nil
}

func (s *server) queueEmailVerification(ctx context.Context, account state.Account) bool {
	if s.verificationMailOutboxRequested == nil || !s.verificationMailOutboxRequested() {
		return false
	}
	if s.deletionMailOutbox == nil {
		s.log.Error("email_verification.outbox_unavailable")
		return true
	}
	if err := s.deletionMailOutbox.QueueVerification(ctx, account.ID, account.Email); err != nil {
		// The public auth response and its anti-enumeration shape stay unchanged.
		// Never log a token, link, ciphertext, key material or raw storage error.
		s.log.Error("email_verification.outbox_save_failed", "account_id", account.ID)
	}
	return true
}

func (s *server) markDeletionPendingWithMail(ctx context.Context, id string) (bool, error) {
	if s.deletionMailOutbox == nil || !s.deletionMailOutbox.Requested() {
		return false, s.store.MarkAccountDeletionPending(ctx, id)
	}
	err := s.deletionMailOutbox.ScheduleDeletion(ctx, id)
	if errors.Is(err, mailoutbox.ErrAccountUnavailable) {
		err = state.ErrNotFound
	}
	return true, err
}

func (s *server) sendInlineDeletionNotice(ctx context.Context, account state.Account, outboxed bool) {
	if outboxed || account.DeletionRequestedAt == nil {
		return
	}
	restoreUntil := account.DeletionRequestedAt.Add(state.DeletionGraceDuration())
	subject, body := mail.AccountDeletionPendingBody(account.Email, *account.DeletionRequestedAt, restoreUntil)
	if err := s.mailer.Send(ctx, Message{To: []string{account.Email}, Subject: subject, TextBody: body}); err != nil {
		s.log.Warn("apid: pending deletion email send failed", "account", account.ID, "err", err)
	}
}

func (s *server) runMailOutbox(ctx context.Context) {
	if s.deletionMailOutbox != nil {
		s.deletionMailOutbox.Run(ctx, func(error) { s.log.Error("apid: producer mail outbox handoff failed") })
	}
}
