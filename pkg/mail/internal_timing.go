package mail

import (
	"context"
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/internalmail"
)

// TimedSender is an explicit capability of the private internal transports.
// Other providers and decorators must opt in rather than silently discard a
// schedule or expiry deadline.
type TimedSender interface {
	Sender
	SendTimed(context.Context, Message, internalmail.MessageTiming) error
}

// WithOutbox gates generation-bound admission independently from ordinary
// producer sends. It returns a separate value and enables no existing caller.
func (s *InternalSender) WithOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.outboxEnabled = enabled
	return &copy
}

// WithVerificationOutbox marks a separate generation-bound template capability.
// Both the sender and maild gate it independently from deletion notifications.
func (s *InternalSender) WithVerificationOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.verificationEnabled = enabled
	copy.mfaDisableEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithPasswordResetOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.passwordResetEnabled = enabled
	copy.mfaDisableEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.magicLinkEnabled = nil
	copy.verificationEnabled = nil
	return &copy
}

func (s *InternalSender) WithMagicLinkOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.magicLinkEnabled = enabled
	copy.mfaDisableEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	return &copy
}

func (s *InternalSender) WithMFARecoveryAlertOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaRecoveryAlertEnabled = enabled
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithMFADisableOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.mfaDisableEnabled = enabled
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithMFADisabledAlertOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = enabled
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithPasswordChangedAlertOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = enabled
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithMFAEnabledAlertOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = enabled
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithPaymentFailedOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = enabled
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithAccountSuspendedOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.accountSuspendedEnabled = enabled
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithAccountRestoredOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.accountRestoredEnabled = enabled
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithNonpaymentDeletionOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = enabled
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithQuotaWarningOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = enabled
	copy.nonpaymentDeletionEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithSubscriptionEndedOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = enabled
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithDeploymentFailedOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.deploymentFailedEnabled = enabled
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.managedPostgresCapEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

// WithManagedPostgresCapOutbox gates the managed PostgreSQL compute cap
// warning (ADR-20261025094915) on its own switch.
func (s *InternalSender) WithManagedPostgresCapOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.deploymentFailedEnabled = nil
	copy.subscriptionEndedEnabled = nil
	copy.quotaWarningEnabled = nil
	copy.nonpaymentDeletionEnabled = nil
	copy.accountSuspendedEnabled = nil
	copy.accountRestoredEnabled = nil
	copy.paymentFailedEnabled = nil
	copy.mfaEnabledAlertEnabled = nil
	copy.passwordChangedAlertEnabled = nil
	copy.mfaDisableEnabled = nil
	copy.managedPostgresCapEnabled = enabled
	return &copy
}

func (s *InternalSender) credentialOutboxGate() (string, func() bool) {
	if s.managedPostgresCapEnabled != nil {
		return internalmail.ProducerOutboxManagedPostgresCapTemplate, s.managedPostgresCapEnabled
	}
	if s.deploymentFailedEnabled != nil {
		return internalmail.ProducerOutboxDeploymentFailedTemplate, s.deploymentFailedEnabled
	}
	if s.subscriptionEndedEnabled != nil {
		return internalmail.ProducerOutboxSubscriptionEndedTemplate, s.subscriptionEndedEnabled
	}
	if s.quotaWarningEnabled != nil {
		return internalmail.ProducerOutboxQuotaWarningTemplate, s.quotaWarningEnabled
	}
	if s.nonpaymentDeletionEnabled != nil {
		return internalmail.ProducerOutboxNonpaymentDeletionTemplate, s.nonpaymentDeletionEnabled
	}
	if s.accountSuspendedEnabled != nil {
		return internalmail.ProducerOutboxAccountSuspendedTemplate, s.accountSuspendedEnabled
	}
	if s.accountRestoredEnabled != nil {
		return internalmail.ProducerOutboxAccountRestoredTemplate, s.accountRestoredEnabled
	}
	if s.paymentFailedEnabled != nil {
		return internalmail.ProducerOutboxPaymentFailedTemplate, s.paymentFailedEnabled
	}
	if s.mfaDisableEnabled != nil {
		return internalmail.ProducerOutboxMFADisableTemplate, s.mfaDisableEnabled
	}
	if s.mfaEnabledAlertEnabled != nil {
		return internalmail.ProducerOutboxMFAEnabledAlertTemplate, s.mfaEnabledAlertEnabled
	}
	if s.passwordChangedAlertEnabled != nil {
		return internalmail.ProducerOutboxPasswordChangedAlertTemplate, s.passwordChangedAlertEnabled
	}
	if s.mfaDisabledAlertEnabled != nil {
		return internalmail.ProducerOutboxMFADisabledAlertTemplate, s.mfaDisabledAlertEnabled
	}
	if s.mfaRecoveryAlertEnabled != nil {
		return internalmail.ProducerOutboxMFARecoveryAlertTemplate, s.mfaRecoveryAlertEnabled
	}
	if s.magicLinkEnabled != nil {
		return internalmail.ProducerOutboxMagicLinkTemplate, s.magicLinkEnabled
	}
	if s.passwordResetEnabled != nil {
		return internalmail.ProducerOutboxPasswordResetTemplate, s.passwordResetEnabled
	}
	if s.verificationEnabled != nil {
		return internalmail.ProducerOutboxVerificationTemplate, s.verificationEnabled
	}
	return "", nil
}

func (s *InternalSender) SendTimedWithRecovery(ctx context.Context, msg Message, timing internalmail.MessageTiming, generation string) error {
	if s.outboxEnabled == nil || !s.outboxEnabled() {
		return internalmail.ErrDisabled
	}
	if !strings.HasPrefix(s.route, internalmail.ProducersPath+"/") || !internalmail.ValidProducerOutboxGeneration(generation) {
		return internalmail.ErrInvalid
	}
	copy := *s
	copy.recoveryGeneration = generation
	return copy.SendTimed(ctx, msg, timing)
}

var ErrTimedTransportRequired = errors.New("mail: timing requires a supported internal transport")

func SendTimed(ctx context.Context, sender Sender, msg Message, timing internalmail.MessageTiming) error {
	if timing.IsZero() {
		return sender.Send(ctx, msg)
	}
	timed, ok := sender.(TimedSender)
	if !ok {
		return ErrTimedTransportRequired
	}
	return timed.SendTimed(ctx, msg, timing)
}

// WithScheduling returns a separate sender value with an explicit gate callback.
// It does not mutate an existing sender or enable ordinary Send calls.
func (s *InternalSender) WithScheduling(enabled func() bool) *InternalSender {
	copy := *s
	copy.schedulingEnabled = enabled
	return &copy
}

func (s *InternalSender) SendTimed(ctx context.Context, msg Message, timing internalmail.MessageTiming) error {
	if timing.IsZero() {
		return s.Send(ctx, msg)
	}
	if !s.enabled() || s.schedulingEnabled == nil || !s.schedulingEnabled() {
		return internalmail.ErrDisabled
	}
	if msg.MessageID == "" {
		return errors.New("mail: timed submission requires a caller-stable MessageID")
	}
	return s.send(ctx, msg, timing)
}
