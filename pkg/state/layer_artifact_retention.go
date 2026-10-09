package state

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

var ErrLayerArtifactRetired = fmt.Errorf("state: immutable layer artifact is retired: %w", ErrConflict)

const (
	LayerArtifactRetained = "retained"
	LayerArtifactDeleting = "deleting"
	LayerArtifactDeleted  = "deleted"
)

// A deletion identity survives retries. Keys cannot be revived after a claim;
// duplicate backend deletes are safe even when an earlier worker lost contact.
type LayerArtifactDeletion struct {
	StorageKey string
	DeletionID string
	State      string
}

type layerArtifactRetentionRecord struct {
	LayerArtifactDeletion
	deleteRequested bool
}

type LayerArtifactRetentionStore interface {
	ClaimLayerArtifactDeletion(context.Context, string) (LayerArtifactDeletion, bool, error)
	CompleteLayerArtifactDeletion(context.Context, LayerArtifactDeletion) error
	PendingLayerArtifactDeletions(context.Context) ([]LayerArtifactDeletion, error)
}

func validLayerArtifactKey(key string) bool {
	return key != "" && len(key) <= 4096 && !strings.ContainsRune(key, '\x00')
}

func cloneLayerArtifacts(record projectCloneWorkloadRecord) map[string]int64 {
	keys := map[string]int64{record.snapshot.Artifact.RootfsKey: record.snapshot.Artifact.RootfsBytes}
	for _, layer := range record.snapshot.Layers {
		keys[layer.StorageKey] = max(keys[layer.StorageKey], layer.Bytes)
	}
	delete(keys, "")
	return keys
}

func cloneNeedsLayerPins(op ProjectEnvironmentCloneOperation) bool {
	return op.Status != CloneOperationReady && op.Status != CloneOperationCompensated
}

func layerArtifactInstanceActive(raw string) bool {
	switch State(strings.ToLower(raw)) {
	case StateWaking, StateColdBooting, StateRunning, StateSnapshotting, StateMigrating, StateWarm, StateDraining:
		return true
	}
	return raw == "pending"
}

func (m *MemStore) requireInstanceLayerArtifactsLocked(deploymentID, nextState string) error {
	if !layerArtifactInstanceActive(nextState) {
		return nil
	}
	return m.requireLayerArtifactsRetainedLocked(m.deploymentLayerKeysLocked(m.deployments[deploymentID]))
}

func (m *MemStore) requireLayerArtifactsRetainedLocked(keys []string) error {
	for _, key := range keys {
		if !validLayerArtifactKey(key) {
			return ErrInvalidArgument
		}
		if row, ok := m.layerArtifactRetention[key]; ok && row.State != LayerArtifactRetained {
			return ErrLayerArtifactRetired
		}
	}
	for _, key := range keys {
		if _, exists := m.layerArtifactRetention[key]; !exists {
			m.layerArtifactRetention[key] = layerArtifactRetentionRecord{LayerArtifactDeletion: LayerArtifactDeletion{StorageKey: key, State: LayerArtifactRetained}}
		}
	}
	return nil
}

func (m *MemStore) deploymentLayerKeysLocked(d Deployment) []string {
	keys := map[string]bool{}
	if d.RootfsKey != "" {
		keys[d.RootfsKey] = true
	}
	for _, layer := range m.deploymentSidecarLayers {
		if layer.DeploymentID == d.ID && layer.StorageKey != "" {
			keys[layer.StorageKey] = true
		}
	}
	var ordered []string
	for key := range keys {
		ordered = append(ordered, key)
	}
	sort.Strings(ordered)
	return ordered
}

func (m *MemStore) deploymentLayerArtifactReferencedLocked(d Deployment) bool {
	now := time.Now().UTC()
	active := d.DeletedAt == nil && (d.Status == DeployLive || !d.Status.IsTerminal()) || m.workflowRetainsDeploymentLocked(d.ID)
	for _, instance := range m.instances {
		active = active || instance.DeploymentID == d.ID && layerArtifactInstanceActive(instance.State)
	}
	for _, snapshot := range m.snapshots {
		active = active || snapshot.DeploymentID == d.ID && !snapshot.Stale
	}
	for _, alias := range m.deploymentAliases {
		active = active || alias.DeploymentID == d.ID
	}
	for _, release := range m.projectReleaseSets {
		if !release.Active && (release.ExpiresAt == nil || !release.ExpiresAt.After(now)) {
			continue
		}
		for _, member := range release.Members {
			active = active || member.DeploymentID == d.ID
		}
	}
	return active
}

func (m *MemStore) layerArtifactReferencedLocked(key string) bool {
	for _, d := range m.deployments {
		app := m.apps[d.AppID]
		if app.ID == "" || app.Status == AppDeleted || !m.deploymentLayerArtifactReferencedLocked(d) {
			continue
		}
		// Protect canonical builder keys even before their publication stamp.
		prefix := "apps/" + app.Slug + "/" + d.ID
		if key == prefix+".ext4" || strings.HasPrefix(key, prefix+"/") {
			return true
		}
		for _, reference := range m.deploymentLayerKeysLocked(d) {
			if reference == key {
				return true
			}
		}
	}
	for operationID, records := range m.projectEnvironmentCloneWorkloads {
		if !cloneNeedsLayerPins(m.projectEnvironmentCloneOperations[operationID]) {
			continue
		}
		for _, record := range records {
			if app := m.apps[record.AppID]; app.Status != AppDeleted && app.ID != "" && cloneLayerArtifacts(record)[key] > 0 {
				return true
			}
		}
	}
	return false
}

func (m *MemStore) ClaimLayerArtifactDeletion(_ context.Context, key string) (LayerArtifactDeletion, bool, error) {
	if !validLayerArtifactKey(key) {
		return LayerArtifactDeletion{}, false, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.layerArtifactRetention[key]
	if row.State == LayerArtifactDeleted {
		return row.LayerArtifactDeletion, true, nil
	}
	if row.State == LayerArtifactDeleting {
		return row.LayerArtifactDeletion, true, nil
	}
	if m.layerArtifactReferencedLocked(key) {
		row = layerArtifactRetentionRecord{LayerArtifactDeletion: LayerArtifactDeletion{StorageKey: key, State: LayerArtifactRetained}, deleteRequested: true}
		m.layerArtifactRetention[key] = row
		return row.LayerArtifactDeletion, false, nil
	}
	row = layerArtifactRetentionRecord{LayerArtifactDeletion: LayerArtifactDeletion{StorageKey: key, DeletionID: uuid.NewString(), State: LayerArtifactDeleting}, deleteRequested: true}
	m.layerArtifactRetention[key] = row
	return row.LayerArtifactDeletion, true, nil
}

func (m *MemStore) CompleteLayerArtifactDeletion(_ context.Context, claim LayerArtifactDeletion) error {
	if !validLayerArtifactKey(claim.StorageKey) || !validCloneLeaseToken(claim.DeletionID) {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.layerArtifactRetention[claim.StorageKey]
	if !ok || claim.DeletionID == "" || row.DeletionID != claim.DeletionID || row.State == LayerArtifactRetained {
		return ErrConflict
	}
	row.State = LayerArtifactDeleted
	m.layerArtifactRetention[claim.StorageKey] = row
	return nil
}

func (m *MemStore) PendingLayerArtifactDeletions(_ context.Context) ([]LayerArtifactDeletion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rows []LayerArtifactDeletion
	for _, row := range m.layerArtifactRetention {
		if row.State == LayerArtifactDeleting || row.State == LayerArtifactRetained && row.deleteRequested {
			rows = append(rows, row.LayerArtifactDeletion)
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].StorageKey < rows[j].StorageKey })
	return rows, nil
}
