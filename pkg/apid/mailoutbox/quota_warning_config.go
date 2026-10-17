package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

const QuotaWarningTemplate = "quota_warning"

func (s *Service) quotaWarningEnabled() bool {
	return s.Enabled() && s.quotaWarning != nil && s.quotaWarning.enabled != nil && s.quotaWarning.enabled()
}

// ConfigureQuotaWarning reads only its exact gate when disabled. It shares
// apid's dedicated credential readers, never maild's payload encryption keys.
func (s *Service) ConfigureQuotaWarning(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.QuotaWarningOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("quota warning outbox requires the configured producer outbox")
	}
	if err := s.queries.QuotaWarningSchemaReady(ctx, s.pool); err != nil {
		return errors.New("quota warning outbox schema unavailable")
	}
	ready, err := s.queries.QuotaWarningReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("quota warning schema unavailable")
	}
	lifecycleReady, err := s.queries.QuotaWarningLifecycleReady(ctx, s.pool)
	if err != nil || !lifecycleReady {
		return errors.New("quota warning lifecycle fence unavailable")
	}
	unsubscribe, err := quotaWarningUnsubscribeURL(getenv("FAAS_NOTIFICATIONS_UNSUBSCRIBE_URL"))
	if err != nil {
		return err
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("quota warning outbox encryption identities unavailable")
	}
	keys, err := s.queries.QuotaWarningKeyReferences(ctx, s.pool, q.QuotaWarningKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("quota warning outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("quota warning outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.QuotaWarningOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	s.quotaWarningUnsubscribe = unsubscribe
	s.quotaWarning = &credentialConfig{keyID: keyID, writer: writer, readers: readers,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithQuotaWarningOutbox(on), enabled: on}
	return nil
}

func (s *Service) QuotaWarningReady(ctx context.Context) error {
	if !s.quotaWarningEnabled() {
		return internalmail.ErrDisabled
	}
	if err := s.queries.QuotaWarningSchemaReady(ctx, s.pool); err != nil {
		return err
	}
	ready, err := s.queries.QuotaWarningLifecycleReady(ctx, s.pool)
	if err != nil {
		return err
	}
	if !ready {
		return internalmail.ErrInvalid
	}
	return nil
}
