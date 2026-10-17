package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const MFAEnabledAlertTemplate = "mfa_enabled"

func (s *Service) mfaEnabledAlertEnabled() bool {
	return s.Enabled() && s.mfaEnabledAlert != nil && s.mfaEnabledAlert.enabled != nil && s.mfaEnabledAlert.enabled()
}

// ConfigureMFAEnabledAlert reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigureMFAEnabledAlert(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.MFAEnabledAlertOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("MFA enabled alert outbox requires the configured producer outbox")
	}
	ready, err := s.queries.MFAEnabledAlertReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("MFA enabled alert schema unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("MFA enabled alert outbox encryption identities unavailable")
	}
	keys, err := s.queries.MFAEnabledAlertKeyReferences(ctx, s.pool, q.MFAEnabledAlertKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("MFA enabled alert outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("MFA enabled alert outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.MFAEnabledAlertOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.mfaEnabledAlert = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithMFAEnabledAlertOutbox(on), enabled: on}
	return nil
}
