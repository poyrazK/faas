package mailoutbox

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/apid/mailoutbox/sqlc"
	"github.com/onebox-faas/faas/pkg/internalmail"
)

// CaptureAgentRunDeliveries is a bounded pass over agentd's email delivery
// records (ADR-20261027090010 §8). Each is captured once: queued as a sealed intent
// for the account's own address, or recorded as unavailable, expired or
// stale. agentd reads the receipt to end the delivery; the run's state is
// already committed and cannot be rolled back by mail capacity or outages.
func (s *Service) CaptureAgentRunDeliveries(ctx context.Context) error {
	if !s.agentRunDeliveredEnabled() {
		return internalmail.ErrDisabled
	}
	pass, cancel := context.WithTimeout(ctx, api.InternalMailProducerOutboxPassTimeout)
	defer cancel()
	if err := s.enrollAgentRunCapture(pass, s.agentRunDeliveredEnabled); err != nil {
		return err
	}
	candidates, err := s.queries.AgentRunDeliveryCandidates(pass, s.pool, q.AgentRunDeliveryCandidatesParams{Namespace: s.namespace, ProducerID: s.producer,
		TtlSeconds: int32(api.InternalMailAgentRunDeliveredTTL / time.Second), BatchSize: api.InternalMailAgentRunCaptureBatch})
	if err != nil {
		return err
	}
	for _, c := range candidates {
		if err := s.captureAgentRunDelivery(pass, c); err != nil {
			return err
		}
	}
	return nil
}

// captureAgentRunDelivery locks the account, then shares the delivery row:
// agentd locks only the delivery, so the order cannot cycle.
func (s *Service) captureAgentRunDelivery(ctx context.Context, c q.AgentRunDeliveryCandidatesRow) error {
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
	capture, err := s.queries.AgentRunCapture(ctx, tx, q.AgentRunCaptureParams{Namespace: s.namespace, ProducerID: s.producer})
	if err != nil {
		return err
	}
	if capture.RecoveryID != recovery.RecoveryID {
		return internalmail.ErrRecoveryHeld
	}
	a, err := s.queries.ShareAccount(ctx, tx, c.AccountID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	d, err := s.queries.ShareAgentRunDelivery(ctx, tx, c.ID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	observed, err := s.queries.AgentRunDeliveryObserved(ctx, tx, d.ID)
	if err != nil || observed || d.AccountID != a.ID || d.CreatedAt.Time.Before(capture.StartedAt.Time) {
		return err
	}
	address, addressErr := internalmail.NormalizeSuppressionAddress(a.Email)
	disposition := ""
	switch {
	// agentd ends a delivery its window closed on; capturing it in the
	// window's last minute could mail a summary agentd reports as failed.
	case !d.CreatedAt.Time.Add(api.InternalMailAgentRunDeliveredTTL).After(time.Now()) || !d.DeadlineAt.Time.After(time.Now().Add(time.Minute)):
		disposition = "expired"
	case d.State != "pending" && d.State != "sending" && d.State != "reconciling":
		disposition = "stale"
	case addressErr != nil || !agentRunDeliveredAccountAvailable(a.Status) || a.DeletionRequestedAt.Valid:
		disposition = "unavailable"
	}
	var intent pgtype.UUID
	if disposition == "" {
		if intent, err = s.saveAgentRunDelivered(ctx, tx, address, recovery.RecoveryID, d); err != nil {
			return err
		}
		disposition = "queued"
	}
	if err := s.queries.RecordAgentRunDeliveryObservation(ctx, tx, q.RecordAgentRunDeliveryObservationParams{DeliveryID: d.ID, AccountID: a.ID,
		Disposition: disposition, IntentID: intent}); err != nil {
		return err
	}
	if !s.agentRunDeliveredEnabled() {
		return internalmail.ErrDisabled
	}
	return tx.Commit(ctx)
}
