package internalmail

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

const ProducerOutboxGenerationHeader = "X-Gregale-Mail-Recovery-ID"
const ProducerOutboxTemplateHeader = "X-Gregale-Mail-Outbox-Template"
const ProducerOutboxVerificationTemplate = "email_verification"
const ProducerOutboxPasswordResetTemplate = "password_reset"
const ProducerOutboxMagicLinkTemplate = "magic_link"
const ProducerOutboxMFADisableTemplate = "mfa_disable_request"
const ProducerOutboxMFADisabledAlertTemplate = "mfa_disabled"
const ProducerOutboxDeploymentFailedTemplate = "deployment_failed"
const ProducerOutboxSubscriptionEndedTemplate = "subscription_ended"
const ProducerOutboxQuotaWarningTemplate = "quota_warning"
const ProducerOutboxNonpaymentDeletionTemplate = "nonpayment_deletion"
const ProducerOutboxAccountSuspendedTemplate = "account_suspended"
const ProducerOutboxManagedPostgresCapTemplate = "managed_postgres_cap"
const ProducerOutboxAccountRestoredTemplate = "account_restored"
const ProducerOutboxPaymentFailedTemplate = "payment_failed"
const ProducerOutboxMFAEnabledAlertTemplate = "mfa_enabled"
const ProducerOutboxPasswordChangedAlertTemplate = "password_changed"
const ProducerOutboxMFARecoveryAlertTemplate = "mfa_recovery_used"

func (s *Service) verificationOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.VerificationOutboxEnabled != nil && s.VerificationOutboxEnabled()
}

func (s *Service) passwordResetOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.PasswordResetOutboxEnabled != nil && s.PasswordResetOutboxEnabled()
}

func (s *Service) magicLinkOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.MagicLinkOutboxEnabled != nil && s.MagicLinkOutboxEnabled()
}

func (s *Service) mfaRecoveryAlertOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.MFARecoveryAlertOutboxEnabled != nil && s.MFARecoveryAlertOutboxEnabled()
}

func (s *Service) mfaDisableOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.MFADisableOutboxEnabled != nil && s.MFADisableOutboxEnabled()
}

func (s *Service) accountSuspendedOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.AccountSuspendedOutboxEnabled != nil && s.AccountSuspendedOutboxEnabled()
}

func (s *Service) managedPostgresCapOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.ManagedPostgresCapOutboxEnabled != nil && s.ManagedPostgresCapOutboxEnabled()
}

func (s *Service) accountRestoredOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.AccountRestoredOutboxEnabled != nil && s.AccountRestoredOutboxEnabled()
}

func (s *Service) paymentFailedOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.PaymentFailedOutboxEnabled != nil && s.PaymentFailedOutboxEnabled()
}

func (s *Service) mfaEnabledAlertOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.MFAEnabledAlertOutboxEnabled != nil && s.MFAEnabledAlertOutboxEnabled()
}

func (s *Service) passwordChangedAlertOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.PasswordChangedAlertOutboxEnabled != nil && s.PasswordChangedAlertOutboxEnabled()
}

func (s *Service) mfaDisabledAlertOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.MFADisabledAlertOutboxEnabled != nil && s.MFADisabledAlertOutboxEnabled()
}

type producerOutboxGenerationKey struct{}
type producerOutboxGateKey struct{}
type producerOutboxGenerationStore interface {
	checkOutboxGeneration(context.Context, q.DBTX, string) error
}

// Zero is the generation before any explicit database recovery. Every other
// value must be the canonical externally supplied recovery UUID.
func ValidProducerOutboxGeneration(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed.String() == id
}

func (s *Service) producerOutboxContext(ctx context.Context, header http.Header) (context.Context, error) {
	values := header.Values(ProducerOutboxGenerationHeader)
	templates := header.Values(ProducerOutboxTemplateHeader)
	gate := s.producerOutboxEnabled
	if len(templates) > 0 {
		switch templates[0] {
		case ProducerOutboxVerificationTemplate:
			gate = s.verificationOutboxEnabled
		case ProducerOutboxPasswordResetTemplate:
			gate = s.passwordResetOutboxEnabled
		case ProducerOutboxMagicLinkTemplate:
			gate = s.magicLinkOutboxEnabled
		case ProducerOutboxMFADisableTemplate:
			gate = s.mfaDisableOutboxEnabled
		case ProducerOutboxDeploymentFailedTemplate:
			gate = s.deploymentFailedOutboxEnabled
		case ProducerOutboxSubscriptionEndedTemplate:
			gate = s.subscriptionEndedOutboxEnabled
		case ProducerOutboxQuotaWarningTemplate:
			gate = s.quotaWarningOutboxEnabled
		case ProducerOutboxNonpaymentDeletionTemplate:
			gate = s.nonpaymentDeletionOutboxEnabled
		case ProducerOutboxAccountSuspendedTemplate:
			gate = s.accountSuspendedOutboxEnabled
		case ProducerOutboxManagedPostgresCapTemplate:
			gate = s.managedPostgresCapOutboxEnabled
		case ProducerOutboxAccountRestoredTemplate:
			gate = s.accountRestoredOutboxEnabled
		case ProducerOutboxPaymentFailedTemplate:
			gate = s.paymentFailedOutboxEnabled
		case ProducerOutboxMFAEnabledAlertTemplate:
			gate = s.mfaEnabledAlertOutboxEnabled
		case ProducerOutboxPasswordChangedAlertTemplate:
			gate = s.passwordChangedAlertOutboxEnabled
		case ProducerOutboxMFADisabledAlertTemplate:
			gate = s.mfaDisabledAlertOutboxEnabled
		case ProducerOutboxMFARecoveryAlertTemplate:
			gate = s.mfaRecoveryAlertOutboxEnabled
		default:
			if !s.verificationOutboxEnabled() && !s.passwordResetOutboxEnabled() && !s.magicLinkOutboxEnabled() && !s.mfaRecoveryAlertOutboxEnabled() && !s.mfaDisableOutboxEnabled() && !s.mfaDisabledAlertOutboxEnabled() && !s.passwordChangedAlertOutboxEnabled() && !s.mfaEnabledAlertOutboxEnabled() && !s.paymentFailedOutboxEnabled() && !s.accountRestoredOutboxEnabled() && !s.accountSuspendedOutboxEnabled() && !s.nonpaymentDeletionOutboxEnabled() && !s.quotaWarningOutboxEnabled() && !s.subscriptionEndedOutboxEnabled() && !s.deploymentFailedOutboxEnabled() && !s.managedPostgresCapOutboxEnabled() {
				return ctx, ErrDisabled
			}
			return ctx, ErrInvalid
		}
		if !gate() {
			return ctx, ErrDisabled
		}
		if len(templates) != 1 {
			return ctx, ErrInvalid
		}
	}
	if len(values) == 0 && len(templates) == 0 {
		return ctx, nil
	}
	if !s.producerOutboxEnabled() {
		return ctx, ErrDisabled
	}
	if len(values) != 1 || !ValidProducerOutboxGeneration(values[0]) {
		return ctx, ErrInvalid
	}
	if _, ok := s.Store.(producerOutboxGenerationStore); !ok {
		return ctx, ErrInvalid
	}
	ctx = context.WithValue(ctx, producerOutboxGenerationKey{}, values[0])
	ctx = context.WithValue(ctx, producerOutboxGateKey{}, gate)
	return ctx, nil
}

func producerOutboxAdmissionEnabled(ctx context.Context) bool {
	gate, bound := ctx.Value(producerOutboxGateKey{}).(func() bool)
	return !bound || gate != nil && gate()
}

// Called under maild's own shared recovery fence before idempotency lookup or
// writes. The producer does not retain that fence across the HTTP boundary.
func (s *PGStore) checkOutboxGeneration(ctx context.Context, db q.DBTX, expected string) error {
	state, err := s.recoveryState(ctx, db)
	if err != nil {
		return err
	}
	actual := uuid.Nil.String()
	if state.RecoveryID.Valid {
		actual = idString(state.RecoveryID)
	}
	if expected != actual {
		return ErrRecoveryHeld
	}
	return nil
}

func (s *Service) nonpaymentDeletionOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.NonpaymentDeletionOutboxEnabled != nil && s.NonpaymentDeletionOutboxEnabled()
}

func (s *Service) quotaWarningOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.QuotaWarningOutboxEnabled != nil && s.QuotaWarningOutboxEnabled()
}

func (s *Service) subscriptionEndedOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.SubscriptionEndedOutboxEnabled != nil && s.SubscriptionEndedOutboxEnabled()
}

func (s *Service) deploymentFailedOutboxEnabled() bool {
	return s.producerOutboxEnabled() && s.DeploymentFailedOutboxEnabled != nil && s.DeploymentFailedOutboxEnabled()
}
