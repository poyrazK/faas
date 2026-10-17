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
	copy.verificationEnabled = enabled
	copy.mfaDisableEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithPasswordResetOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.passwordResetEnabled = enabled
	copy.mfaDisableEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.magicLinkEnabled = nil
	copy.verificationEnabled = nil
	return &copy
}

func (s *InternalSender) WithMagicLinkOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.magicLinkEnabled = enabled
	copy.mfaDisableEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	return &copy
}

func (s *InternalSender) WithMFARecoveryAlertOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.mfaDisableEnabled = nil
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaRecoveryAlertEnabled = enabled
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithMFADisableOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.mfaDisableEnabled = enabled
	copy.mfaDisabledAlertEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) WithMFADisabledAlertOutbox(enabled func() bool) *InternalSender {
	copy := *s
	copy.mfaDisabledAlertEnabled = enabled
	copy.mfaDisableEnabled = nil
	copy.mfaRecoveryAlertEnabled = nil
	copy.verificationEnabled = nil
	copy.passwordResetEnabled = nil
	copy.magicLinkEnabled = nil
	return &copy
}

func (s *InternalSender) credentialOutboxGate() (string, func() bool) {
	if s.mfaDisableEnabled != nil {
		return internalmail.ProducerOutboxMFADisableTemplate, s.mfaDisableEnabled
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
