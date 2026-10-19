package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const MFADisableOriginEnv = "FAAS_INTERNAL_MAIL_MFA_DISABLE_ORIGIN"
const MFADisableTemplate = "mfa_disable_request"

func (s *Service) mfaDisableEnabled() bool {
	return s.Enabled() && s.mfaDisable != nil && s.mfaDisable.enabled != nil && s.mfaDisable.enabled()
}

// ConfigureMFADisable reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigureMFADisable(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.MFADisableOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("MFA disable outbox requires the configured producer outbox")
	}
	origin, err := verificationOrigin(getenv(MFADisableOriginEnv))
	if err != nil {
		return errors.New("MFA disable outbox requires a canonical HTTPS origin")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("MFA disable outbox encryption identities unavailable")
	}
	keys, err := s.queries.MFADisableKeyReferences(ctx, s.pool, q.MFADisableKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("MFA disable outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("MFA disable outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.MFADisableOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.mfaDisable = &credentialConfig{origin: origin, keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithMFADisableOutbox(on), enabled: on}
	return nil
}
