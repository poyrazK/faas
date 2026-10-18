package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const PaymentFailedTemplate = "payment_failed"

func (s *Service) paymentFailedEnabled() bool {
	return s.Enabled() && s.paymentFailed != nil && s.paymentFailed.enabled != nil && s.paymentFailed.enabled()
}

// ConfigurePaymentFailed reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigurePaymentFailed(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.PaymentFailedOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("payment failed outbox requires the configured producer outbox")
	}
	if err := s.queries.PaymentFailedSchemaReady(ctx, s.pool); err != nil {
		return errors.New("payment failed outbox schema unavailable")
	}
	ready, err := s.queries.PaymentFailedReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("payment failed schema unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("payment failed outbox encryption identities unavailable")
	}
	keys, err := s.queries.PaymentFailedKeyReferences(ctx, s.pool, q.PaymentFailedKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("payment failed outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("payment failed outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.PaymentFailedOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.paymentFailed = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithPaymentFailedOutbox(on), enabled: on}
	return nil
}
