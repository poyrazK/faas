package state

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// ActiveSnapshotRestoreCounts reads the other schedds' in-flight restores
// in one round trip without a lock (ADR 20261103195512). Expired leases are
// filtered here and deleted by the release path.
func (s *PgStore) ActiveSnapshotRestoreCounts(ctx context.Context, excludeLeaseIDs []string) (map[string]int, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("state: pgstore has nil pool")
	}
	if excludeLeaseIDs == nil {
		excludeLeaseIDs = []string{}
	}
	rows, err := s.pool.Query(ctx, `
		select node_id::text, count(*)
		  from snapshot_restore_pressure_leases
		 where expires_at > now()
		   and lease_id::text <> all($1::text[])
		 group by node_id`, excludeLeaseIDs)
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

// ReserveSnapshotRestore inserts the lease in the background: the wake does
// not wait a control-plane round trip for a placement hint (ADR-1741), and
// the reserving schedd counts its own restores locally until the row lands.
func (s *PgStore) ReserveSnapshotRestore(ctx context.Context, leaseID, nodeID string, ttl time.Duration) (func(), error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("state: pgstore has nil pool")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if leaseID == "" || nodeID == "" || ttl <= 0 {
		return nil, errors.New("state: snapshot restore pressure requires lease id, node id and positive ttl")
	}
	inserted := make(chan struct{})
	insertCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	go func() {
		defer close(inserted)
		defer cancel()
		_, _ = s.pool.Exec(insertCtx, `
			insert into snapshot_restore_pressure_leases (lease_id, node_id, expires_at)
			values ($1::uuid, $2::uuid, now() + ($3 * interval '1 second'))`,
			leaseID, nodeID, ttl.Seconds())
	}()
	var once sync.Once
	return func() { //nolint:contextcheck // Lease cleanup must outlive the canceled request context; cleanup is bounded below.
		once.Do(func() {
			// Best-effort and bounded because expiry is authoritative. The
			// delete also sweeps leases a crashed schedd left behind.
			go func() {
				<-inserted
				deleteCtx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
				defer cancel()
				_, _ = s.pool.Exec(deleteCtx, `
					delete from snapshot_restore_pressure_leases
					 where lease_id = $1::uuid or expires_at <= now()`, leaseID)
			}()
		})
	}, nil
}

var _ SnapshotRestorePressureLeases = (*PgStore)(nil)
