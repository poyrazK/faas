package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const AccountSuspendedTemplate = "account_suspended"

func (s *Service) accountSuspendedEnabled() bool {
	return s.Enabled() && s.accountSuspended != nil && s.accountSuspended.enabled != nil && s.accountSuspended.enabled()
}

// ConfigureAccountSuspended reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigureAccountSuspended(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.AccountSuspendedOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("account suspended outbox requires the configured producer outbox")
	}
	if err := s.queries.AccountSuspendedSchemaReady(ctx, s.pool); err != nil {
		return errors.New("account suspended outbox schema unavailable")
	}
	ready, err := s.queries.AccountSuspendedReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("account suspended schema unavailable")
	}
	lifecycleReady, err := s.queries.AccountRestoredLifecycleReady(ctx, s.pool)
	if err != nil || !lifecycleReady {
		return errors.New("account suspended lifecycle fence unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("account suspended outbox encryption identities unavailable")
	}
	keys, err := s.queries.AccountSuspendedKeyReferences(ctx, s.pool, q.AccountSuspendedKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("account suspended outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("account suspended outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.AccountSuspendedOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.accountSuspended = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithAccountSuspendedOutbox(on), enabled: on}
	return nil
}

func (s *Service) AccountSuspendedReady(ctx context.Context) error {
	if !s.accountSuspendedEnabled() {
		return internalmail.ErrDisabled
	}
	if err := s.queries.AccountSuspendedSchemaReady(ctx, s.pool); err != nil {
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
