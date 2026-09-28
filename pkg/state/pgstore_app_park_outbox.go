package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const appParkTransitionBatchMax = 32

func (s *PgStore) BeginAppParkTransition(ctx context.Context, appID string, expected AppStatus) (AppParkTransition, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppParkTransition{}, false, fmt.Errorf("state: begin app park transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var current AppStatus
	var transitionID string
	err = tx.QueryRow(ctx, `
		select status, coalesce(park_transition_id::text, '')
		  from apps
		 where id = $1::uuid
		 for update
	`, appID).Scan(&current, &transitionID)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppParkTransition{}, false, ErrNotFound
	}
	if err != nil {
		return AppParkTransition{}, false, fmt.Errorf("state: read app park transition: %w", err)
	}
	if current == AppEvictedCold && transitionID != "" {
		return AppParkTransition{ID: transitionID, AppID: appID}, false, nil
	}
	if current != AppEvictedCold && current != expected {
		return AppParkTransition{}, false, nil
	}

	transitionID = uuid.NewString()
	if _, err := tx.Exec(ctx, `delete from app_park_transitions where app_id = $1::uuid`, appID); err != nil {
		return AppParkTransition{}, false, fmt.Errorf("state: replace old app park transition: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into app_park_transitions (id, app_id) values ($1::uuid, $2::uuid)
	`, transitionID, appID); err != nil {
		return AppParkTransition{}, false, fmt.Errorf("state: record app park transition: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update apps
		   set status = $2, park_transition_id = $3::uuid
		 where id = $1::uuid
	`, appID, string(AppEvictedCold), transitionID); err != nil {
		return AppParkTransition{}, false, fmt.Errorf("state: update app park status: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AppParkTransition{}, false, fmt.Errorf("state: commit app park transition: %w", err)
	}
	return AppParkTransition{ID: transitionID, AppID: appID}, current != AppEvictedCold, nil
}

func (s *PgStore) CompleteDrainedAppParkTransition(ctx context.Context, transitionID string) (bool, error) {
	if transitionID == "" {
		return false, nil
	}
	n, err := s.drainDrainedAppParkTransitions(ctx, 1, transitionID)
	return n == 1, err
}

func (s *PgStore) DrainDrainedAppParkTransitions(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	return s.drainDrainedAppParkTransitions(ctx, min(limit, appParkTransitionBatchMax), "")
}

// The transition is the durable recovery record. This worker only creates the
// customer event after the app is still parked and the instance ledger has no
// live rows; completing the transition and inserting the event share a tx.
func (s *PgStore) drainDrainedAppParkTransitions(ctx context.Context, limit int, transitionFilter string) (int, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("state: begin drained app park reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		update app_park_transitions p
		   set superseded_at = now()
		 where p.id in (
		       select stale.id
		         from app_park_transitions stale
		        where stale.completed_at is null and stale.superseded_at is null
		          and ($1::uuid is null or stale.id = $1::uuid)
		          and not exists (
		              select 1 from apps a
		               where a.id = stale.app_id and a.status = $2 and a.park_transition_id = stale.id
		          )
	        order by stale.requested_at, stale.id
	        for update skip locked
	        limit $3
	   )
	`, nullableUUID(transitionFilter), string(AppEvictedCold), limit); err != nil {
		return 0, fmt.Errorf("state: supersede stale app park transitions: %w", err)
	}
	rows, err := tx.Query(ctx, `
		select p.id::text, a.id::text, a.account_id::text, a.slug
		  from app_park_transitions p
		  join apps a on a.id = p.app_id
		 where p.completed_at is null and p.superseded_at is null
		   and ($1::uuid is null or p.id = $1::uuid)
		   and a.status = $2 and a.park_transition_id = p.id
		   and not exists (
		       select 1 from instances i
		        where i.app_id = a.id
		          and i.state in ('waking','cold_booting','running','draining','snapshotting','migrating','warm')
		   )
		 order by p.requested_at, p.id
		 for update of a skip locked
		 limit $3
	`, nullableUUID(transitionFilter), string(AppEvictedCold), limit)
	if err != nil {
		return 0, fmt.Errorf("state: claim drained app park transitions: %w", err)
	}
	type appParkRow struct{ id, appID, accountID, slug string }
	var pending []appParkRow
	for rows.Next() {
		var row appParkRow
		if err := rows.Scan(&row.id, &row.appID, &row.accountID, &row.slug); err != nil {
			rows.Close()
			return 0, fmt.Errorf("state: scan drained app park transition: %w", err)
		}
		pending = append(pending, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("state: read drained app park transitions: %w", err)
	}
	rows.Close()
	for _, row := range pending {
		now := time.Now().UTC()
		payload, err := appParkedWebhookPayload(App{ID: row.appID, Slug: row.slug}, now)
		if err != nil {
			return 0, fmt.Errorf("state: encode app parked webhook payload: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			insert into app_webhook_event_outbox
				(account_id, app_id, event, source_id, payload, recipient_webhook_ids)
			select $1::uuid, $2::uuid, $3, $4::uuid, $5::jsonb, array_agg(h.id order by h.id)
			  from app_webhooks h
			 where h.account_id = $1::uuid and h.app_id = $2::uuid
			   and h.scope = 'app' and h.enabled
			   and (cardinality(h.event_filter) = 0 or $3 = any(h.event_filter))
			having count(*) > 0
			on conflict (event, source_id) do nothing
		`, row.accountID, row.appID, string(AppWebhookEventAppParked), row.id, string(payload)); err != nil {
			return 0, fmt.Errorf("state: enqueue drained app parked webhook: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			update app_park_transitions set completed_at = $2
			 where id = $1::uuid and completed_at is null and superseded_at is null
		`, row.id, now); err != nil {
			return 0, fmt.Errorf("state: complete app park transition: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: commit drained app park transitions: %w", err)
	}
	return len(pending), nil
}
