package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const MFADisabledAlertTemplate = "mfa_disabled"

func (s *Service) mfaDisabledAlertEnabled() bool {
	return s.Enabled() && s.mfaDisabledAlert != nil && s.mfaDisabledAlert.enabled != nil && s.mfaDisabledAlert.enabled()
}

// ConfigureMFADisabledAlert reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigureMFADisabledAlert(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.MFADisabledAlertOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("MFA disabled alert outbox requires the configured producer outbox")
	}
	ready, err := s.queries.MFADisabledAlertReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("MFA disabled alert schema unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("MFA disabled alert outbox encryption identities unavailable")
	}
	keys, err := s.queries.MFADisabledAlertKeyReferences(ctx, s.pool, q.MFADisabledAlertKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("MFA disabled alert outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("MFA disabled alert outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.MFADisabledAlertOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.mfaDisabledAlert = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithMFADisabledAlertOutbox(on), enabled: on}
	return nil
}
