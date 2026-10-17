package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const AccountRestoredTemplate = "account_restored"

func (s *Service) accountRestoredEnabled() bool {
	return s.Enabled() && s.accountRestored != nil && s.accountRestored.enabled != nil && s.accountRestored.enabled()
}

// ConfigureAccountRestored reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigureAccountRestored(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.AccountRestoredOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("account restored outbox requires the configured producer outbox")
	}
	if err := s.queries.AccountRestoredSchemaReady(ctx, s.pool); err != nil {
		return errors.New("account restored outbox schema unavailable")
	}
	ready, err := s.queries.AccountRestoredReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("account restored schema unavailable")
	}
	lifecycleReady, err := s.queries.AccountRestoredLifecycleReady(ctx, s.pool)
	if err != nil || !lifecycleReady {
		return errors.New("account restored lifecycle fence unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("account restored outbox encryption identities unavailable")
	}
	keys, err := s.queries.AccountRestoredKeyReferences(ctx, s.pool, q.AccountRestoredKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("account restored outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("account restored outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.AccountRestoredOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.accountRestored = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithAccountRestoredOutbox(on), enabled: on}
	return nil
}
