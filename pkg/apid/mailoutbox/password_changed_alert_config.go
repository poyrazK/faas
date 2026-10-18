package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const PasswordChangedAlertTemplate = "password_changed"

func (s *Service) passwordChangedAlertEnabled() bool {
	return s.Enabled() && s.passwordChangedAlert != nil && s.passwordChangedAlert.enabled != nil && s.passwordChangedAlert.enabled()
}

// ConfigurePasswordChangedAlert reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigurePasswordChangedAlert(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.PasswordChangedAlertOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("password changed alert outbox requires the configured producer outbox")
	}
	ready, err := s.queries.PasswordChangedAlertReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("password changed alert schema unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("password changed alert outbox encryption identities unavailable")
	}
	keys, err := s.queries.PasswordChangedAlertKeyReferences(ctx, s.pool, q.PasswordChangedAlertKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("password changed alert outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("password changed alert outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.PasswordChangedAlertOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.passwordChangedAlert = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithPasswordChangedAlertOutbox(on), enabled: on}
	return nil
}
