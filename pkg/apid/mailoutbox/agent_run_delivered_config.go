package mailoutbox

import (
	"context"
	"errors"

	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

// AgentRunDeliveredTemplate mails an agent run's summary to its account's
// own address (ADR-20261027090010 §8).
const AgentRunDeliveredTemplate = "agent_run_delivered"

func (s *Service) agentRunDeliveredEnabled() bool {
	return s.Enabled() && s.agentRunDelivered != nil && s.agentRunDelivered.enabled != nil && s.agentRunDelivered.enabled()
}

// ConfigureAgentRunDelivered reads only its own exact gate when disabled.
// The first successful enrollment sets the capture boundary; restarts reuse
// it, so enabling the template never mails runs that finished before it.
func (s *Service) ConfigureAgentRunDelivered(ctx context.Context, getenv func(string) string) error {
	if getenv(internalmail.AgentRunDeliveredOutboxEnableEnv) != "1" {
		return nil
	}
	if !s.Enabled() || s.transport == nil {
		return errors.New("agent run summary outbox requires the configured producer outbox")
	}
	if err := s.queries.AgentRunDeliveredSchemaReady(ctx, s.pool); err != nil {
		return errors.New("agent run summary outbox schema unavailable")
	}
	ready, err := s.queries.AgentRunDeliveredReady(ctx, s.pool)
	if err != nil || !ready {
		return errors.New("agent run summary outbox schema unavailable")
	}
	keyID, writer, readers, err := credentialReaders(getenv)
	if err != nil {
		return errors.New("agent run summary outbox encryption identities unavailable")
	}
	keys, err := s.queries.AgentRunDeliveredKeyReferences(ctx, s.pool, q.AgentRunDeliveredKeyReferencesParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return errors.New("agent run summary outbox schema unavailable")
	}
	for _, key := range keys {
		if readers[key] == nil {
			return errors.New("agent run summary outbox is missing a pending payload's reader identity")
		}
	}
	on := func() bool { return s.Enabled() && internalmail.AgentRunDeliveredOutboxEnabled(getenv) }
	if !on() {
		return internalmail.ErrDisabled
	}
	if err := s.enrollAgentRunCapture(ctx, on); err != nil {
		return err
	}
	s.agentRunDelivered = &credentialConfig{keyID: keyID, writer: writer, readers: readers, enabled: on,
		sender: s.transport.WithScheduling(on).WithOutbox(on).WithAgentRunDeliveredOutbox(on)}
	return nil
}

func (s *Service) enrollAgentRunCapture(ctx context.Context, on func() bool) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollback(ctx, tx)
	if err := s.queries.LockAdmission(ctx, tx, s.namespace); err != nil {
		return err
	}
	recovery, err := s.recovery(ctx, tx)
	if err != nil {
		return err
	}
	if recovery.Held {
		return internalmail.ErrRecoveryHeld
	}
	if err := s.queries.EnrollAgentRunCapture(ctx, tx, q.EnrollAgentRunCaptureParams{Namespace: s.namespace, ProducerID: s.producer, RecoveryID: recovery.RecoveryID}); err != nil {
		return err
	}
	capture, err := s.queries.AgentRunCapture(ctx, tx, q.AgentRunCaptureParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return err
	}
	if capture.RecoveryID != recovery.RecoveryID {
		return internalmail.ErrRecoveryHeld
	}
	if !on() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}
