package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ MigrationSourceStore = (*MemStore)(nil)

func (m *MemStore) RecordMigrationSourceSnapshot(ctx context.Context, lease, node, memory, vmstate string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if memory == "" || vmstate == "" {
		return ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, l := m.migrationSources[lease], m.migrationLeases[lease]
	if r.Phase != MigrationSourceBound || r.SourceNodeID != node || m.migrationResumeFences[lease] || !time.Now().Before(l.LeaseExpiresAt) || l.MemStorageKey != "" && l.MemStorageKey != memory || l.VMStateStorageKey != "" && l.VMStateStorageKey != vmstate {
		return ErrConflict
	}
	l.MemStorageKey, l.VMStateStorageKey = memory, vmstate
	m.migrationLeases[lease] = l
	return nil
}

func (m *MemStore) BindMigrationSource(ctx context.Context, b MigrationSourceBinding) (MigrationSource, error) {
	if err := b.Validate(); err != nil {
		return MigrationSource{}, err
	}
	if err := ctx.Err(); err != nil {
		return MigrationSource{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.migrationSources[b.LeaseToken]; ok {
		if r.MigrationSourceBinding != b {
			return MigrationSource{}, ErrConflict
		}
		return r, nil
	}
	i, l := m.instances[b.InstanceID], m.migrationLeases[b.LeaseToken]
	n, exists := m.computeNodes[b.SourceNodeID]
	if !exists || n.Lifecycle == NodeLifecycleMaintenance || n.Lifecycle == NodeLifecycleRetired || i.NodeID != b.SourceNodeID || i.WakeID != b.SourceWakeID || !IsLive(i.State) || l.InstanceID != b.InstanceID || l.SourceNodeID != b.SourceNodeID || m.migrationResumeFences[b.LeaseToken] {
		return MigrationSource{}, ErrConflict
	}
	for _, r := range m.migrationSources {
		if r.InstanceID == b.InstanceID && r.SourceNodeID == b.SourceNodeID && r.Pending() {
			return MigrationSource{}, ErrConflict
		}
	}
	c := m.migrationInstanceCostLocked(i)
	r := MigrationSource{MigrationSourceBinding: b, RAMMB: int(c.RAM), CPUMillicores: int(c.CPU), VCPU: int(c.VCPU), Phase: MigrationSourceBound}
	if m.migrationSources == nil {
		m.migrationSources = make(map[string]MigrationSource)
	}
	m.migrationSources[b.LeaseToken] = r
	m.capacityInstanceResources[i.ID] = c
	return r, nil
}

func (m *MemStore) originalMigrationSourceLocked(i Instance) bool {
	for _, r := range m.migrationSources {
		if r.Phase == MigrationSourceBound && r.InstanceID == i.ID && r.SourceNodeID == i.NodeID && r.SourceWakeID == i.WakeID {
			return true
		}
	}
	return false
}

func (m *MemStore) MigrationSourceByLease(ctx context.Context, lease string) (MigrationSource, error) {
	if err := ctx.Err(); err != nil {
		return MigrationSource{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.migrationSources[lease]
	if !ok {
		return r, ErrNotFound
	}
	return r, nil
}

func (m *MemStore) RequestMigrationSourceRetirement(ctx context.Context, lease, node string) (MigrationSource, error) {
	if err := ctx.Err(); err != nil {
		return MigrationSource{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.migrationSources[lease]
	if !ok {
		return r, ErrNotFound
	}
	if r.SourceNodeID != node || r.Phase == MigrationSourceResumed {
		return MigrationSource{}, ErrConflict
	}
	if r.Phase == MigrationSourceBound {
		i := m.instances[r.InstanceID]
		if m.migrationPendingLocked(r.InstanceID, lease, false) || i.NodeID == node && i.WakeID == r.SourceWakeID && IsLive(i.State) {
			return r, ErrConflict
		}
		r.Phase = MigrationSourceRetiring
		m.migrationSources[lease] = r
	}
	return r, nil
}

func (m *MemStore) AcknowledgeMigrationSource(ctx context.Context, b MigrationSourceBinding, outcome, proof string) error {
	if err := b.Validate(); err != nil {
		return err
	}
	if proof == "" || len(proof) > api.MigrationCleanupProofMaxBytes || outcome != MigrationSourceRetired && outcome != MigrationSourceResumed {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.migrationSources[b.LeaseToken]
	if !ok {
		return ErrNotFound
	}
	if r.MigrationSourceBinding != b {
		return ErrConflict
	}
	if !r.Pending() {
		if r.Phase != outcome || r.Proof != proof {
			return ErrConflict
		}
		return nil
	}
	if outcome == MigrationSourceResumed && (r.Phase != MigrationSourceBound || !m.migrationResumeFences[b.LeaseToken] || m.migrationPendingLocked(b.InstanceID, b.LeaseToken, true)) || outcome == MigrationSourceRetired && r.Phase != MigrationSourceRetiring && !m.migrationResumeFences[b.LeaseToken] {
		return ErrConflict
	}
	i := m.instances[b.InstanceID]
	if outcome == MigrationSourceResumed && (i.NodeID != b.SourceNodeID || i.WakeID != b.SourceWakeID || i.State != string(StateRunning) && i.State != string(StateDraining)) {
		return ErrConflict
	}
	r.Phase, r.Proof = outcome, proof
	m.migrationSources[b.LeaseToken] = r
	return nil
}

func (m *MemStore) ListMigrationSourcesForRecovery(ctx context.Context, node string, now time.Time, after string, limit int) ([]MigrationSource, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit < 1 || limit > api.MigrationAdmissionRecoveryBatchMax {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []MigrationSource{}
	for token, r := range m.migrationSources {
		l := m.migrationLeases[token]
		if r.SourceNodeID == node && token > after && r.Pending() && (r.Phase == MigrationSourceRetiring || !l.LeaseExpiresAt.After(now)) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LeaseToken < out[j].LeaseToken })
	return out[:min(len(out), limit)], nil
}

func (m *MemStore) migrationSourcePendingLocked(lease string) bool {
	return m.migrationSources[lease].Pending()
}

func (m *MemStore) ListMigrationSourceLeasesForRecovery(ctx context.Context, node string, now time.Time, after string, limit int) ([]MigrationLease, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateSnapshotRecoveryIDs(node); err != nil || limit < 1 || limit > api.MigrationAdmissionRecoveryBatchMax {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []MigrationLease{}
	for token, l := range m.migrationLeases {
		r := m.migrationSources[token]
		if l.SourceNodeID == node && token > after && (!l.LeaseExpiresAt.After(now) || m.migrationResumeFences[token] || r.Phase == MigrationSourceRetiring || r.Phase == MigrationSourceRetired || r.Phase == MigrationSourceResumed) {
			out = append(out, l)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LeaseToken < out[j].LeaseToken })
	return out[:min(len(out), limit)], nil
}

// A bound source is already represented by its original live instance. A
// retiring source is a separate physical owner even if that name returns here.
func (m *MemStore) migrationSourceCostLocked(r MigrationSource, legacy bool) capacityResources {
	if !r.Pending() {
		return capacityResources{}
	}
	i := m.instances[r.InstanceID]
	counted := IsLive(i.State)
	if legacy {
		counted = nodeUsageCounts(State(i.State))
	}
	if r.Phase == MigrationSourceBound && i.NodeID == r.SourceNodeID && i.WakeID == r.SourceWakeID && counted {
		return capacityResources{}
	}
	return capacityResources{RAM: int64(r.RAMMB), CPU: int64(r.CPUMillicores), VCPU: int64(r.VCPU)}
}
