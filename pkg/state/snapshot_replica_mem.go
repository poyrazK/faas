package state

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
)

type snapshotReplicaKey struct {
	snapshotID string
	nodeID     string
}

type snapshotReplicaRow struct {
	region        string
	state         SnapshotReplicaState
	attempts      int
	lastError     string
	nextAttemptAt time.Time
	readyAt       time.Time
	updatedAt     time.Time
}

type snapshotOriginRow struct {
	nodeID string
	region string
}

func (m *MemStore) RecordSnapshotOrigin(_ context.Context, snapshotID, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if snapshotID == "" || nodeID == "" {
		return errors.New("memstore: record snapshot origin: snapshot_id and node_id required")
	}
	node, ok := m.computeNodes[nodeID]
	if !ok {
		return ErrNotFound
	}
	if m.snapshotOrigins == nil {
		m.snapshotOrigins = make(map[string]snapshotOriginRow)
	}
	m.snapshotOrigins[snapshotID] = snapshotOriginRow{nodeID: nodeID, region: nodeRegion(node)}
	return nil
}

func (m *MemStore) EnqueueSnapshotReplicasForNode(_ context.Context, nodeID string) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	node, ok := m.computeNodes[nodeID]
	if !ok {
		return 0, ErrNotFound
	}
	if !node.Active {
		return 0, nil
	}
	if m.snapshotReplicas == nil {
		m.snapshotReplicas = make(map[snapshotReplicaKey]snapshotReplicaRow)
	}
	created := 0
	for _, snap := range m.snapshots {
		if snap.Stale || snap.StorageKey == "" {
			continue
		}
		deployment, ok := m.deployments[snap.DeploymentID]
		if !ok {
			continue
		}
		app, ok := m.apps[deployment.AppID]
		if !ok || app.Status == AppDeleted {
			continue
		}
		if _, serviceable := snapshotReplicaDeploymentPriority(deployment.Status); !serviceable {
			continue
		}
		if origin, exists := m.snapshotOrigins[snap.ID]; exists {
			if origin.nodeID == nodeID || (origin.region != "" && origin.region != nodeRegion(node)) {
				continue
			}
		}
		key := snapshotReplicaKey{snapshotID: snap.ID, nodeID: nodeID}
		if _, exists := m.snapshotReplicas[key]; exists {
			continue
		}
		m.snapshotReplicas[key] = snapshotReplicaRow{
			region: nodeRegion(node),
			state:  SnapshotReplicaPending,
		}
		created++
	}
	return created, nil
}

func (m *MemStore) ClaimSnapshotReplica(_ context.Context, nodeID string) (SnapshotReplicaJob, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	var chosen *Snapshot
	var chosenKey snapshotReplicaKey
	var chosenRow snapshotReplicaRow
	chosenPriority := 0
	for i := range m.snapshots {
		snap := &m.snapshots[i]
		if snap.Stale || snap.StorageKey == "" {
			continue
		}
		deployment, ok := m.deployments[snap.DeploymentID]
		if !ok {
			continue
		}
		app, ok := m.apps[deployment.AppID]
		if !ok || app.Status == AppDeleted {
			continue
		}
		priority, serviceable := snapshotReplicaDeploymentPriority(deployment.Status)
		if !serviceable {
			continue
		}
		key := snapshotReplicaKey{snapshotID: snap.ID, nodeID: nodeID}
		row, ok := m.snapshotReplicas[key]
		if !ok || row.attempts >= snapshotReplicaMaxAttempts || row.nextAttemptAt.After(now) {
			continue
		}
		reclaim := row.state == SnapshotReplicaSyncing && now.Sub(row.updatedAt) >= 5*time.Minute
		if row.state != SnapshotReplicaPending && row.state != SnapshotReplicaFailed && !reclaim {
			continue
		}
		if chosen == nil || priority < chosenPriority ||
			(priority == chosenPriority && snap.CreatedAt.After(chosen.CreatedAt)) ||
			(priority == chosenPriority && snap.CreatedAt.Equal(chosen.CreatedAt) && snap.ID < chosen.ID) {
			chosen, chosenKey, chosenRow, chosenPriority = snap, key, row, priority
		}
	}
	if chosen == nil {
		return SnapshotReplicaJob{}, ErrNotFound
	}
	chosenRow.state = SnapshotReplicaSyncing
	chosenRow.attempts++
	chosenRow.updatedAt = now
	chosenRow.nextAttemptAt = time.Time{}
	chosenRow.lastError = ""
	m.snapshotReplicas[chosenKey] = chosenRow
	node := m.computeNodes[nodeID]
	deployment := m.deployments[chosen.DeploymentID]
	mainLayerKey := deployment.RootfsKey
	if mainLayerKey == "" {
		mainLayerKey = "layers/" + chosen.DeploymentID + ".ext4"
	}
	layerKeys := []string{mainLayerKey}
	for _, layer := range m.deploymentSidecarLayers {
		if layer.DeploymentID == chosen.DeploymentID && layer.StorageKey != "" {
			layerKeys = append(layerKeys, layer.StorageKey)
		}
	}
	sort.Strings(layerKeys[1:])
	return SnapshotReplicaJob{
		SnapshotID:        chosen.ID,
		DeploymentID:      chosen.DeploymentID,
		StorageKey:        chosen.StorageKey,
		VMStateStorageKey: snapshotVMStateKey(*chosen),
		LayerStorageKeys:  layerKeys,
		Tier:              snapshotTier(*chosen),
		NodeID:            nodeID,
		Region:            nodeRegion(node),
		Attempts:          chosenRow.attempts,
	}, nil
}

func (m *MemStore) MarkSnapshotReplicaReady(_ context.Context, snapshotID, nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := snapshotReplicaKey{snapshotID: snapshotID, nodeID: nodeID}
	row, ok := m.snapshotReplicas[key]
	if !ok {
		return ErrNotFound
	}
	row.state = SnapshotReplicaReady
	row.readyAt = time.Now()
	row.updatedAt = row.readyAt
	row.lastError = ""
	row.nextAttemptAt = time.Time{}
	m.snapshotReplicas[key] = row
	return nil
}

func (m *MemStore) MarkSnapshotReplicaFailed(_ context.Context, snapshotID, nodeID string, cause error) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	key := snapshotReplicaKey{snapshotID: snapshotID, nodeID: nodeID}
	row, ok := m.snapshotReplicas[key]
	if !ok {
		return ErrNotFound
	}
	message := "snapshot replica failed"
	if cause != nil {
		message = strings.TrimSpace(cause.Error())
	}
	if len(message) > 2048 {
		message = message[:2048]
	}
	row.state = SnapshotReplicaFailed
	row.lastError = message
	row.readyAt = time.Time{}
	row.updatedAt = time.Now()
	if isPermanentSnapshotReplicaError(cause) {
		row.attempts = snapshotReplicaMaxAttempts
		row.nextAttemptAt = time.Time{}
	} else if row.attempts >= snapshotReplicaMaxAttempts {
		row.nextAttemptAt = time.Time{}
	} else {
		row.nextAttemptAt = row.updatedAt.Add(snapshotReplicaRetryDelay(row.attempts))
	}
	m.snapshotReplicas[key] = row
	return nil
}

func (m *MemStore) ReadySnapshotReplicaNodes(_ context.Context, snapshotID string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for key, row := range m.snapshotReplicas {
		if key.snapshotID == snapshotID && row.state == SnapshotReplicaReady {
			out = append(out, key.nodeID)
		}
	}
	sort.Strings(out)
	return out, nil
}

func nodeRegion(n ComputeNode) string {
	if n.Region == nil {
		return ""
	}
	return *n.Region
}

func snapshotTier(s Snapshot) string {
	if s.Tier == SnapshotTierWarm {
		return SnapshotTierWarm
	}
	return SnapshotTierInit
}

func snapshotVMStateKey(s Snapshot) string { return SnapshotVMStateKey(s) }

var _ SnapshotReplicaStore = (*MemStore)(nil)
var _ SnapshotReplicaStore = (*PgStore)(nil)
