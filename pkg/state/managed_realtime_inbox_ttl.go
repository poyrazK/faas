package state

import (
	"context"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	ttlq "github.com/onebox-faas/faas/pkg/state/realtimettl/sqlc"
	"strconv"
	"time"
)

// ManagedRealtimeExpiringInboxStore commits delivery TTL together with inbox and fallback state.
type ManagedRealtimeExpiringInboxStore interface {
	AppendManagedRealtimeInboxExpiring(context.Context, string, api.ManagedRealtimePrincipalMessageRequest, []byte) (ManagedRealtimeChannelMessage, error)
}

func (s *PgStore) AppendManagedRealtimeInboxExpiring(ctx context.Context, ep string, req api.ManagedRealtimePrincipalMessageRequest, data []byte) (ManagedRealtimeChannelMessage, error) {
	ctx, err := ttlContext(ctx, req.TTLSeconds)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if req.Delivery != api.ManagedRealtimeDeliveryRetained || req.FallbackAfterSeconds < 0 {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if req.FallbackAfterSeconds > 0 {
		category := req.NotificationCategory
		if category == "" {
			category = "notifications"
		}
		return s.AppendManagedRealtimeCategorizedNotification(ctx, ep, req.Principal, data, req.Binary, req.MessageID, req.FallbackAfterSeconds, category, req.NotificationGroupKey, req.NotificationGroupLabel, req.NotificationPriority, strconv.Itoa(req.NotificationTTLSeconds), req.NotificationCollapseKey, req.NotificationNotBefore)
	}
	return s.AppendManagedRealtimeInboxMessage(ctx, ep, req.Principal, data, req.Binary, req.MessageID)
}

func (m *MemStore) AppendManagedRealtimeInboxExpiring(ctx context.Context, ep string, req api.ManagedRealtimePrincipalMessageRequest, data []byte) (ManagedRealtimeChannelMessage, error) {
	ctx, err := ttlContext(ctx, req.TTLSeconds)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	if req.Delivery != api.ManagedRealtimeDeliveryRetained || req.FallbackAfterSeconds < 0 {
		return ManagedRealtimeChannelMessage{}, ErrManagedRealtimeHistoryInvalid
	}
	if req.FallbackAfterSeconds > 0 {
		category := req.NotificationCategory
		if category == "" {
			category = "notifications"
		}
		return m.AppendManagedRealtimeCategorizedNotification(ctx, ep, req.Principal, data, req.Binary, req.MessageID, req.FallbackAfterSeconds, category, req.NotificationGroupKey, req.NotificationGroupLabel, req.NotificationPriority, strconv.Itoa(req.NotificationTTLSeconds), req.NotificationCollapseKey, req.NotificationNotBefore)
	}
	return m.AppendManagedRealtimeInboxMessage(ctx, ep, req.Principal, data, req.Binary, req.MessageID)
}

func loadRealtimeInboxExpirations(ctx context.Context, db ttlq.DBTX, ep, ch string, messages []ManagedRealtimeChannelMessage) error {
	sequences := make([]int64, len(messages))
	for i := range messages {
		sequences[i] = messages[i].Sequence
	}
	if len(sequences) == 0 {
		return nil
	}
	rows, err := new(ttlq.Queries).ReadInboxExpirations(ctx, db, ttlq.ReadInboxExpirationsParams{EndpointID: ep, Channel: ch, Sequences: sequences})
	if err != nil {
		return err
	}
	bySequence := make(map[int64]int, len(messages))
	for i := range messages {
		bySequence[messages[i].Sequence] = i
	}
	for _, row := range rows {
		i := bySequence[row.Sequence]
		messages[i].TTLSeconds = int(row.TtlSeconds)
		expiry := row.ExpiresAt.Time.UTC()
		messages[i].ExpiresAt = &expiry
	}
	return nil
}
func saveRealtimeInboxExpiration(ctx context.Context, db ttlq.DBTX, message *ManagedRealtimeChannelMessage) error {
	if message.TTLSeconds == 0 {
		return nil
	}
	if message.ExpiresAt == nil {
		expiry := message.CreatedAt.Add(time.Duration(message.TTLSeconds) * time.Second)
		message.ExpiresAt = &expiry
	}
	return new(ttlq.Queries).SaveInboxExpiration(ctx, db, ttlq.SaveInboxExpirationParams{EndpointID: message.EndpointID, Channel: message.Channel, Sequence: message.Sequence, TtlSeconds: int32(message.TTLSeconds), ExpiresAt: pgtype.Timestamptz{Time: *message.ExpiresAt, Valid: true}})
}
