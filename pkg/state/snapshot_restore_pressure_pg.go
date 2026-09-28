package state

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

const snapshotRestorePressureLockName = "snapshot-restore-pressure:v1"

func (s *PgStore) AcquireSnapshotRestorePressure(ctx context.Context) (SnapshotRestorePressureSession, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("state: pgstore has nil pool")
	}
	conn, release, err := s.acquireSessionAdvisoryLockConn(ctx, sessionAdvisoryLock{
		what:      "snapshot restore pressure lock",
		tryLock:   `select pg_try_advisory_lock(hashtextextended($1, 0))`,
		unlock:    `select pg_advisory_unlock(hashtextextended($1, 0))`,
		keyArg:    snapshotRestorePressureLockName,
		retryWait: 10 * time.Millisecond,
	})
	if err != nil {
		return nil, err
	}
	return &pgSnapshotRestorePressureSession{store: s, conn: conn, releaseLock: release}, nil
}

type pgSnapshotRestorePressureSession struct {
	store       *PgStore
	conn        *pgxpool.Conn
	releaseLock func(context.Context)
	once        sync.Once
}

func (s *pgSnapshotRestorePressureSession) ActiveSnapshotRestoreCounts(ctx context.Context) (map[string]int, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := s.conn.Exec(ctx, `delete from snapshot_restore_pressure_leases where expires_at <= now()`); err != nil {
		return nil, fmt.Errorf("state: clean expired snapshot restore pressure leases: %w", err)
	}
	rows, err := s.conn.Query(ctx, `
		select node_id::text, count(*)
		  from snapshot_restore_pressure_leases
		 where expires_at > now()
		 group by node_id`)
	if err != nil {
		return nil, fmt.Errorf("state: list snapshot restore pressure: %w", err)
	}
	defer rows.Close()
	counts := make(map[string]int)
	for rows.Next() {
		var nodeID string
		var count int
		if err := rows.Scan(&nodeID, &count); err != nil {
			return nil, fmt.Errorf("state: scan snapshot restore pressure: %w", err)
		}
		counts[nodeID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: list snapshot restore pressure: %w", err)
	}
	return counts, nil
}

func (s *pgSnapshotRestorePressureSession) ReserveSnapshotRestore(ctx context.Context, nodeID string, ttl time.Duration) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if nodeID == "" || ttl <= 0 {
		return nil, errors.New("state: snapshot restore pressure requires node id and positive ttl")
	}
	leaseID := uuid.NewString()
	if _, err := s.conn.Exec(ctx, `
		insert into snapshot_restore_pressure_leases (lease_id, node_id, expires_at)
		values ($1::uuid, $2::uuid, now() + ($3 * interval '1 second'))`,
		leaseID, nodeID, ttl.Seconds()); err != nil {
		return nil, fmt.Errorf("state: reserve snapshot restore pressure: %w", err)
	}
	var once sync.Once
	return func() { //nolint:contextcheck // Lease cleanup must outlive the canceled request context; cleanup is bounded below.
		once.Do(func() {
			// Cleanup is best-effort because expiry is authoritative; bound the
			// RPC completion path if the pool is contended or the database is down.
			deleteCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			_, _ = s.store.pool.Exec(deleteCtx,
				`delete from snapshot_restore_pressure_leases where lease_id = $1::uuid`, leaseID)
		})
	}, nil
}

func (s *pgSnapshotRestorePressureSession) Close() {
	s.once.Do(func() { s.releaseLock(context.Background()) })
}

var _ SnapshotRestorePressureCoordinator = (*PgStore)(nil)
var _ SnapshotRestorePressureSession = (*pgSnapshotRestorePressureSession)(nil)
