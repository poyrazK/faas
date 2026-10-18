package internalmail

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) enqueueProduced(ctx context.Context, m Message, recipients []Recipient, a domainAuthorization, p Producer, enabled func() bool) (Message, bool, error) {
	if enabled == nil || !enabled() {
		return Message{}, false, ErrDisabled
	}
	if !m.IsZero() || !validProducerMessage(m, p) {
		return Message{}, false, ErrInvalid
	}
	return s.enqueueMessage(ctx, m, recipients, a, &p, enabled)
}

func validProducerMessage(m Message, p Producer) bool {
	return validProducerID(p.ID) && m.ProducerID == p.ID && p.allows(m.SenderAddress) && p.DailyRecipientLimit >= 1 && p.DailyRecipientLimit <= api.InternalMailDailyRecipients && p.PendingRecipientLimit >= 1 && p.PendingRecipientLimit <= api.InternalMailMaxPending
}

func (s *PGStore) checkProducerAdmission(ctx context.Context, db q.DBTX, p Producer, count int) error {
	usage, err := s.queries.ProducerQuota(ctx, db, q.ProducerQuotaParams{Namespace: s.namespace, ProducerID: p.ID})
	if err != nil {
		return err
	}
	if usage.Daily+int64(count) > int64(p.DailyRecipientLimit) || usage.Pending+int64(count) > int64(p.PendingRecipientLimit) {
		return ErrQuota
	}
	return nil
}

func (s *PGStore) producerUsage(ctx context.Context, id string) (ProducerUsage, error) {
	r, err := s.queries.ProducerQuota(ctx, s.pool, q.ProducerQuotaParams{Namespace: s.namespace, ProducerID: id})
	return ProducerUsage{ProducerID: id, Daily: r.Daily, Pending: r.Pending}, err
}

func (s *PGStore) producerMessage(ctx context.Context, producerID, id string) (OperatorMessage, error) {
	r, err := s.queries.ProducerMessage(ctx, s.pool, q.ProducerMessageParams{Namespace: s.namespace, ProducerID: producerID, ID: pgID(id)})
	if err != nil {
		return OperatorMessage{}, storeError(err)
	}
	return s.operatorMessageDetails(ctx, q.OperatorMessageRow(r))
}

func (s *PGStore) producerEvents(ctx context.Context, producerID, id string, o OperatorList) (OperatorPage[Event], error) {
	if _, err := s.queries.ProducerMessage(ctx, s.pool, q.ProducerMessageParams{Namespace: s.namespace, ProducerID: producerID, ID: pgID(id)}); err != nil {
		return OperatorPage[Event]{}, storeError(err)
	}
	return s.operatorEvents(ctx, id, o)
}
