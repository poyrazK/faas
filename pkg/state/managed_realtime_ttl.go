package state

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	ttlq "github.com/onebox-faas/faas/pkg/state/realtimettl/sqlc"
)

type realtimeMessageTTLKey struct{}

// ManagedRealtimeExpiringStore atomically appends an optional expiring event.
type ManagedRealtimeExpiringStore interface {
	AppendManagedRealtimeChannelExpiring(context.Context, string, string, []byte, bool, string, map[string]string, *int64, int) (ManagedRealtimeChannelMessage, error)
}

func realtimeMessageTTL(ctx context.Context) int {
	ttl, _ := ctx.Value(realtimeMessageTTLKey{}).(int)
	return ttl
}
func ttlContext(ctx context.Context, ttl int) (context.Context, error) {
	if ttl < 0 || ttl > api.RealtimeMessageTTLMaxSeconds {
		return nil, ErrManagedRealtimeHistoryInvalid
	}
	return context.WithValue(ctx, realtimeMessageTTLKey{}, ttl), nil
}
func (s *PgStore) AppendManagedRealtimeChannelExpiring(ctx context.Context, ep, ch string, data []byte, binary bool, key string, metadata map[string]string, expected *int64, ttl int) (ManagedRealtimeChannelMessage, error) {
	ctx, err := ttlContext(ctx, ttl)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	return s.AppendManagedRealtimeChannelConditional(ctx, ep, ch, data, binary, key, metadata, expected)
}
func (s *MemStore) AppendManagedRealtimeChannelExpiring(ctx context.Context, ep, ch string, data []byte, binary bool, key string, metadata map[string]string, expected *int64, ttl int) (ManagedRealtimeChannelMessage, error) {
	ctx, err := ttlContext(ctx, ttl)
	if err != nil {
		return ManagedRealtimeChannelMessage{}, err
	}
	return s.AppendManagedRealtimeChannelConditional(ctx, ep, ch, data, binary, key, metadata, expected)
}
func loadRealtimeExpirations(ctx context.Context, db ttlq.DBTX, ep, ch string, messages []ManagedRealtimeChannelMessage) error {
	sequences := make([]int64, len(messages))
	for i := range messages {
		sequences[i] = messages[i].Sequence
	}
	if len(sequences) == 0 {
		return nil
	}
	rows, err := new(ttlq.Queries).ReadExpirations(ctx, db, ttlq.ReadExpirationsParams{EndpointID: ep, Channel: ch, Sequences: sequences})
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
func saveRealtimeExpiration(ctx context.Context, db ttlq.DBTX, message *ManagedRealtimeChannelMessage) error {
	if message.TTLSeconds == 0 {
		return nil
	}
	if message.ExpiresAt == nil {
		expiry := message.CreatedAt.Add(time.Duration(message.TTLSeconds) * time.Second)
		message.ExpiresAt = &expiry
	}
	return new(ttlq.Queries).SaveExpiration(ctx, db, ttlq.SaveExpirationParams{EndpointID: message.EndpointID, Channel: message.Channel, Sequence: message.Sequence, TtlSeconds: int32(message.TTLSeconds), ExpiresAt: pgtype.Timestamptz{Time: *message.ExpiresAt, Valid: true}})
}

// Expired is a delivery decision, not a retention hole or a reducer rollback.
func (m ManagedRealtimeChannelMessage) Expired(now time.Time) bool {
	return m.ExpiresAt != nil && !now.Before(*m.ExpiresAt)
}
