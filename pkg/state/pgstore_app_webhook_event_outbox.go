package state

import (
	"context"
	"fmt"
)

const appWebhookEventOutboxBatchMax = 32

func (s *PgStore) DrainAppWebhookEventOutbox(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	return s.relayAppWebhookEventOutbox(ctx, min(limit, appWebhookEventOutboxBatchMax), nil, nil)
}

func (s *PgStore) RelayAppWebhookEventOutboxSource(ctx context.Context, event AppWebhookEvent, sourceID string) (bool, error) {
	if event == "" || sourceID == "" {
		return false, nil
	}
	n, err := s.relayAppWebhookEventOutbox(ctx, 1, string(event), sourceID)
	return n == 1, err
}

// Row locks serialize competing relays. Inserting the full fan-out and removing
// each event under the same transaction makes a restart retry the whole event.
func (s *PgStore) relayAppWebhookEventOutbox(ctx context.Context, limit int, eventFilter, sourceFilter any) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("state: begin app webhook event relay: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `
		select id
		  from app_webhook_event_outbox
		 where ($1::text is null or event = $1)
		   and ($2::uuid is null or source_id = $2)
		 order by created_at, id
		 for update skip locked
		 limit $3
	`, eventFilter, sourceFilter, limit)
	if err != nil {
		return 0, fmt.Errorf("state: claim app webhook outbox events: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if scanErr := rows.Scan(&id); scanErr != nil {
			rows.Close()
			return 0, fmt.Errorf("state: scan app webhook outbox event: %w", scanErr)
		}
		ids = append(ids, id)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		rows.Close()
		return 0, fmt.Errorf("state: read app webhook outbox events: %w", rowsErr)
	}
	rows.Close()
	for _, id := range ids {
		if _, insertErr := tx.Exec(ctx, `
			insert into app_webhook_deliveries
				(source_event_id, webhook_id, app_id, account_id, event, payload)
			select e.id, h.id, e.app_id, e.account_id, e.event, e.payload
			  from app_webhook_event_outbox e
			  join app_webhooks h on h.id = any(e.recipient_webhook_ids)
			   and h.account_id = e.account_id and h.app_id = e.app_id
			 where e.id = $1::uuid
			on conflict (source_event_id, webhook_id) where source_event_id is not null do nothing
		`, id); insertErr != nil {
			return 0, fmt.Errorf("state: fan out app webhook event %s: %w", id, insertErr)
		}
		if _, deleteErr := tx.Exec(ctx, `delete from app_webhook_event_outbox where id = $1::uuid`, id); deleteErr != nil {
			return 0, fmt.Errorf("state: complete app webhook event %s: %w", id, deleteErr)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: commit app webhook event relay: %w", err)
	}
	return len(ids), nil
}
