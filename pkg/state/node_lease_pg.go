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

var _ NodeLeaseStore = (*PgStore)(nil)

func (s *PgStore) RenewNodeLease(ctx context.Context, nodeID, holder string, ttl time.Duration) (NodeLease, error) {
	if err := validateNodeLeaseRenewal(nodeID, holder, ttl); err != nil {
		return NodeLease{}, err
	}
	row, err := sqlc.New().RenewNodeLease(ctx, s.pool, sqlc.RenewNodeLeaseParams{
		NodeID:     mustPgUUID(nodeID),
		Holder:     holder,
		TtlSeconds: ttl.Seconds(),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeLease{}, ErrNodeLeaseHeld
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23503" {
		return NodeLease{}, ErrNotFound
	}
	if err != nil {
		return NodeLease{}, fmt.Errorf("renew node lease: %w", err)
	}
	return NodeLease{
		NodeID: row.NodeID, Holder: row.Holder, Epoch: row.Epoch,
		RenewedAt: row.RenewedAt.Time, ExpiresAt: row.ExpiresAt.Time, DBNow: row.DbNow.Time,
	}, nil
}

func (s *PgStore) GetNodeLease(ctx context.Context, nodeID string) (NodeLease, error) {
	if err := validateSnapshotRecoveryIDs(nodeID); err != nil {
		return NodeLease{}, err
	}
	row, err := sqlc.New().GetNodeLease(ctx, s.pool, mustPgUUID(nodeID))
	if errors.Is(err, pgx.ErrNoRows) {
		return NodeLease{}, ErrNotFound
	}
	if err != nil {
		return NodeLease{}, fmt.Errorf("read node lease: %w", err)
	}
	return NodeLease{
		NodeID: row.NodeID, Holder: row.Holder, Epoch: row.Epoch,
		RenewedAt: row.RenewedAt.Time, ExpiresAt: row.ExpiresAt.Time, DBNow: row.DbNow.Time,
	}, nil
}
