package state

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ BindingPromotionStore = (*PgStore)(nil)

func (s *PgStore) BindingPromotionBackend() any { return s.pool }
func (s *PgStore) ReadBindingPromotionRevision(ctx context.Context, accountID, appID string) (string, error) {
	revision, err := sqlc.New().ReadBindingPromotionRevision(ctx, s.pool, sqlc.ReadBindingPromotionRevisionParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("state: read binding promotion revision: %w", err)
	}
	return revision, nil
}

func (s *PgStore) PromoteDeploymentWithBindings(ctx context.Context, id string, fence BindingPromotionFence, serving string) (BindingPromotionResult, error) {
	// Reuse the existing deployment reader before entering the transaction.
	// All fields needed by the guard are covered by the supplied revision;
	// its locked comparison rejects any intervening committed change.
	snapshot, err := s.DeploymentByID(ctx, id)
	if err != nil {
		return BindingPromotionResult{}, fmt.Errorf("state: read binding promotion deployment: %w", err)
	}
	guard := &bindingTrafficGuard{fence: fence, snapshot: snapshot}
	var expected []string
	if serving != "" {
		expected = []string{serving}
	}
	deployment, err := s.updateDeploymentTraffic(ctx, id, 100, expected, guard)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && (pgErr.Code == "40P01" || pgErr.Code == "40001") {
		err = ErrBindingPromotionChanged
	}
	return BindingPromotionResult{Deployment: deployment, FromPercent: guard.fromPercent, CheckedAt: guard.checkedAt}, err
}

func (s *PgStore) checkBindingTrafficGuard(ctx context.Context, tx pgx.Tx, guard *bindingTrafficGuard) (Deployment, error) {
	revision, err := sqlc.New().LockBindingPromotionRevision(ctx, tx, sqlc.LockBindingPromotionRevisionParams{AccountID: mustPgUUID(guard.fence.AccountID), AppID: mustPgUUID(guard.fence.AppID)})
	if errors.Is(err, pgx.ErrNoRows) {
		return Deployment{}, ErrBindingPromotionChanged
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("state: lock binding promotion revision: %w", err)
	}
	deployment := guard.snapshot
	if err := guard.check(deployment, guard.fence.AccountID, revision, time.Now()); err != nil {
		return Deployment{}, err
	}
	return deployment, nil
}
