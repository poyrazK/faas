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
	s.mfaDisableCredentials = mailoutbox.NewMFADisableCredentials(pool)
	s.mfaDisableMailOutboxRequested = func() bool { return getenv(internalmail.MFADisableOutboxEnableEnv) == "1" }
	s.magicLinkCredentials = mailoutbox.NewMagicLinkCredentials(pool)
	s.deploymentFailedOutboxRequested = func() bool { return getenv(internalmail.DeploymentFailedOutboxEnableEnv) == "1" }
	s.subscriptionEndedOutboxRequested = func() bool { return getenv(internalmail.SubscriptionEndedOutboxEnableEnv) == "1" }
	s.quotaWarningOutboxRequested = func() bool { return getenv(internalmail.QuotaWarningOutboxEnableEnv) == "1" }
	s.nonpaymentDeletionOutboxRequested = func() bool { return getenv(internalmail.NonpaymentDeletionOutboxEnableEnv) == "1" }
	s.accountSuspendedOutboxRequested = func() bool { return getenv(internalmail.AccountSuspendedOutboxEnableEnv) == "1" }
	s.accountRestoredOutboxRequested = func() bool { return getenv(internalmail.AccountRestoredOutboxEnableEnv) == "1" }
	s.paymentFailedOutboxRequested = func() bool { return getenv(internalmail.PaymentFailedOutboxEnableEnv) == "1" }
	s.mfaEnabledAlertOutboxRequested = func() bool { return getenv(internalmail.MFAEnabledAlertOutboxEnableEnv) == "1" }
	s.passwordChangedAlertOutboxRequested = func() bool { return getenv(internalmail.PasswordChangedAlertOutboxEnableEnv) == "1" }
	s.mfaDisabledAlertOutboxRequested = func() bool { return getenv(internalmail.MFADisabledAlertOutboxEnableEnv) == "1" }
	s.mfaRecoveryAlertOutboxRequested = func() bool { return getenv(internalmail.MFARecoveryAlertOutboxEnableEnv) == "1" }
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
	if err := outbox.ConfigureMFADisable(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigureDeploymentFailed(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigureSubscriptionEnded(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigureQuotaWarning(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigureNonpaymentDeletion(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigureAccountSuspended(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigureAccountRestored(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigurePaymentFailed(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigureMFAEnabledAlert(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigurePasswordChangedAlert(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigureMFADisabledAlert(ctx, getenv); err != nil {
		return err
	}
	if err := outbox.ConfigureMFARecoveryAlert(ctx, getenv); err != nil {
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

func (s *server) markDeletionPendingWithMail(ctx context.Context, id string, opts ...state.AccountDeletionOption) (bool, error) {
	if s.deletionMailOutbox == nil || !s.deletionMailOutbox.Requested() {
		return false, s.store.MarkAccountDeletionPending(ctx, id, opts...)
	}
	// The outbox writes accounts in its own transaction; map the deletion
	// guards to the errors MarkAccountDeletionPending returns for them.
	err := state.AccountDeletionGuardError(s.deletionMailOutbox.ScheduleDeletion(ctx, id, opts...))
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
