package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	_ OrgActivityOutboxStore      = (*PgStore)(nil)
	_ OrgActivityEnvMutationStore = (*PgStore)(nil)
)

// UpsertAppEnvInScopeWithActivity persists the env update and its activity
// handoff in one transaction. If either write fails, neither is committed.
func (s *PgStore) UpsertAppEnvInScopeWithActivity(ctx context.Context, accountID, appID, scope, key, value string, entry OrgActivity) (int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("state: begin env activity upsert: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `
		INSERT INTO app_envs (account_id, app_id, scope, key, value)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (app_id, scope, key) DO UPDATE
		   SET value = EXCLUDED.value,
		       updated_at = now()
	`, accountID, appID, scope, key, value); err != nil {
		return 0, fmt.Errorf("state: upsert env with activity: %w", err)
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, entry)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: commit env activity upsert: %w", err)
	}
	return outboxID, nil
}

// DeleteAppEnvInScopeWithActivity deletes an existing env row and inserts its
// activity handoff atomically. A missing env key creates no outbox item.
func (s *PgStore) DeleteAppEnvInScopeWithActivity(ctx context.Context, accountID, appID, scope, key string, entry OrgActivity) (int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("state: begin env activity delete: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `
		DELETE FROM app_envs WHERE account_id = $1 AND app_id = $2 AND scope = $3 AND key = $4
	`, accountID, appID, scope, key)
	if err != nil {
		return 0, fmt.Errorf("state: delete env with activity: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return 0, ErrNotFound
	}
	outboxID, err := enqueueOrgActivityOutboxTx(ctx, tx, entry)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: commit env activity delete: %w", err)
	}
	return outboxID, nil
}

// EnqueueOrgActivityOutbox durably accepts a fact produced by an external or
// multi-step operation whose primary mutation cannot share this database
// transaction. Database-backed mutations should prefer a transactional
// mutation method such as UpsertAppEnvInScopeWithActivity.
func (s *PgStore) EnqueueOrgActivityOutbox(ctx context.Context, entry OrgActivity) (int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return 0, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return 0, fmt.Errorf("state: begin org activity enqueue: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	id, err := enqueueOrgActivityOutboxTx(ctx, tx, entry)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: commit org activity enqueue: %w", err)
	}
	return id, nil
}

func enqueueOrgActivityOutboxTx(ctx context.Context, tx pgx.Tx, entry OrgActivity) (int64, error) {
	entry, err := normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return 0, err
	}
	activity, err := json.Marshal(entry)
	if err != nil {
		return 0, fmt.Errorf("state: marshal org activity outbox: %w", err)
	}
	var id int64
	err = tx.QueryRow(ctx, `
		INSERT INTO org_activity_outbox (org_id, source_type, source_id, activity)
		VALUES ($1, $2, $3, $4::jsonb)
		ON CONFLICT (org_id, source_type, source_id) DO UPDATE
		   SET source_id = EXCLUDED.source_id
		RETURNING id
	`, entry.OrgID, entry.SourceType, entry.SourceID, activity).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("state: enqueue org activity: %w", err)
	}
	return id, nil
}

// ClaimOrgActivityOutbox leases the oldest available timeline fact. Pending
// rows and expired processing leases are recoverable after an apid restart.
func (s *PgStore) ClaimOrgActivityOutbox(ctx context.Context, consumer string, lease time.Duration) (OrgActivityOutboxItem, error) {
	if strings.TrimSpace(consumer) == "" {
		return OrgActivityOutboxItem{}, errors.New("state: org activity outbox consumer required")
	}
	leaseSeconds := int(orgActivityOutboxLease(lease) / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return OrgActivityOutboxItem{}, fmt.Errorf("state: claim org activity begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var item OrgActivityOutboxItem
	var payload []byte
	if err := tx.QueryRow(ctx, `
		SELECT id, activity, attempts
		  FROM org_activity_outbox
		 WHERE (state = $1 AND available_at <= now())
		    OR (state = $2 AND coalesce(lease_until, now()) <= now())
		 ORDER BY available_at, id
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1
	`, orgActivityOutboxStatePending, orgActivityOutboxStateProcessing).Scan(&item.ID, &payload, &item.Attempts); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return OrgActivityOutboxItem{}, ErrNotFound
		}
		return OrgActivityOutboxItem{}, fmt.Errorf("state: claim org activity scan: %w", err)
	}
	if err := json.Unmarshal(payload, &item.Activity); err != nil {
		return OrgActivityOutboxItem{}, fmt.Errorf("state: decode org activity outbox %d: %w", item.ID, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE org_activity_outbox
		   SET state = $2,
		       attempts = attempts + 1,
		       claimed_by = $3,
		       claimed_at = now(),
		       lease_until = now() + make_interval(secs => $4)
		 WHERE id = $1
	`, item.ID, orgActivityOutboxStateProcessing, consumer, leaseSeconds); err != nil {
		return OrgActivityOutboxItem{}, fmt.Errorf("state: claim org activity update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OrgActivityOutboxItem{}, fmt.Errorf("state: claim org activity commit: %w", err)
	}
	item.Attempts++
	return cloneOrgActivityOutboxItem(item), nil
}

// DeliverOrgActivityOutbox appends the customer-facing fact and marks the
// queue row delivered in one transaction. Timeline source uniqueness makes a
// replay harmless even if an older worker already appended the fact.
func (s *PgStore) DeliverOrgActivityOutbox(ctx context.Context, id int64) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, fmt.Errorf("state: deliver org activity begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var queueState string
	var payload []byte
	var deliveredAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT state, activity, delivered_at
		  FROM org_activity_outbox
		 WHERE id = $1
		 FOR UPDATE
	`, id).Scan(&queueState, &payload, &deliveredAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("state: deliver org activity scan: %w", err)
	}
	if deliveredAt != nil || queueState == orgActivityOutboxStateDelivered || queueState == orgActivityOutboxStateDeadLetter {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("state: deliver org activity terminal commit: %w", err)
		}
		return false, nil
	}
	var entry OrgActivity
	if err := json.Unmarshal(payload, &entry); err != nil {
		return false, fmt.Errorf("state: decode org activity outbox %d: %w", id, err)
	}
	entry, err = normalizeOrgActivity(entry, time.Now())
	if err != nil {
		return false, fmt.Errorf("state: validate org activity outbox %d: %w", id, err)
	}
	if err := insertOrgActivityTx(ctx, tx, entry); err != nil {
		return false, fmt.Errorf("state: append org activity outbox %d: %w", id, err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE org_activity_outbox
		   SET state = $2,
		       delivered_at = now(),
		       claimed_by = NULL,
		       claimed_at = NULL,
		       lease_until = NULL,
		       last_error = NULL
		 WHERE id = $1
	`, id, orgActivityOutboxStateDelivered); err != nil {
		return false, fmt.Errorf("state: mark org activity delivered: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: deliver org activity commit: %w", err)
	}
	return true, nil
}

func insertOrgActivityTx(ctx context.Context, tx pgx.Tx, entry OrgActivity) error {
	_, err := tx.Exec(ctx, `
		INSERT INTO org_activity (
			org_id, occurred_at, kind, actor_type, actor_account_id,
			actor_label, resource_type, resource_id, resource_label,
			app_id, project_id, deployment_id, data, source_type, source_id
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12,
			$13::jsonb, $14, $15
		)
		ON CONFLICT (org_id, source_type, source_id) DO NOTHING`,
		entry.OrgID, entry.OccurredAt, entry.Kind, string(entry.ActorType),
		entry.ActorAccountID, entry.ActorLabel, entry.ResourceType,
		nullString(entry.ResourceID), entry.ResourceLabel, entry.AppID,
		entry.ProjectID, entry.DeploymentID, []byte(entry.Data),
		entry.SourceType, entry.SourceID)
	return err
}

// FailOrgActivityOutbox releases a failed row for exponential retry or parks
// it in dead_letter after the bounded attempt count. Raw errors are not stored
// because database errors can contain customer-authored values.
func (s *PgStore) FailOrgActivityOutbox(ctx context.Context, id int64, cause error) error {
	message := orgActivityOutboxFailureMessage(cause)
	tag, err := s.pool.Exec(ctx, `
		UPDATE org_activity_outbox
		   SET state = CASE WHEN attempts >= $2 THEN $3 ELSE $4 END,
		       available_at = CASE
		         WHEN attempts >= $2 THEN available_at
		         ELSE now() + make_interval(secs => least($5, $6 * power(2, greatest(attempts - 1, 0)))::int)
		       END,
		       claimed_by = NULL,
		       claimed_at = NULL,
		       lease_until = NULL,
		       last_error = $7
		 WHERE id = $1 AND state IN ($8, $9)
	`, id, OrgActivityOutboxMaxAttempts, orgActivityOutboxStateDeadLetter, orgActivityOutboxStatePending,
		int(orgActivityOutboxMaxRetryDelay/time.Second), int(orgActivityOutboxInitialRetryDelay/time.Second), message,
		orgActivityOutboxStatePending, orgActivityOutboxStateProcessing)
	if err != nil {
		return fmt.Errorf("state: fail org activity outbox: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM org_activity_outbox WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("state: check failed org activity outbox: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) PruneOrgActivityOutbox(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM org_activity_outbox
		 WHERE state = $1 AND delivered_at < $2
	`, orgActivityOutboxStateDelivered, before)
	if err != nil {
		return 0, fmt.Errorf("state: prune org activity outbox: %w", err)
	}
	return tag.RowsAffected(), nil
}
