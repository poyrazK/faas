package state

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/db"
)

const snapshotRestorePressureLockName = "snapshot-restore-pressure:v1"

var snapshotRestorePressureLock = sessionAdvisoryLock{
	what:      "snapshot restore pressure lock",
	tryLock:   `select pg_try_advisory_lock(hashtextextended($1, 0))`,
	unlock:    `select pg_advisory_unlock(hashtextextended($1, 0))`,
	keyArg:    snapshotRestorePressureLockName,
	retryWait: 10 * time.Millisecond,
}

func (s *PgStore) AcquireSnapshotRestorePressure(ctx context.Context) (SnapshotRestorePressureSession, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("state: pgstore has nil pool")
	}
	if s.admissionFunctionsUsable(ctx) {
		return s.acquireSnapshotRestorePressureCounted(ctx)
	}
	conn, release, err := s.acquireSessionAdvisoryLockConn(ctx, snapshotRestorePressureLock)
	if err != nil {
		return nil, err
	}
	return &pgSnapshotRestorePressureSession{store: s, conn: conn, releaseLock: release}, nil
}

// acquireSnapshotRestorePressureCounted takes the lock and reads the active
// counts in one call (snapshot_restore_pressure_acquire, ADR-1741). A failed
// call may still have granted the session lock, so it closes the session.
func (s *PgStore) acquireSnapshotRestorePressureCounted(ctx context.Context) (SnapshotRestorePressureSession, error) {
	l := snapshotRestorePressureLock
	for {
		conn, err := db.DirectPool(s.pool).Acquire(ctx)
		if err != nil {
			return nil, fmt.Errorf("state: acquire %s connection: %w", l.what, err)
		}
		counts, locked, err := snapshotRestorePressureCounts(ctx, conn)
		if err != nil {
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
			_ = conn.Hijack().Close(closeCtx)
			cancel()
			if ctx.Err() != nil {
				err = ctx.Err()
			}
			return nil, fmt.Errorf("state: acquire %s: %w", l.what, err)
		}
		if locked {
			return &pgSnapshotRestorePressureSession{store: s, conn: conn, releaseLock: releaseSessionAdvisoryLock(conn, l), counts: counts}, nil
		}
		conn.Release()
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("state: acquire %s: %w", l.what, ctx.Err())
		case <-time.After(l.retryWait):
		}
	}
}

func snapshotRestorePressureCounts(ctx context.Context, conn *pgxpool.Conn) (map[string]int, bool, error) {
	rows, err := conn.Query(ctx, `select locked, node_id, active from snapshot_restore_pressure_acquire($1)`, snapshotRestorePressureLockName)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	counts, locked := make(map[string]int), false
	for rows.Next() {
		var nodeID *string
		var active *int64
		if err := rows.Scan(&locked, &nodeID, &active); err != nil {
			return nil, false, err
		}
		if nodeID != nil && active != nil {
			counts[*nodeID] = int(*active)
		}
	}
	return counts, locked, rows.Err()
}

type pgSnapshotRestorePressureSession struct {
	store       *PgStore
	conn        *pgxpool.Conn
	releaseLock func(context.Context)
	once        sync.Once
	// counts were read with the lock (ADR-1741); nil means read them.
	counts map[string]int
	// inserted closes when a reservation's lease insert has finished on
	// conn; the unlock and the lease's release wait for it.
	inserted chan struct{}
}

func (s *pgSnapshotRestorePressureSession) ActiveSnapshotRestoreCounts(ctx context.Context) (map[string]int, error) {
	if s.counts != nil {
		return maps.Clone(s.counts), nil
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// One round trip on the wake path: the cleanup CTE always runs to
	// completion, and the count filters on expiry itself, so reading the
	// pre-delete snapshot returns the same live counts.
	rows, err := s.conn.Query(ctx, `
		with expired as (
			delete from snapshot_restore_pressure_leases where expires_at <= now()
		)
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
	insert := func(ctx context.Context) error {
		_, err := s.conn.Exec(ctx, `
		insert into snapshot_restore_pressure_leases (lease_id, node_id, expires_at)
		values ($1::uuid, $2::uuid, now() + ($3 * interval '1 second'))`,
			leaseID, nodeID, ttl.Seconds())
		return err
	}
	if s.counts == nil {
		if err := insert(ctx); err != nil {
			return nil, fmt.Errorf("state: reserve snapshot restore pressure: %w", err)
		}
	} else {
		// ADR-1741: the insert finishes in the background on the locked
		// session and the unlock waits for it, so the next holder still
		// counts this lease; the wake does not wait for it. A lease only
		// steers placement, and one that fails to land costs only that.
		s.inserted = make(chan struct{})
		insertCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		go func() {
			defer close(s.inserted)
			defer cancel()
			_ = insert(insertCtx)
		}()
	}
	inserted := s.inserted
	var once sync.Once
	return func() { //nolint:contextcheck // Lease cleanup must outlive the canceled request context; cleanup is bounded below.
		once.Do(func() {
			// Cleanup is best-effort because expiry is authoritative, so it runs
			// in the background: the wake that releases the lease does not wait
			// a control-plane round trip before publishing (ADR-1741). The bound
			// keeps a contended pool or a down database from piling up deletes.
			go func() {
				if inserted != nil {
					<-inserted
				}
				deleteCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				_, _ = s.store.pool.Exec(deleteCtx,
					`delete from snapshot_restore_pressure_leases where lease_id = $1::uuid`, leaseID)
			}()
		})
	}, nil
}

// Close releases the lock off the caller's path. The unlock runs on the
// session connection after any reserve insert has committed there, so the
// next holder still counts it; contenders retry until it lands.
func (s *pgSnapshotRestorePressureSession) Close() {
	s.once.Do(func() {
		inserted := s.inserted
		go func() {
			if inserted != nil {
				<-inserted
			}
			s.releaseLock(context.Background())
		}()
	})
}

var _ SnapshotRestorePressureCoordinator = (*PgStore)(nil)
var _ SnapshotRestorePressureSession = (*pgSnapshotRestorePressureSession)(nil)
