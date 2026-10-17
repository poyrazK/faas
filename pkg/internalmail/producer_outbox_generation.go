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
		case ProducerOutboxPasswordChangedAlertTemplate:
			gate = s.passwordChangedAlertOutboxEnabled
		case ProducerOutboxMFADisabledAlertTemplate:
			gate = s.mfaDisabledAlertOutboxEnabled
		case ProducerOutboxMFARecoveryAlertTemplate:
			gate = s.mfaRecoveryAlertOutboxEnabled
		default:
			if !s.verificationOutboxEnabled() && !s.passwordResetOutboxEnabled() && !s.magicLinkOutboxEnabled() && !s.mfaRecoveryAlertOutboxEnabled() && !s.mfaDisableOutboxEnabled() && !s.mfaDisabledAlertOutboxEnabled() && !s.passwordChangedAlertOutboxEnabled() {
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
