package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const SubscriptionEndedTemplate = "subscription_ended"

func (s *Service) subscriptionEndedEnabled() bool {
	return s.Enabled() && s.subscriptionEnded != nil && s.subscriptionEnded.enabled != nil && s.subscriptionEnded.enabled()
}

// ConfigureSubscriptionEnded reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigureSubscriptionEnded(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.SubscriptionEndedOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("subscription ended outbox requires the configured producer outbox")
	}
	if err := s.queries.SubscriptionEndedSchemaReady(ctx, s.pool); err != nil {
		return errors.New("subscription ended outbox schema unavailable")
	}
	ready, err := s.queries.SubscriptionEndedReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("subscription ended schema unavailable")
	}
	lifecycleReady, err := s.queries.SubscriptionEndedLifecycleReady(ctx, s.pool)
	if err != nil || !lifecycleReady {
		return errors.New("subscription ended lifecycle fence unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("subscription ended outbox encryption identities unavailable")
	}
	keys, err := s.queries.SubscriptionEndedKeyReferences(ctx, s.pool, q.SubscriptionEndedKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("subscription ended outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("subscription ended outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.SubscriptionEndedOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.subscriptionEnded = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithSubscriptionEndedOutbox(on), enabled: on}
	return nil
}
