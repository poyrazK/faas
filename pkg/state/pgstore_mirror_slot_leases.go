package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// TryAcquireMirrorSlotLease atomically reserves one of a rule's bounded
// concurrent mirror slots. Locking the mirror_rules row serializes contenders
// for this rule across all gateway replicas without blocking other rules.
func (s *PgStore) TryAcquireMirrorSlotLease(ctx context.Context, ruleID string, limit int, ttl time.Duration) (string, bool, error) {
	if s == nil || s.pool == nil {
		return "", false, fmt.Errorf("state: mirror slot lease postgres pool is not configured")
	}
	if ruleID == "" || limit <= 0 || ttl <= 0 {
		return "", false, fmt.Errorf("state: invalid mirror slot lease policy")
	}
	ruleUUID, err := parsePgUUID(ruleID)
	if err != nil {
		return "", false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return "", false, fmt.Errorf("state: begin mirror slot lease: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()

	q := sqlc.New()
	if _, err := q.LockMirrorRuleForSlotLease(ctx, tx, ruleUUID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, ErrNotFound
		}
		return "", false, fmt.Errorf("state: lock mirror rule %s for slot lease: %w", ruleID, err)
	}
	if _, err := q.DeleteExpiredMirrorSlotLeases(ctx, tx, ruleUUID); err != nil {
		return "", false, fmt.Errorf("state: reap expired mirror slot leases for rule %s: %w", ruleID, err)
	}
	active, err := q.CountActiveMirrorSlotLeases(ctx, tx, ruleUUID)
	if err != nil {
		return "", false, fmt.Errorf("state: count mirror slot leases for rule %s: %w", ruleID, err)
	}
	if active >= int64(limit) {
		if err := tx.Commit(ctx); err != nil {
			return "", false, fmt.Errorf("state: commit mirror slot lease reap for rule %s: %w", ruleID, err)
		}
		return "", false, nil
	}

	leaseID := uuid.NewString()
	leaseUUID, err := parsePgUUID(leaseID)
	if err != nil {
		return "", false, err
	}
	ttlMillis := ttl.Milliseconds()
	if ttlMillis < 1 {
		ttlMillis = 1
	}
	createdLeaseID, err := q.CreateMirrorSlotLease(ctx, tx, sqlc.CreateMirrorSlotLeaseParams{
		LeaseID:   leaseUUID,
		RuleID:    ruleUUID,
		TtlMillis: ttlMillis,
	})
	if err != nil {
		return "", false, fmt.Errorf("state: insert mirror slot lease for rule %s: %w", ruleID, err)
	}
	if err := tx.Commit(ctx); err != nil {
		// Commit errors can be ambiguous. Best-effort deletion avoids holding a
		// slot until expiry if PostgreSQL committed but the reply was lost.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		_ = q.ReleaseMirrorSlotLease(cleanupCtx, s.pool, sqlc.ReleaseMirrorSlotLeaseParams{RuleID: ruleUUID, LeaseID: leaseUUID})
		cancel()
		return "", false, fmt.Errorf("state: commit mirror slot lease for rule %s: %w", ruleID, err)
	}
	return createdLeaseID, true, nil
}

// ReleaseMirrorSlotLease idempotently returns one shared mirror permit.
func (s *PgStore) ReleaseMirrorSlotLease(ctx context.Context, ruleID, leaseID string) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("state: mirror slot lease postgres pool is not configured")
	}
	if ruleID == "" || leaseID == "" {
		return nil
	}
	ruleUUID, err := parsePgUUID(ruleID)
	if err != nil {
		return err
	}
	leaseUUID, err := parsePgUUID(leaseID)
	if err != nil {
		return err
	}
	if err := sqlc.New().ReleaseMirrorSlotLease(ctx, s.pool, sqlc.ReleaseMirrorSlotLeaseParams{RuleID: ruleUUID, LeaseID: leaseUUID}); err != nil {
		return fmt.Errorf("state: release mirror slot lease %s for rule %s: %w", leaseID, ruleID, err)
	}
	return nil
}
