package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const PasswordResetOriginEnv = "FAAS_INTERNAL_MAIL_PASSWORD_RESET_ORIGIN"
const PasswordResetTemplate = "password_reset"

func (s *Service) passwordResetEnabled() bool {
	return s.Enabled() && s.passwordReset != nil && s.passwordReset.enabled != nil && s.passwordReset.enabled()
}

// ConfigurePasswordReset reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigurePasswordReset(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.PasswordResetOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("password-reset outbox requires the configured producer outbox")
	}
	origin, err := verificationOrigin(getenv(PasswordResetOriginEnv))
	if err != nil {
		return errors.New("password-reset outbox requires a canonical HTTPS origin")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("password-reset outbox encryption identities unavailable")
	}
	keys, err := s.queries.PasswordResetKeyReferences(ctx, s.pool, q.PasswordResetKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("password-reset outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("password-reset outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.PasswordResetOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.passwordReset = &credentialConfig{origin: origin, keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithPasswordResetOutbox(on), enabled: on}
	return nil
}
