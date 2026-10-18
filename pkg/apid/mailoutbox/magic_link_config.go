package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const MagicLinkOriginEnv = "FAAS_INTERNAL_MAIL_MAGIC_LINK_ORIGIN"
const MagicLinkTemplate = "magic_link"

func (s *Service) magicLinkEnabled() bool {
	return s.Enabled() && s.magicLink != nil && s.magicLink.enabled != nil && s.magicLink.enabled()
}

// ConfigureMagicLink reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigureMagicLink(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.MagicLinkOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("magic-link outbox requires the configured producer outbox")
	}
	origin, err := verificationOrigin(getenv(MagicLinkOriginEnv))
	if err != nil {
		return errors.New("magic-link outbox requires a canonical HTTPS origin")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("magic-link outbox encryption identities unavailable")
	}
	keys, err := s.queries.MagicLinkKeyReferences(ctx, s.pool, q.MagicLinkKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("magic-link outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("magic-link outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.MagicLinkOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.magicLink = &credentialConfig{origin: origin, keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithMagicLinkOutbox(on), enabled: on}
	return nil
}
