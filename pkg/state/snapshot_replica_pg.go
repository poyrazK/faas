package state

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	snapshotReplicaInitialRetryDelay = 5 * time.Second
	snapshotReplicaMaxRetryDelay     = 5 * time.Minute
	snapshotReplicaRevalidateAfter   = 5 * time.Minute
)

func snapshotReplicaRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	delay := snapshotReplicaInitialRetryDelay
	for i := 1; i < attempt && delay < snapshotReplicaMaxRetryDelay; i++ {
		delay *= 2
		if delay > snapshotReplicaMaxRetryDelay {
			delay = snapshotReplicaMaxRetryDelay
		}
	}
	return delay
}

// EnqueueSnapshotReplicasForNode consumes the global snapshot fan-out event
// stream for one node. The cursor makes the normal path proportional to new
// snapshots rather than to the complete snapshots table. It is safe to call
// on every worker tick and also repairs jobs missed while a node or schedd
// was offline.
func (s *PgStore) EnqueueSnapshotReplicasForNode(ctx context.Context, nodeID string) (int, error) {
	if nodeID == "" {
		return 0, errors.New("state: enqueue snapshot replicas: node_id required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("state: enqueue snapshot replicas begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// The cursor must never advance past an event that was visible to the
	// max(id) query but not to the INSERT query. A repeatable-read snapshot
	// makes both statements observe the same event set; an event committed
	// concurrently is picked up on the next worker tick.
	if _, err := tx.Exec(ctx, `set transaction isolation level repeatable read`); err != nil {
		return 0, fmt.Errorf("state: enqueue snapshot replicas isolation: %w", err)
	}

	var active bool
	if err := tx.QueryRow(ctx, `
		select active from compute_nodes where id = $1`, nodeID).Scan(&active); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("state: enqueue snapshot replicas node lookup: %w", err)
	}
	if !active {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("state: enqueue snapshot replicas inactive commit: %w", err)
		}
		return 0, nil
	}

	if _, err := tx.Exec(ctx, `
		insert into snapshot_replica_cursors (node_id)
		values ($1)
		on conflict (node_id) do nothing`, nodeID); err != nil {
		return 0, fmt.Errorf("state: enqueue snapshot replicas cursor init: %w", err)
	}
	var lastEventID int64
	if err := tx.QueryRow(ctx, `
		select last_event_id
		  from snapshot_replica_cursors
		 where node_id = $1
		 for update`, nodeID).Scan(&lastEventID); err != nil {
		return 0, fmt.Errorf("state: enqueue snapshot replicas cursor read: %w", err)
	}

	tag, err := tx.Exec(ctx, `
		insert into snapshot_replicas (snapshot_id, node_id, region)
		select e.snapshot_id, cn.id, coalesce(cn.region, '')
		  from snapshot_fanout_events e
		  join snapshots sn on sn.id = e.snapshot_id
		  join deployments d on d.id = sn.deployment_id
		  join apps a on a.id = d.app_id
		  cross join compute_nodes cn
		  left join snapshot_origins so on so.snapshot_id = sn.id
		 where e.id > $2
		   and cn.id = $1
		   and cn.active = true
		   and sn.stale = false
		   and sn.storage_key <> ''
		   and a.status <> 'deleted'
		   and d.status in ('snapshotting', 'live', 'superseded')
		   and (so.snapshot_id is null or so.region = '' or so.region = coalesce(cn.region, ''))
		on conflict (snapshot_id, node_id) do nothing`, nodeID, lastEventID)
	if err != nil {
		return 0, fmt.Errorf("state: enqueue snapshot replicas for %s: %w", nodeID, err)
	}
	var latestEventID int64
	if err := tx.QueryRow(ctx, `
		select coalesce(max(id), $1)
		  from snapshot_fanout_events
		 where id > $1`, lastEventID).Scan(&latestEventID); err != nil {
		return 0, fmt.Errorf("state: enqueue snapshot replicas cursor advance: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update snapshot_replica_cursors
		   set last_event_id = $2, updated_at = now()
		 where node_id = $1`, nodeID, latestEventID); err != nil {
		return 0, fmt.Errorf("state: enqueue snapshot replicas cursor update: %w", err)
	}
	revalidated, err := tx.Exec(ctx, `
		update snapshot_replicas r
		   set state = 'pending', ready_at = null, updated_at = now()
		  from snapshots sn
		  join deployments d on d.id = sn.deployment_id
		  join apps a on a.id = d.app_id
		 where r.snapshot_id = sn.id
		   and r.node_id = $1
		   and r.state = 'ready'
		   and r.ready_at <= now() - make_interval(secs => $2)
		   and sn.stale = false
		   and a.status <> 'deleted'
		   and d.status in ('snapshotting', 'live', 'superseded')`,
		nodeID, int(snapshotReplicaRevalidateAfter/time.Second))
	if err != nil {
		return 0, fmt.Errorf("state: enqueue snapshot replicas revalidate: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: enqueue snapshot replicas commit: %w", err)
	}
	return int(tag.RowsAffected() + revalidated.RowsAffected()), nil
}

// RecordSnapshotOrigin lets the reconciler restrict fan-out to the producer's
// region without widening the hot snapshots row or making old rows invalid.
func (s *PgStore) RecordSnapshotOrigin(ctx context.Context, snapshotID, nodeID string) error {
	if snapshotID == "" || nodeID == "" {
		return errors.New("state: record snapshot origin: snapshot_id and node_id required")
	}
	tag, err := s.pool.Exec(ctx, `
		insert into snapshot_origins (snapshot_id, node_id, region)
		select $1, cn.id, coalesce(cn.region, '')
		from compute_nodes cn
		where cn.id = $2
		on conflict (snapshot_id) do update
		set node_id = excluded.node_id, region = excluded.region`, snapshotID, nodeID)
	if err != nil {
		return fmt.Errorf("state: record snapshot origin: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ClaimSnapshotReplica leases one job with row locking. A stale syncing lease
// is reclaimable after five minutes so a crashed node-local worker cannot
// strand a snapshot forever.
func (s *PgStore) ClaimSnapshotReplica(ctx context.Context, nodeID string) (SnapshotReplicaJob, error) {
	if nodeID == "" {
		return SnapshotReplicaJob{}, errors.New("state: claim snapshot replica: node_id required")
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return SnapshotReplicaJob{}, fmt.Errorf("state: claim snapshot replica begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var job SnapshotReplicaJob
	row := tx.QueryRow(ctx, `
		select r.snapshot_id::text,
		       sn.deployment_id::text,
		       sn.storage_key,
		       case when coalesce(sn.tier, 'init') = 'warm'
		            then 'snap/' || sn.deployment_id::text || '/warm/vmstate'
		            else 'snap/' || sn.deployment_id::text || '/vmstate'
		       end,
		       array[
		         case when d.rootfs_key <> '' then d.rootfs_key
		              else 'layers/' || sn.deployment_id::text || '.ext4'
		         end
		       ] || coalesce((
		         select array_agg(dsl.storage_key order by dsl.sidecar_name)
		           from deployment_sidecar_layers dsl
		          where dsl.deployment_id = sn.deployment_id
		            and dsl.storage_key <> ''
		       ), array[]::text[]),
		       coalesce(sn.tier, 'init'),
		       r.node_id::text,
		       coalesce(cn.region, ''),
		       r.attempts
		from snapshot_replicas r
		join snapshots sn on sn.id = r.snapshot_id
		join deployments d on d.id = sn.deployment_id
		join apps a on a.id = d.app_id
		join compute_nodes cn on cn.id = r.node_id
		where r.node_id = $1
		  and sn.stale = false
		  and a.status <> 'deleted'
		  and d.status in ('snapshotting', 'live', 'superseded')
		  and (
				r.state = 'pending'
				or (r.state = 'failed' and r.next_attempt_at is not null)
				or (r.state = 'syncing' and r.updated_at < now() - interval '5 minutes')
			  )
		  and (r.next_attempt_at is null or r.next_attempt_at <= now())
		order by case d.status
		           when 'live' then 0
		           when 'snapshotting' then 1
		           else 2
		         end,
		         sn.created_at desc,
		         r.created_at desc,
		         r.snapshot_id
		for update of r skip locked
		limit 1`, nodeID)
	if err := row.Scan(&job.SnapshotID, &job.DeploymentID, &job.StorageKey,
		&job.VMStateStorageKey, &job.LayerStorageKeys, &job.Tier, &job.NodeID, &job.Region, &job.Attempts); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return SnapshotReplicaJob{}, ErrNotFound
		}
		return SnapshotReplicaJob{}, fmt.Errorf("state: claim snapshot replica scan: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		update snapshot_replicas
		set state = 'syncing', attempts = least(attempts + 1, $3),
		    updated_at = now(), next_attempt_at = null, last_error = null
		where snapshot_id = $1 and node_id = $2`, job.SnapshotID, job.NodeID, snapshotReplicaAttemptCap); err != nil {
		return SnapshotReplicaJob{}, fmt.Errorf("state: claim snapshot replica update: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return SnapshotReplicaJob{}, fmt.Errorf("state: claim snapshot replica commit: %w", err)
	}
	job.Attempts = min(job.Attempts+1, snapshotReplicaAttemptCap)
	job.VMStateStorageKey = SnapshotVMStateKey(Snapshot{DeploymentID: job.DeploymentID, StorageKey: job.StorageKey, Tier: job.Tier})
	return job, nil
}

func (s *PgStore) MarkSnapshotReplicaReady(ctx context.Context, snapshotID, nodeID string) error {
	return s.markSnapshotReplica(ctx, snapshotID, nodeID, string(SnapshotReplicaReady), "", time.Time{})
}

func (s *PgStore) MarkSnapshotReplicaFailed(ctx context.Context, snapshotID, nodeID string, cause error) error {
	message := "snapshot replica failed"
	if cause != nil {
		message = cause.Error()
	}
	message = strings.TrimSpace(message)
	if len(message) > 2048 {
		message = message[:2048]
	}
	if isPermanentSnapshotReplicaError(cause) {
		tag, err := s.pool.Exec(ctx, `
			update snapshot_replicas
			set state = $3, attempts = greatest(attempts, $5), last_error = $4,
			    ready_at = null, updated_at = now(), next_attempt_at = null
			where snapshot_id = $1 and node_id = $2`, snapshotID, nodeID, string(SnapshotReplicaFailed), message, snapshotReplicaAttemptCap)
		if err != nil {
			return fmt.Errorf("state: mark snapshot replica permanent failure: %w", err)
		}
		if tag.RowsAffected() == 0 {
			return ErrNotFound
		}
		return nil
	}

	// attempts was incremented by ClaimSnapshotReplica. Compute the capped
	// exponential delay in SQL so the state transition remains atomic.
	tag, err := s.pool.Exec(ctx, `
		update snapshot_replicas
		set state = $3, last_error = $4, ready_at = null, updated_at = now(),
		    next_attempt_at = now() + make_interval(secs => least($5, $6 * power(2, greatest(attempts - 1, 0)))::int)
		where snapshot_id = $1 and node_id = $2`, snapshotID, nodeID, string(SnapshotReplicaFailed), message,
		int(snapshotReplicaMaxRetryDelay/time.Second), int(snapshotReplicaInitialRetryDelay/time.Second))
	if err != nil {
		return fmt.Errorf("state: mark snapshot replica failed: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) markSnapshotReplica(ctx context.Context, snapshotID, nodeID, status, message string, retryAt time.Time) error {
	if snapshotID == "" || nodeID == "" {
		return errors.New("state: mark snapshot replica: snapshot_id and node_id required")
	}
	var tag interface{ RowsAffected() int64 }
	var err error
	if retryAt.IsZero() {
		tag, err = s.pool.Exec(ctx, `
			update snapshot_replicas
			set state = $3, last_error = null, ready_at = now(), updated_at = now(), next_attempt_at = null
			where snapshot_id = $1 and node_id = $2`, snapshotID, nodeID, status)
	} else {
		tag, err = s.pool.Exec(ctx, `
			update snapshot_replicas
			set state = $3, last_error = $4, ready_at = null, updated_at = now(), next_attempt_at = $5
			where snapshot_id = $1 and node_id = $2`, snapshotID, nodeID, status, message, retryAt)
	}
	if err != nil {
		return fmt.Errorf("state: mark snapshot replica %s: %w", status, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ReadySnapshotReplicaNodes(ctx context.Context, snapshotID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		select node_id::text
		from snapshot_replicas
		where snapshot_id = $1 and state = 'ready'
		order by node_id::text`, snapshotID)
	if err != nil {
		return nil, fmt.Errorf("state: ready snapshot replica nodes: %w", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var nodeID string
		if err := rows.Scan(&nodeID); err != nil {
			return nil, fmt.Errorf("state: ready snapshot replica node scan: %w", err)
		}
		out = append(out, nodeID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("state: ready snapshot replica nodes rows: %w", err)
	}
	return out, nil
}
