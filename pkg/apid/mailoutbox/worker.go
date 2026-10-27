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
	magicOrigin := ""
	magicKeys := []string{}
	alertKeys := []string{}
	disabledKeys := []string{}
	changedKeys := []string{}
	quotaKeys := []string{}
	nonpaymentKeys := []string{}
	suspendedKeys := []string{}
	restoredKeys := []string{}
	paymentKeys := []string{}
	enabledKeys := []string{}
	disableOrigin := ""
	disableKeys := []string{}
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
	if s.magicLinkEnabled() {
		magicOrigin = s.magicLink.origin
		for key := range s.magicLink.readers {
			magicKeys = append(magicKeys, key)
		}
	}
	if s.mfaDisableEnabled() {
		disableOrigin = s.mfaDisable.origin
		for key := range s.mfaDisable.readers {
			disableKeys = append(disableKeys, key)
		}
	}
	if err := s.queries.PurgeExpiredMFADisableTokens(ctx, tx, api.InternalMailProducerOutboxMaintenanceBatch); err != nil {
		return q.PlatformMailProducerOutbox{}, err
	}
	if s.quotaWarningEnabled() {
		for key := range s.quotaWarning.readers {
			quotaKeys = append(quotaKeys, key)
		}
	}
	if s.nonpaymentDeletionEnabled() {
		for key := range s.nonpaymentDeletion.readers {
			nonpaymentKeys = append(nonpaymentKeys, key)
		}
	}
	if s.accountSuspendedEnabled() {
		for key := range s.accountSuspended.readers {
			suspendedKeys = append(suspendedKeys, key)
		}
	}
	if s.accountRestoredEnabled() {
		for key := range s.accountRestored.readers {
			restoredKeys = append(restoredKeys, key)
		}
	}
	if s.paymentFailedEnabled() {
		for key := range s.paymentFailed.readers {
			paymentKeys = append(paymentKeys, key)
		}
	}
	if s.mfaEnabledAlertEnabled() {
		for key := range s.mfaEnabledAlert.readers {
			enabledKeys = append(enabledKeys, key)
		}
	}
	if s.passwordChangedAlertEnabled() {
		for key := range s.passwordChangedAlert.readers {
			changedKeys = append(changedKeys, key)
		}
	}
	if s.mfaDisabledAlertEnabled() {
		for key := range s.mfaDisabledAlert.readers {
			disabledKeys = append(disabledKeys, key)
		}
	}
	if s.mfaRecoveryAlertEnabled() {
		for key := range s.mfaRecoveryAlert.readers {
			alertKeys = append(alertKeys, key)
		}
	}
	if err := s.queries.PurgeExpiredMagicLinkTokens(ctx, tx, api.InternalMailProducerOutboxMaintenanceBatch); err != nil {
		return q.PlatformMailProducerOutbox{}, err
	}
	if err := s.queries.PurgeExpiredPasswordResetTokens(ctx, tx, api.InternalMailProducerOutboxMaintenanceBatch); err != nil {
		return q.PlatformMailProducerOutbox{}, err
	}
	var deploymentKeys []string
	if s.deploymentFailedEnabled() {
		for key := range s.deploymentFailed.readers {
			deploymentKeys = append(deploymentKeys, key)
		}
	}
	var agentKeys []string
	if s.agentRunDeliveredEnabled() {
		for key := range s.agentRunDelivered.readers {
			agentKeys = append(agentKeys, key)
		}
	}
	var endedKeys []string
	if s.subscriptionEndedEnabled() {
		for key := range s.subscriptionEnded.readers {
			endedKeys = append(endedKeys, key)
		}
	}
	r, err := s.queries.Claim(ctx, tx, q.ClaimParams{Namespace: s.namespace, ProducerID: s.producer, FromAddress: s.from, VerificationEnabled: s.verificationEnabled(), VerificationOrigin: origin, VerificationKeys: keys, PasswordResetEnabled: s.passwordResetEnabled(), PasswordResetOrigin: resetOrigin, PasswordResetKeys: resetKeys, MagicLinkEnabled: s.magicLinkEnabled(), MagicLinkOrigin: magicOrigin, MagicLinkKeys: magicKeys, MfaDisableEnabled: s.mfaDisableEnabled(), MfaDisableOrigin: disableOrigin, MfaDisableKeys: disableKeys, DeploymentFailedEnabled: s.deploymentFailedEnabled(), DeploymentFailedKeys: deploymentKeys, AgentRunDeliveredEnabled: s.agentRunDeliveredEnabled(), AgentRunDeliveredKeys: agentKeys, SubscriptionEndedEnabled: s.subscriptionEndedEnabled(), SubscriptionEndedKeys: endedKeys, QuotaWarningEnabled: s.quotaWarningEnabled(), QuotaWarningKeys: quotaKeys, NonpaymentDeletionEnabled: s.nonpaymentDeletionEnabled(), NonpaymentDeletionKeys: nonpaymentKeys, AccountSuspendedEnabled: s.accountSuspendedEnabled(), AccountSuspendedKeys: suspendedKeys, AccountRestoredEnabled: s.accountRestoredEnabled(), AccountRestoredKeys: restoredKeys, PaymentFailedEnabled: s.paymentFailedEnabled(), PaymentFailedKeys: paymentKeys, MfaEnabledAlertEnabled: s.mfaEnabledAlertEnabled(), MfaEnabledAlertKeys: enabledKeys, PasswordChangedAlertEnabled: s.passwordChangedAlertEnabled(), PasswordChangedAlertKeys: changedKeys, MfaDisabledAlertEnabled: s.mfaDisabledAlertEnabled(), MfaDisabledAlertKeys: disabledKeys, MfaRecoveryAlertEnabled: s.mfaRecoveryAlertEnabled(), MfaRecoveryAlertKeys: alertKeys, MaxAttempts: api.InternalMailProducerOutboxMaxAttempts, LeaseID: pgID(uuid.NewString()), LeaseSeconds: int32(api.InternalMailProducerOutboxLease / time.Second)})
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
	if r.Template == DeploymentFailedTemplate {
		current, err := s.deploymentFailureCurrent(ctx, tx, r)
		if err != nil {
			return err
		}
		if !current {
			_, err := s.queries.Settle(ctx, tx, q.SettleParams{Namespace: s.namespace, ID: r.ID, LeaseID: claimed.LeaseID, State: "cancelled", Code: "account_changed"})
			if err != nil {
				return err
			}
			return tx.Commit(ctx)
		}
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
	case r.Template == MagicLinkTemplate && !magicLinkAccountAvailable(account.Status, account.AbuseHoldAt):
		state, code = "cancelled", "token_unavailable"
	case (r.Template == MFARecoveryAlertTemplate || r.Template == MFADisabledAlertTemplate || r.Template == PasswordChangedAlertTemplate || r.Template == MFAEnabledAlertTemplate) && account.Status != "active" && account.Status != "past_due" && account.Status != "suspended":
		state, code = "cancelled", "account_changed"
	case r.Template == AgentRunDeliveredTemplate && (!agentRunDeliveredAccountAvailable(account.Status) || account.DeletionRequestedAt.Valid):
		state, code = "cancelled", "account_changed"
	case r.Template == PaymentFailedTemplate && (account.Status != "past_due" || account.DeletionRequestedAt.Valid || !account.PastDueAt.Valid || !r.PaymentPastDueAt.Valid || !account.PastDueAt.Time.Equal(r.PaymentPastDueAt.Time)):
		state, code = "cancelled", "account_changed"
	case r.Template == SubscriptionEndedTemplate && (account.Plan != "free" || account.Status != "active" || account.PastDueAt.Valid || account.DeletionRequestedAt.Valid || account.StripeSubscriptionItem != "" || !r.SubscriptionEndedRevision.Valid || account.SubscriptionMailRevision != r.SubscriptionEndedRevision.Int64):
		state, code = "cancelled", "account_changed"
	case r.Template == QuotaWarningTemplate && (!quotaWarningAccountAvailable(account.Status) || account.DeletionRequestedAt.Valid || !account.LastQuotaWarningAt.Valid || !r.QuotaWarningDay.Valid || !account.LastQuotaWarningAt.Time.Equal(r.QuotaWarningDay.Time) || !r.QuotaWarningRevision.Valid || account.QuotaMailRevision != r.QuotaWarningRevision.Int64 || !r.QuotaWarningPlan.Valid || account.Plan != r.QuotaWarningPlan.String):
		state, code = "cancelled", "account_changed"
	case r.Template == NonpaymentDeletionTemplate && (account.Status != "deleted_pending" || !account.DeletionRequestedAt.Valid || !r.NonpaymentDeletionAt.Valid || !account.DeletionRequestedAt.Time.Equal(r.NonpaymentDeletionAt.Time) || !account.PastDueAt.Valid || !r.NonpaymentPastDueAt.Valid || !account.PastDueAt.Time.Equal(r.NonpaymentPastDueAt.Time) || !r.NonpaymentDeletionRevision.Valid || account.BillingMailRevision != r.NonpaymentDeletionRevision.Int64):
		state, code = "cancelled", "account_changed"
	case r.Template == AccountSuspendedTemplate && (account.Status != "suspended" || account.DeletionRequestedAt.Valid || !account.PastDueAt.Valid || !r.SuspensionPastDueAt.Valid || !account.PastDueAt.Time.Equal(r.SuspensionPastDueAt.Time) || !r.AccountSuspendedRevision.Valid || account.BillingMailRevision != r.AccountSuspendedRevision.Int64):
		state, code = "cancelled", "account_changed"
	case r.Template == AccountRestoredTemplate && (account.Status != "active" || account.PastDueAt.Valid || account.DeletionRequestedAt.Valid || !r.AccountRestoredRevision.Valid || account.BillingMailRevision != r.AccountRestoredRevision.Int64):
		state, code = "cancelled", "account_changed"
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
			state, code, refund, err = s.handoff(ctx, tx, r, account)
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

func (s *Service) handoff(ctx context.Context, tx pgx.Tx, r q.PlatformMailProducerOutbox, account q.ShareAccountRow) (string, string, bool, error) {
	sender := s.sender
	var message mail.Message
	switch r.Template {
	case VerificationTemplate, PasswordResetTemplate, MagicLinkTemplate, MFARecoveryAlertTemplate, MFADisableTemplate, MFADisabledAlertTemplate, PasswordChangedAlertTemplate, MFAEnabledAlertTemplate, PaymentFailedTemplate, AccountRestoredTemplate, AccountSuspendedTemplate, NonpaymentDeletionTemplate, QuotaWarningTemplate, SubscriptionEndedTemplate, DeploymentFailedTemplate, AgentRunDeliveredTemplate:
		var err error
		switch r.Template {
		case MFADisableTemplate:
			message, err = s.mfaDisableMessage(ctx, tx, r, account)
			if s.mfaDisable != nil {
				sender = s.mfaDisable.sender
			}
		case DeploymentFailedTemplate:
			message, err = s.deploymentFailedMessage(r)
			if s.deploymentFailed != nil {
				sender = s.deploymentFailed.sender
			}
		case AgentRunDeliveredTemplate:
			message, err = s.agentRunDeliveredMessage(r)
			if s.agentRunDelivered != nil {
				sender = s.agentRunDelivered.sender
			}
		case SubscriptionEndedTemplate:
			message, err = s.subscriptionEndedMessage(r)
			if s.subscriptionEnded != nil {
				sender = s.subscriptionEnded.sender
			}
		case QuotaWarningTemplate:
			message, err = s.quotaWarningMessage(r)
			if s.quotaWarning != nil {
				sender = s.quotaWarning.sender
			}
		case NonpaymentDeletionTemplate:
			message, err = s.nonpaymentDeletionMessage(r)
			if s.nonpaymentDeletion != nil {
				sender = s.nonpaymentDeletion.sender
			}
		case AccountSuspendedTemplate:
			message, err = s.accountSuspendedMessage(r)
			if s.accountSuspended != nil {
				sender = s.accountSuspended.sender
			}
		case AccountRestoredTemplate:
			message, err = s.accountRestoredMessage(r)
			if s.accountRestored != nil {
				sender = s.accountRestored.sender
			}
		case PaymentFailedTemplate:
			message, err = s.paymentFailedMessage(r)
			if s.paymentFailed != nil {
				sender = s.paymentFailed.sender
			}
		case MFAEnabledAlertTemplate:
			message, err = s.mfaEnabledAlertMessage(r)
			if s.mfaEnabledAlert != nil {
				sender = s.mfaEnabledAlert.sender
			}
		case PasswordChangedAlertTemplate:
			message, err = s.passwordChangedAlertMessage(r)
			if s.passwordChangedAlert != nil {
				sender = s.passwordChangedAlert.sender
			}
		case MFADisabledAlertTemplate:
			message, err = s.mfaDisabledAlertMessage(r)
			if s.mfaDisabledAlert != nil {
				sender = s.mfaDisabledAlert.sender
			}
		case MFARecoveryAlertTemplate:
			message, err = s.mfaRecoveryAlertMessage(r)
			if s.mfaRecoveryAlert != nil {
				sender = s.mfaRecoveryAlert.sender
			}
		case MagicLinkTemplate:
			message, err = s.magicLinkMessage(ctx, tx, r, account)
			if s.magicLink != nil {
				sender = s.magicLink.sender
			}
		case PasswordResetTemplate:
			message, err = s.passwordResetMessage(ctx, tx, r, account.PasswordResetRevision)
			if s.passwordReset != nil {
				sender = s.passwordReset.sender
			}
		case VerificationTemplate:
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
		if s.deploymentFailedEnabled() {
			if err := s.CaptureDeploymentFailures(ctx); err != nil && ctx.Err() == nil && onError != nil {
				onError(err)
			}
		}
		if s.agentRunDeliveredEnabled() {
			if err := s.CaptureAgentRunDeliveries(ctx); err != nil && ctx.Err() == nil && onError != nil {
				onError(err)
			}
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
