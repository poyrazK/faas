package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const NonpaymentDeletionTemplate = "nonpayment_deletion"

func (s *Service) nonpaymentDeletionEnabled() bool {
	return s.Enabled() && s.nonpaymentDeletion != nil && s.nonpaymentDeletion.enabled != nil && s.nonpaymentDeletion.enabled()
}

// ConfigureNonpaymentDeletion reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigureNonpaymentDeletion(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.NonpaymentDeletionOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("non-payment deletion outbox requires the configured producer outbox")
	}
	if err := s.queries.NonpaymentDeletionSchemaReady(ctx, s.pool); err != nil {
		return errors.New("non-payment deletion outbox schema unavailable")
	}
	ready, err := s.queries.NonpaymentDeletionReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("non-payment deletion schema unavailable")
	}
	lifecycleReady, err := s.queries.AccountRestoredLifecycleReady(ctx, s.pool)
	if err != nil || !lifecycleReady {
		return errors.New("non-payment deletion lifecycle fence unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("non-payment deletion outbox encryption identities unavailable")
	}
	keys, err := s.queries.NonpaymentDeletionKeyReferences(ctx, s.pool, q.NonpaymentDeletionKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("non-payment deletion outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("non-payment deletion outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.NonpaymentDeletionOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.nonpaymentDeletion = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithNonpaymentDeletionOutbox(on), enabled: on}
	return nil
}

func (s *Service) NonpaymentDeletionReady(ctx context.Context) error {
	if !s.nonpaymentDeletionEnabled() {
		return internalmail.ErrDisabled
	}
	if err := s.queries.NonpaymentDeletionSchemaReady(ctx, s.pool); err != nil {
		return err
	}
	ready, err := s.queries.AccountRestoredLifecycleReady(ctx, s.pool)
	if err != nil {
		return err
	}
	if !ready {
		return internalmail.ErrInvalid
	}
	return nil
}
