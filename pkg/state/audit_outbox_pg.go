package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var _ AuditEventOutboxStore = (*PgStore)(nil)

// EnqueueAuditEvent inserts an audit handoff idempotently. The unique dedupe
// key is stable across imaged retries, so a notification can be emitted after
// the insert without creating duplicate audit rows.
func (s *PgStore) EnqueueAuditEvent(ctx context.Context, actor, kind string, subject *string, data []byte, dedupeKey string) (int64, error) {
	if err := validateAuditEventOutboxInput(actor, kind, data, dedupeKey); err != nil {
		return 0, err
	}
	var subj *uuid.UUID
	if subject != nil && strings.TrimSpace(*subject) != "" {
		u, err := uuid.Parse(*subject)
		if err == nil {
			subj = &u
		}
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO audit_event_outbox
			(actor, kind, subject, data, dedupe_key)
		VALUES ($1, $2, $3, $4::jsonb, $5)
		ON CONFLICT (dedupe_key) DO UPDATE
		   SET dedupe_key = EXCLUDED.dedupe_key
		RETURNING id
	`, actor, kind, subj, data, dedupeKey).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("state: enqueue audit event: %w", err)
	}
	return id, nil
}

// ClaimAuditEvent atomically leases the oldest available audit handoff. Both
// pending rows and processing rows whose lease expired are recoverable, which
// is the replay path after an apid crash or a lost pg_notify connection.
func (s *PgStore) ClaimAuditEvent(ctx context.Context, consumer string, lease time.Duration) (AuditEventOutbox, error) {
	if strings.TrimSpace(consumer) == "" {
		return AuditEventOutbox{}, errors.New("state: audit event outbox consumer required")
	}
	leaseSeconds := int(auditEventOutboxLease(lease) / time.Second)
	if leaseSeconds < 1 {
		leaseSeconds = 1
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return AuditEventOutbox{}, fmt.Errorf("state: claim audit event begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var item AuditEventOutbox
	var subject *string
	if err := tx.QueryRow(ctx, `
		SELECT id, actor, kind, subject::text, data, dedupe_key, attempts
		  FROM audit_event_outbox
		 WHERE (state = $1 AND available_at <= now())
		    OR (state = $2 AND coalesce(lease_until, now()) <= now())
		 ORDER BY available_at, id
		 FOR UPDATE SKIP LOCKED
		 LIMIT 1
	`, auditEventOutboxStatePending, auditEventOutboxStateProcessing).Scan(
		&item.ID, &item.Actor, &item.Kind, &subject, &item.Data, &item.DedupeKey, &item.Attempts,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AuditEventOutbox{}, ErrNotFound
		}
		return AuditEventOutbox{}, fmt.Errorf("state: claim audit event scan: %w", err)
	}
	item.Subject = subject
	if _, err := tx.Exec(ctx, `
		UPDATE audit_event_outbox
		   SET state = $2,
		       attempts = attempts + 1,
		       claimed_by = $3,
		       claimed_at = now(),
		       lease_until = now() + make_interval(secs => $4)
		 WHERE id = $1
	`, item.ID, auditEventOutboxStateProcessing, consumer, leaseSeconds); err != nil {
		return AuditEventOutbox{}, fmt.Errorf("state: claim audit event update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return AuditEventOutbox{}, fmt.Errorf("state: claim audit event commit: %w", err)
	}
	item.Attempts++
	return cloneAuditEventOutbox(item), nil
}

// DeliverAuditEvent atomically appends the audit row and marks the outbox row
// delivered. The events.outbox_id unique index makes the insert idempotent if
// a future caller re-enters this method after a partial deployment.
func (s *PgStore) DeliverAuditEvent(ctx context.Context, id int64) (bool, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return false, fmt.Errorf("state: deliver audit event begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var state, actor, kind string
	var subjectText *string
	var data []byte
	var deliveredAt *time.Time
	if err := tx.QueryRow(ctx, `
		SELECT state, actor, kind, subject::text, data, delivered_at
		  FROM audit_event_outbox
		 WHERE id = $1
		 FOR UPDATE
	`, id).Scan(&state, &actor, &kind, &subjectText, &data, &deliveredAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, ErrNotFound
		}
		return false, fmt.Errorf("state: deliver audit event scan: %w", err)
	}
	if deliveredAt != nil || state == auditEventOutboxStateDelivered || state == auditEventOutboxStateDeadLetter {
		if err := tx.Commit(ctx); err != nil {
			return false, fmt.Errorf("state: deliver audit event terminal commit: %w", err)
		}
		return false, nil
	}

	var subject *uuid.UUID
	if subjectText != nil {
		u, parseErr := uuid.Parse(*subjectText)
		if parseErr == nil {
			subject = &u
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO events (actor, kind, subject, data, outbox_id)
		VALUES ($1, $2, $3, $4::jsonb, $5)
		ON CONFLICT (outbox_id) WHERE outbox_id IS NOT NULL DO NOTHING
	`, actor, kind, subject, data, id); err != nil {
		return false, fmt.Errorf("state: deliver audit event insert: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE audit_event_outbox
		   SET state = $2,
		       delivered_at = now(),
		       claimed_by = NULL,
		       claimed_at = NULL,
		       lease_until = NULL,
		       last_error = NULL
		 WHERE id = $1
	`, id, auditEventOutboxStateDelivered); err != nil {
		return false, fmt.Errorf("state: deliver audit event mark delivered: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("state: deliver audit event commit: %w", err)
	}
	return true, nil
}

// FailAuditEvent releases a claim for capped exponential retry, or moves it
// to dead_letter once the failure threshold is reached. Terminal rows are
// idempotent no-ops so a late worker cannot undo a successful delivery.
func (s *PgStore) FailAuditEvent(ctx context.Context, id int64, cause error) error {
	message := auditEventOutboxFailureMessage(cause)
	tag, err := s.pool.Exec(ctx, `
		UPDATE audit_event_outbox
		   SET state = CASE
		                 WHEN attempts >= $2 THEN $3
		                 ELSE $4
		               END,
		       available_at = CASE
		                       WHEN attempts >= $2 THEN available_at
		                       ELSE now() + make_interval(secs => least($5, $6 * power(2, greatest(attempts - 1, 0)))::int)
		                     END,
		       claimed_by = NULL,
		       claimed_at = NULL,
		       lease_until = NULL,
		       last_error = $7
		 WHERE id = $1
	   AND state IN ($8, $9)
	`, id, AuditEventOutboxMaxAttempts, auditEventOutboxStateDeadLetter, auditEventOutboxStatePending,
		int(auditEventOutboxMaxRetryDelay/time.Second), int(auditEventOutboxInitialRetryDelay/time.Second), message,
		auditEventOutboxStatePending, auditEventOutboxStateProcessing)
	if err != nil {
		return fmt.Errorf("state: fail audit event: %w", err)
	}
	if tag.RowsAffected() > 0 {
		return nil
	}
	var exists bool
	if err := s.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM audit_event_outbox WHERE id = $1)`, id).Scan(&exists); err != nil {
		return fmt.Errorf("state: check failed audit event: %w", err)
	}
	if !exists {
		return ErrNotFound
	}
	return nil
}

// PruneAuditEventOutbox removes only terminal queue metadata. The resulting
// events row is retained by the migration's ON DELETE SET NULL foreign key.
func (s *PgStore) PruneAuditEventOutbox(ctx context.Context, before time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM audit_event_outbox
		 WHERE state IN ($1, $2)
		   AND coalesce(delivered_at, created_at) < $3
	`, auditEventOutboxStateDelivered, auditEventOutboxStateDeadLetter, before)
	if err != nil {
		return 0, fmt.Errorf("state: prune audit event outbox: %w", err)
	}
	return tag.RowsAffected(), nil
}
