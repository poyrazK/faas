package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Capacity precedes every target/advisory/instance/account lock. The node share
// lock keeps both lifecycle and generation stable until this transaction ends.
func checkNodeAdmissionDB(ctx context.Context, tx pgx.Tx, nodeID string) error {
	expected, bound, err := nodeAdmissionExpectation(ctx, nodeID)
	if err != nil {
		return err
	}
	if _, err := uuid.Parse(nodeID); err != nil {
		return ErrInvalidArgument
	}
	q := sqlc.New()
	if err := q.LockNodeAdmissionPolicy(ctx, tx); err != nil {
		return fmt.Errorf("lock node admission policy: %w", err)
	}
	node, err := q.LockNodeAdmissionTarget(ctx, tx, mustPgUUID(nodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNodeAdmissionStale
	}
	if err != nil {
		return fmt.Errorf("lock node admission target: %w", err)
	}
	if !NodeLifecycle(node.Lifecycle).IsAdmitting() || bound && node.LifecycleGeneration != expected.Generation {
		return ErrNodeAdmissionStale
	}
	return nil
}
