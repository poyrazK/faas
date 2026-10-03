package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const appWakeTransitionBatchMax = 32

func (s *PgStore) BeginAppWakeTransition(ctx context.Context, appID string) (AppWakeTransition, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return AppWakeTransition{}, false, fmt.Errorf("state: begin app wake transition: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var status AppStatus
	var currentTransition string
	err = tx.QueryRow(ctx, `
		select status, coalesce(wake_transition_id::text, '')
		  from apps
		 where id = $1::uuid
		 for update
	`, appID).Scan(&status, &currentTransition)
	if errors.Is(err, pgx.ErrNoRows) {
		return AppWakeTransition{}, false, ErrNotFound
	}
	if err != nil {
		return AppWakeTransition{}, false, fmt.Errorf("state: read app wake transition: %w", err)
	}
	if status == AppActive && currentTransition != "" {
		var pending bool
		if err := tx.QueryRow(ctx, `
			select exists (
				select 1 from app_wake_transitions
				 where id = $1::uuid and completed_at is null and superseded_at is null
			)
		`, currentTransition).Scan(&pending); err != nil {
			return AppWakeTransition{}, false, fmt.Errorf("state: read current app wake transition: %w", err)
		}
		if pending {
			return AppWakeTransition{ID: currentTransition, AppID: appID}, false, nil
		}
	}
	if status != AppEvictedCold {
		return AppWakeTransition{}, false, nil
	}

	id := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		update app_park_transitions
		   set superseded_at = now()
		 where id = (select park_transition_id from apps where id = $1::uuid)
		   and completed_at is null and superseded_at is null
	`, appID); err != nil {
		return AppWakeTransition{}, false, fmt.Errorf("state: supersede app park transition for wake: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into app_wake_transitions (id, app_id) values ($1::uuid, $2::uuid)
	`, id, appID); err != nil {
		return AppWakeTransition{}, false, fmt.Errorf("state: record app wake transition: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update apps
		   set status = $2, park_transition_id = null, wake_transition_id = $3::uuid
		 where id = $1::uuid
	`, appID, string(AppActive), id); err != nil {
		return AppWakeTransition{}, false, fmt.Errorf("state: activate app for wake transition: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AppWakeTransition{}, false, fmt.Errorf("state: commit app wake transition: %w", err)
	}
	return AppWakeTransition{ID: id, AppID: appID}, true, nil
}

// AbortAppWakeTransition rolls a failed initial wake back to evicted_cold only
// while its transition still owns the app. A newer park already supersedes it.
func (s *PgStore) AbortAppWakeTransition(ctx context.Context, transitionID string) (bool, error) {
	if transitionID == "" {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("state: begin app wake abort: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var appID, currentTransition string
	var status AppStatus
	err = tx.QueryRow(ctx, `
		select id::text, status, coalesce(wake_transition_id::text, '')
		  from apps
		 where id = (select app_id from app_wake_transitions where id = $1::uuid)
		 for update
	`, transitionID).Scan(&appID, &status, &currentTransition)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: read app for wake abort: %w", err)
	}
	var pendingTransition string
	err = tx.QueryRow(ctx, `
		select id::text from app_wake_transitions
		 where id = $1::uuid and completed_at is null and superseded_at is null
	 for update
	`, transitionID).Scan(&pendingTransition)
	if errors.Is(err, pgx.ErrNoRows) {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("state: commit completed app wake abort: %w", err)
		}
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: lock app wake for abort: %w", err)
	}
	if currentTransition != transitionID || status != AppActive {
		if _, err := tx.Exec(ctx, `
			update app_wake_transitions set superseded_at = now() where id = $1::uuid
		`, transitionID); err != nil {
			return false, fmt.Errorf("state: supersede stale app wake transition: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("state: commit stale app wake abort: %w", err)
		}
		return false, nil
	}
	if _, err := tx.Exec(ctx, `
		update app_wake_transitions set superseded_at = now() where id = $1::uuid
	`, transitionID); err != nil {
		return false, fmt.Errorf("state: supersede failed app wake: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update apps set status = $2, wake_transition_id = null
		 where id = $1::uuid and status = $3 and wake_transition_id = $4::uuid
	`, appID, string(AppEvictedCold), string(AppActive), transitionID); err != nil {
		return false, fmt.Errorf("state: restore app after failed wake: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: commit failed app wake: %w", err)
	}
	return true, nil
}

func (s *PgStore) CompleteReadyAppWakeTransition(ctx context.Context, transitionID, instanceID, wakeID string) (bool, error) {
	if transitionID == "" || instanceID == "" || wakeID == "" {
		return false, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("state: begin ready app wake completion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	completed, err := completeReadyAppWakeTransitionTx(ctx, tx, transitionID, instanceID, wakeID)
	if err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: commit ready app wake completion: %w", err)
	}
	return completed, nil
}

func (s *PgStore) DrainReadyAppWakeTransitions(ctx context.Context, limit int) (int, error) {
	if limit <= 0 {
		return 0, nil
	}
	limit = min(limit, appWakeTransitionBatchMax)
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("state: begin ready app wake reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
		update app_wake_transitions t
		   set superseded_at = now()
		 where t.id in (
		       select stale.id
		         from app_wake_transitions stale
		         left join apps a on a.id = stale.app_id
		         left join accounts ac on ac.id = a.account_id
		        where stale.completed_at is null and stale.superseded_at is null
		          and (a.id is null or a.status <> $1 or a.wake_transition_id is distinct from stale.id
		               or ac.status not in ('active', 'past_due') or ac.abuse_hold_at is not null)
	        order by stale.requested_at, stale.id
	        for update of stale skip locked
	        limit $2
	         )
	`, string(AppActive), limit); err != nil {
		return 0, fmt.Errorf("state: supersede stale app wake transitions: %w", err)
	}
	rows, err := tx.Query(ctx, `
		select t.id::text, i.id::text, i.wake_id::text
		  from app_wake_transitions t
		  join apps a on a.id = t.app_id
		  join accounts ac on ac.id = a.account_id and ac.status in ('active', 'past_due') and ac.abuse_hold_at is null
		  join lateral (
		       select ready.id, ready.wake_id
		         from instances ready
		        where ready.app_id = a.id and ready.state = 'running'
		        order by ready.started_at, ready.id
		        limit 1
		  ) i on true
		 where t.completed_at is null and t.superseded_at is null
		   and a.status = $1 and a.wake_transition_id = t.id
		order by t.requested_at, t.id
		for update of a skip locked
		limit $2
	`, string(AppActive), limit)
	if err != nil {
		return 0, fmt.Errorf("state: claim ready app wake transitions: %w", err)
	}
	type readyWake struct{ transitionID, instanceID, wakeID string }
	var ready []readyWake
	for rows.Next() {
		var row readyWake
		if err := rows.Scan(&row.transitionID, &row.instanceID, &row.wakeID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("state: scan ready app wake transition: %w", err)
		}
		ready = append(ready, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return 0, fmt.Errorf("state: read ready app wake transitions: %w", err)
	}
	rows.Close()
	completed := 0
	for _, row := range ready {
		ok, err := completeReadyAppWakeTransitionTx(ctx, tx, row.transitionID, row.instanceID, row.wakeID)
		if err != nil {
			return 0, err
		}
		if ok {
			completed++
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: commit ready app wake reconciliation: %w", err)
	}
	return completed, nil
}

func completeReadyAppWakeTransitionTx(ctx context.Context, tx pgx.Tx, transitionID, instanceID, wakeID string) (bool, error) {
	var appID, accountID, slug string
	err := tx.QueryRow(ctx, `
		select a.id::text, a.account_id::text, a.slug
		  from apps a
		  join accounts ac on ac.id = a.account_id and ac.status in ('active', 'past_due') and ac.abuse_hold_at is null
		  join app_wake_transitions t on t.app_id = a.id
		 where t.id = $1::uuid and t.completed_at is null and t.superseded_at is null
		   and a.status = $2 and a.wake_transition_id = t.id
		 for update of a
	`, transitionID, string(AppActive)).Scan(&appID, &accountID, &slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: lock app for ready wake transition: %w", err)
	}
	var lockedTransition string
	err = tx.QueryRow(ctx, `
		select id::text from app_wake_transitions
		 where id = $1::uuid and completed_at is null and superseded_at is null
		 for update
	`, transitionID).Scan(&lockedTransition)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: lock ready app wake transition: %w", err)
	}
	var ready bool
	err = tx.QueryRow(ctx, `
		select true from instances
		 where id = $1::uuid and app_id = $2::uuid and wake_id = $3::uuid and state = 'running'
		 for update
	`, instanceID, appID, wakeID).Scan(&ready)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("state: lock ready app instance: %w", err)
	}
	now := time.Now().UTC()
	payload, err := appWokenWebhookPayload(App{ID: appID, AccountID: accountID, Slug: slug}, instanceID, wakeID, now)
	if err != nil {
		return false, fmt.Errorf("state: encode app woken webhook payload: %w", err)
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
	`, accountID, appID, string(AppWebhookEventAppWoken), transitionID, string(payload)); err != nil {
		return false, fmt.Errorf("state: enqueue app woken webhook: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update app_wake_transitions
		   set completed_at = $2, instance_id = $3::uuid, wake_id = $4::uuid
		 where id = $1::uuid and completed_at is null and superseded_at is null
	`, transitionID, now, instanceID, wakeID); err != nil {
		return false, fmt.Errorf("state: complete app wake transition: %w", err)
	}
	return true, nil
}
