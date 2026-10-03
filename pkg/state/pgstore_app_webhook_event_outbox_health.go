package state

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgtype"
)

func (s *PgStore) AppWebhookEventOutboxHealth(ctx context.Context) (AppWebhookEventOutboxHealth, error) {
	var health AppWebhookEventOutboxHealth
	var oldest pgtype.Timestamptz
	err := s.pool.QueryRow(ctx, `
		select count(*), min(created_at)
		  from app_webhook_event_outbox
	`).Scan(&health.PendingCount, &oldest)
	if err != nil {
		return AppWebhookEventOutboxHealth{}, fmt.Errorf("state: app webhook event outbox health: %w", err)
	}
	if oldest.Valid {
		at := oldest.Time
		health.OldestPendingAt = &at
	}
	return health, nil
}
