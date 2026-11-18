package state

// adr: 20261118160000

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// NodeOwnedSnapshots is what a compute node keeps longest in its cache: the
// live snapshot captures and layers of the apps it owns. Their wakes are
// placed on this node, so evicting one turns the owner's next wake into a
// remote fetch, while a copy of another node's app only serves an overflow.
type NodeOwnedSnapshots struct {
	// Captures holds capture prefixes (SnapshotCapturePrefix).
	Captures map[string]bool
	// Layers holds the app and sidecar layer keys of those deployments.
	Layers map[string]bool
}

// NodeOwnedSnapshotStore is implemented by PgStore and MemStore. vmmd's
// snapshot fan-out reads it on every cache sweep.
type NodeOwnedSnapshotStore interface {
	// NodeOwnedSnapshots lists the live snapshots of the apps nodeID owns.
	NodeOwnedSnapshots(ctx context.Context, nodeID string) (NodeOwnedSnapshots, error)
	// RequeueOwnedSnapshotReplicas queues a fetch onto nodeID of each live
	// snapshot of an app it owns that has no replica row there, or whose
	// replica failed for good (an eviction) longer than retryAfter ago. It
	// returns how many it queued.
	RequeueOwnedSnapshotReplicas(ctx context.Context, nodeID string, retryAfter time.Duration) (int, error)
}

// ownedSnapshotDeployment reports whether a deployment's snapshot serves its
// owner's wakes: the live deployment, or the one about to become live.
func ownedSnapshotDeployment(status DeploymentStatus) bool {
	return status == DeployLive || status == DeploySnapshotting
}

func (s *PgStore) NodeOwnedSnapshots(ctx context.Context, nodeID string) (NodeOwnedSnapshots, error) {
	out := NodeOwnedSnapshots{Captures: map[string]bool{}, Layers: map[string]bool{}}
	if nodeID == "" {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		select sn.storage_key,
		       case when d.rootfs_key <> '' then d.rootfs_key
		            else 'layers/' || d.id::text || '.ext4' end
		  from apps a
		  join deployments d on d.app_id = a.id and d.status in ('live', 'snapshotting')
		  join snapshots sn on sn.deployment_id = d.id and not sn.stale
		 where a.node_id = $1::uuid
		   and a.status <> 'deleted'
		union all
		select '', dsl.storage_key
		  from apps a
		  join deployments d on d.app_id = a.id and d.status in ('live', 'snapshotting')
		  join snapshots sn on sn.deployment_id = d.id and not sn.stale
		  join deployment_sidecar_layers dsl on dsl.deployment_id = d.id
		 where a.node_id = $1::uuid
		   and a.status <> 'deleted'
		   and dsl.storage_key <> ''`, nodeID)
	if err != nil {
		return NodeOwnedSnapshots{}, fmt.Errorf("state: node owned snapshots: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var capture, layer string
		if err := rows.Scan(&capture, &layer); err != nil {
			return NodeOwnedSnapshots{}, fmt.Errorf("state: scan node owned snapshots: %w", err)
		}
		out.add(capture, layer)
	}
	if err := rows.Err(); err != nil {
		return NodeOwnedSnapshots{}, fmt.Errorf("state: node owned snapshots: %w", err)
	}
	return out, nil
}

func (s *PgStore) RequeueOwnedSnapshotReplicas(ctx context.Context, nodeID string, retryAfter time.Duration) (int, error) {
	if nodeID == "" {
		return 0, errors.New("state: requeue owned snapshot replicas: node_id required")
	}
	// The same eligibility the fan-out enqueue uses (non-stale, stored,
	// same region as the capture), narrowed to the apps this node owns. Only
	// a permanent failure (next_attempt_at null: an eviction or a missing
	// object) is reset; a transient one already retries on its own.
	tag, err := s.pool.Exec(ctx, `
		insert into snapshot_replicas (snapshot_id, node_id, region)
		select sn.id, cn.id, coalesce(cn.region, '')
		  from apps a
		  join compute_nodes cn on cn.id = a.node_id and cn.active
		  join deployments d on d.app_id = a.id and d.status in ('live', 'snapshotting')
		  join snapshots sn on sn.deployment_id = d.id and not sn.stale and sn.storage_key <> ''
		  left join snapshot_origins so on so.snapshot_id = sn.id
		 where a.node_id = $1::uuid
		   and a.status <> 'deleted'
		   and (so.snapshot_id is null or so.region = '' or so.region = coalesce(cn.region, ''))
		on conflict (snapshot_id, node_id) do update
		   set state = 'pending', attempts = 0, last_error = null, next_attempt_at = null,
		       ready_at = null, lease_token = null, updated_at = now()
		 where snapshot_replicas.state = 'failed'
		   and snapshot_replicas.next_attempt_at is null
		   and snapshot_replicas.updated_at < now() - make_interval(secs => $2)`,
		nodeID, int(retryAfter/time.Second))
	if err != nil {
		return 0, fmt.Errorf("state: requeue owned snapshot replicas: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (m *MemStore) NodeOwnedSnapshots(_ context.Context, nodeID string) (NodeOwnedSnapshots, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := NodeOwnedSnapshots{Captures: map[string]bool{}, Layers: map[string]bool{}}
	if nodeID == "" {
		return out, nil
	}
	for _, snap := range m.snapshots {
		d, ok := m.ownedSnapshotDeploymentLocked(snap, nodeID)
		if !ok {
			continue
		}
		layer := d.RootfsKey
		if layer == "" {
			layer = "layers/" + d.ID + ".ext4"
		}
		out.add(snap.StorageKey, layer)
		for _, l := range m.deploymentSidecarLayers {
			if l.DeploymentID == d.ID && l.StorageKey != "" {
				out.add("", l.StorageKey)
			}
		}
	}
	return out, nil
}

func (m *MemStore) RequeueOwnedSnapshotReplicas(_ context.Context, nodeID string, retryAfter time.Duration) (int, error) {
	if nodeID == "" {
		return 0, errors.New("memstore: requeue owned snapshot replicas: node_id required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	node, ok := m.computeNodes[nodeID]
	if !ok || !node.Active {
		return 0, nil
	}
	if m.snapshotReplicas == nil {
		m.snapshotReplicas = make(map[snapshotReplicaKey]snapshotReplicaRow)
	}
	now := time.Now()
	queued := 0
	for _, snap := range m.snapshots {
		if snap.StorageKey == "" {
			continue
		}
		if _, ok := m.ownedSnapshotDeploymentLocked(snap, nodeID); !ok {
			continue
		}
		if origin, exists := m.snapshotOrigins[snap.ID]; exists && origin.region != "" && origin.region != nodeRegion(node) {
			continue
		}
		key := snapshotReplicaKey{snapshotID: snap.ID, nodeID: nodeID}
		row, exists := m.snapshotReplicas[key]
		if exists && (row.state != SnapshotReplicaFailed || !row.nextAttemptAt.IsZero() || now.Sub(row.updatedAt) <= retryAfter) {
			continue
		}
		created := row.createdAt
		if !exists {
			created = now
		}
		m.snapshotReplicas[key] = snapshotReplicaRow{region: nodeRegion(node), state: SnapshotReplicaPending, createdAt: created, updatedAt: now}
		queued++
	}
	return queued, nil
}

// ownedSnapshotDeploymentLocked returns snap's deployment when snap is a live
// snapshot of an app nodeID owns. Caller holds m.mu.
func (m *MemStore) ownedSnapshotDeploymentLocked(snap Snapshot, nodeID string) (Deployment, bool) {
	if snap.Stale {
		return Deployment{}, false
	}
	d, ok := m.deployments[snap.DeploymentID]
	if !ok || !ownedSnapshotDeployment(d.Status) {
		return Deployment{}, false
	}
	app, ok := m.apps[d.AppID]
	if !ok || app.Status == AppDeleted || app.NodeID != nodeID {
		return Deployment{}, false
	}
	return d, true
}

func (o *NodeOwnedSnapshots) add(captureKey, layerKey string) {
	if prefix := SnapshotCapturePrefix(captureKey); prefix != "" {
		o.Captures[prefix] = true
	}
	if layerKey != "" {
		o.Layers[layerKey] = true
	}
}
