package internalmail

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	q "github.com/onebox-faas/faas/pkg/internalmail/sqlc"
)

func (s *PGStore) enqueueTimed(ctx context.Context, m Message, recipients []Recipient, a domainAuthorization, p *Producer, enabled func() bool) (Message, bool, error) {
	if enabled == nil || !enabled() {
		return Message{}, false, ErrDisabled
	}
	if m.IsZero() || p == nil && (m.ProducerID != "" || m.SenderAddress != "") || p != nil && !validProducerMessage(m, *p) {
		return Message{}, false, ErrInvalid
	}
	normalized, err := m.normalized()
	if err != nil || !sameTiming(normalized, m.MessageTiming) {
		return Message{}, false, ErrInvalid
	}
	return s.enqueueMessage(ctx, m, recipients, a, p, enabled)
}

func (s *PGStore) checkTimingAdmission(ctx context.Context, db q.DBTX, timing MessageTiming) error {
	allowed, err := s.queries.TimingAdmission(ctx, db, q.TimingAdmissionParams{SendAt: sqlTime(timing.SendAt), ExpiresAt: sqlTime(timing.ExpiresAt),
		MaxDelaySeconds: int32(api.InternalMailMaxScheduleDelay / time.Second), MaxAgeSeconds: int32(api.InternalMailMaxQueueAge / time.Second)})
	if err != nil {
		return err
	}
	if !allowed.Valid || !allowed.Bool {
		return ErrInvalid
	}
	return nil
}

func (s *PGStore) checkRecipientTiming(ctx context.Context, db q.DBTX, id string, enabled bool) error {
	disposition, err := s.queries.RecipientTiming(ctx, db, q.RecipientTimingParams{Namespace: s.namespace, RecipientID: pgID(id), SchedulingEnabled: enabled, MaxAgeSeconds: int32(api.InternalMailMaxQueueAge / time.Second)})
	if err != nil {
		return storeError(err)
	}
	switch disposition {
	case "expired":
		return ErrMessageExpired
	case "waiting":
		return ErrMessageNotDue
	case "disabled":
		return ErrDisabled
	}
	return nil
}

func (s *PGStore) expireTimed(ctx context.Context) error {
	return s.expireRecipients(ctx, q.ExpireRecipientsParams{Namespace: s.namespace, MaxAttempts: api.InternalMailMaxAttempts, MaxAgeSeconds: int32(api.InternalMailMaxQueueAge / time.Second), TimedOnly: true})
}
