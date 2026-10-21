package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) MutateOperatorAlertTriage(ctx context.Context, input OperatorAlertTriageMutation) (OperatorAlertTriage, error) {
	m, err := normalizeOperatorAlertTriageMutation(input)
	if err != nil {
		return OperatorAlertTriage{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperatorAlertTriage{}, fmt.Errorf("alert triage transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	// Lock only the exact episode. Collector and retention can serialize on it
	// without any reverse dependency on their singleton locks.
	row, err := q.OperatorAlertTriageLock(ctx, tx, m.EpisodeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return OperatorAlertTriage{}, ErrNotFound
	}
	if err != nil {
		return OperatorAlertTriage{}, fmt.Errorf("lock alert triage: %w", err)
	}
	next, err := applyOperatorAlertTriage(pgAlertTriage(row.TriageRevision, row.TriageAcknowledged, row.TriageOwnerID, row.TriageSnoozedUntil, row.TriageUpdatedAt), m)
	if err != nil {
		return OperatorAlertTriage{}, err
	}
	owner := pgtype.UUID{}
	if next.OwnerID != "" {
		owner = pgtype.UUID{Bytes: uuid.MustParse(next.OwnerID), Valid: true}
	}
	changed, err := q.OperatorAlertTriageUpdate(ctx, tx, sqlc.OperatorAlertTriageUpdateParams{
		ID: m.EpisodeID, Revision: next.Revision, ExpectedRevision: m.ExpectedRevision, Acknowledged: next.Acknowledged,
		OwnerID: owner, SnoozedUntil: triagePGTime(next.SnoozedUntil), UpdatedAt: triagePGTime(next.UpdatedAt),
	})
	if err != nil {
		return OperatorAlertTriage{}, fmt.Errorf("update alert triage: %w", err)
	}
	if changed != 1 {
		return OperatorAlertTriage{}, ErrConflict
	}
	audit, err := operatorAlertTriageAudit(m, next)
	if err != nil {
		return OperatorAlertTriage{}, err
	}
	err = q.OperatorInsertAuditLog(ctx, tx, sqlc.OperatorInsertAuditLogParams{
		ID: pgtype.UUID{Bytes: audit.ID, Valid: true}, Kind: audit.Kind, AccountID: pgtype.UUID{Bytes: *audit.AccountID, Valid: true},
		AccountEmail: pgtype.Text{Valid: true}, Actor: pgtype.Text{String: audit.Actor, Valid: true}, ReceivedAt: pgtype.Timestamptz{Time: audit.ReceivedAt, Valid: true}, Data: audit.Data,
	})
	if err != nil {
		return OperatorAlertTriage{}, fmt.Errorf("audit alert triage: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OperatorAlertTriage{}, fmt.Errorf("commit alert triage: %w", err)
	}
	return next, nil
}

func triagePGTime(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func pgAlertTriage(revision int64, ack bool, owner string, snooze, updated pgtype.Timestamptz) OperatorAlertTriage {
	t := OperatorAlertTriage{Revision: revision, Acknowledged: ack, OwnerID: owner}
	if snooze.Valid {
		at := snooze.Time.UTC()
		t.SnoozedUntil = &at
	}
	if updated.Valid {
		at := updated.Time.UTC()
		t.UpdatedAt = &at
	}
	return t
}
