package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const MFARecoveryAlertTemplate = "mfa_recovery_used"

func (s *Service) mfaRecoveryAlertEnabled() bool {
	return s.Enabled() && s.mfaRecoveryAlert != nil && s.mfaRecoveryAlert.enabled != nil && s.mfaRecoveryAlert.enabled()
}

// ConfigureMFARecoveryAlert reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigureMFARecoveryAlert(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.MFARecoveryAlertOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("MFA recovery alert outbox requires the configured producer outbox")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("MFA recovery alert outbox encryption identities unavailable")
	}
	keys, err := s.queries.MFARecoveryAlertKeyReferences(ctx, s.pool, q.MFARecoveryAlertKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("MFA recovery alert outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("MFA recovery alert outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.MFARecoveryAlertOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.mfaRecoveryAlert = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithMFARecoveryAlertOutbox(on), enabled: on}
	return nil
}
