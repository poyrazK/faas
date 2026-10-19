package state

import (
	"bytes"
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

var _ MigrationAdmissionStore = (*MemStore)(nil)

func (m *MemStore) migrationResidentLocked(i Instance) bool {
	for _, r := range m.migrationSources {
		if r.Phase == MigrationSourceResumed && r.InstanceID == i.ID && r.SourceNodeID == i.NodeID && r.SourceWakeID == i.WakeID {
			return true
		}
	}

	for _, a := range m.migrationAdmissions {
		if a.Phase == MigrationCommitted && a.InstanceID == i.ID && a.DestinationNodeID == i.NodeID && a.DestinationWakeID == i.WakeID {
			return true
		}
	}
	return false
}

func (m *MemStore) migrationResidentRAMLocked(i Instance) int64 {
	ram := int64(i.RAMMB + api.PerVMOverheadMB)
	if m.migrationResidentLocked(i) || m.originalMigrationSourceLocked(i) {
		ram = max(ram, m.capacityInstanceResources[i.ID].RAM)
	}
	return ram
}

func (m *MemStore) migrationResourceFloorLocked(i Instance, floor capacityResources) capacityResources {
	for _, r := range m.migrationSources {
		if r.Pending() && r.SourceNodeID == i.NodeID && r.SourceWakeID == i.WakeID && r.InstanceID == i.ID {
			floor.RAM = max(floor.RAM, int64(r.RAMMB))
			floor.CPU = max(floor.CPU, int64(r.CPUMillicores))
			floor.VCPU = max(floor.VCPU, int64(r.VCPU))
		}
	}
	for _, incoming := range m.migrationAdmissions {
		if incoming.Pending() && incoming.InstanceID == i.ID && incoming.DestinationNodeID == i.NodeID && incoming.DestinationWakeID == i.WakeID {
			floor.RAM, floor.CPU, floor.VCPU = max(floor.RAM, int64(incoming.RAMMB)), max(floor.CPU, int64(incoming.CPUMillicores)), max(floor.VCPU, int64(incoming.VCPU))
		}
	}
	return floor
}

func (m *MemStore) guardMigrationAdmissionSourceLocked(old, next Instance) error {
	if next.State == string(StateMigrating) && (old.State != next.State || old.LeaseToken != next.LeaseToken) {
		if r, exists := m.migrationSources[next.LeaseToken]; exists {
			l := m.migrationLeases[next.LeaseToken]
			if r.Phase != MigrationSourceBound || r.InstanceID != next.ID || r.SourceNodeID != next.NodeID || r.SourceWakeID != next.WakeID || l.InstanceID != next.ID || m.migrationResumeFences[next.LeaseToken] || !time.Now().Before(l.LeaseExpiresAt) {
				return ErrConflict
			}
		}
	}
	for _, r := range m.migrationSources {
		if r.Phase != MigrationSourceBound || r.InstanceID != old.ID || old.NodeID != r.SourceNodeID || old.WakeID != r.SourceWakeID {
			continue
		}
		if old.NodeID != next.NodeID || old.WakeID != next.WakeID {
			allowed := false
			for _, a := range m.migrationAdmissions {
				if a.LeaseToken == r.LeaseToken && a.Phase == MigrationAdopted && a.DestinationNodeID == next.NodeID && a.DestinationWakeID == next.WakeID && next.State == string(StateRunning) && next.LeaseToken == r.LeaseToken {
					allowed = true
				}
			}
			if !allowed {
				return ErrConflict
			}
		}
	}

	for _, a := range m.migrationAdmissions {
		if a.InstanceID != old.ID || !a.Pending() {
			continue
		}
		if old.NodeID == next.NodeID && old.WakeID == next.WakeID && old.State == next.State && old.LeaseToken == next.LeaseToken && old.AppID == next.AppID && old.DeploymentID == next.DeploymentID {
			return nil
		}
		if a.Phase == MigrationAdopted && old.NodeID == a.SourceNodeID && old.WakeID == a.SourceWakeID && next.NodeID == a.DestinationNodeID && next.WakeID == a.DestinationWakeID && next.State == string(StateRunning) && next.LeaseToken == a.LeaseToken && old.AppID == next.AppID && old.DeploymentID == next.DeploymentID {
			return nil
		}
		return ErrConflict
	}
	return nil
}

func (m *MemStore) migrationPendingLocked(instance, lease string, includeCommitted bool) bool {
	for _, a := range m.migrationAdmissions {
		if a.InstanceID == instance && (lease == "" || a.LeaseToken == lease) && (a.Pending() || includeCommitted && a.Phase == MigrationCommitted) {
			return true
		}
	}
	return false
}

func (m *MemStore) nodeMigrationPendingLocked(node string) bool {
	for _, l := range m.migrationLeases {
		if l.SourceNodeID == node {
			return true
		}
	}
	return m.nodeMigrationCapacityPendingLocked(node)
}

func (m *MemStore) nodeMigrationCapacityPendingLocked(node string) bool {
	for _, r := range m.migrationSources {
		if r.SourceNodeID == node && r.Pending() {
			return true
		}
	}
	for _, a := range m.migrationAdmissions {
		if a.DestinationNodeID == node && a.Pending() {
			return true
		}
	}
	return false
}

func (m *MemStore) migrationInstanceCostLocked(i Instance) capacityResources {
	a, d := m.apps[i.AppID], m.deployments[i.DeploymentID]
	ram, cpu := capacitySidecarResources(d.Sidecars)
	limits, _ := api.LimitsFor(m.accounts[a.AccountID].Plan)
	vcpus := int64(limits.VCPU)
	if vcpus <= 0 {
		vcpus = 4
	}
	if i.AppID == "" {
		vcpus = 1
	}
	captured := m.capacityInstanceResources[i.ID]
	cost := capacityResources{RAM: max(captured.RAM, int64(i.RAMMB+api.PerVMOverheadMB)+ram), CPU: max(captured.CPU, int64(max(a.CPUMillicores, api.DefaultAppCPUMillicores))+cpu), VCPU: max(captured.VCPU, vcpus)}
	return m.migrationResourceFloorLocked(i, cost)
}

func (m *MemStore) migrationNodeUsedLocked(node, omit string) capacityResources {
	var used capacityResources
	for _, i := range m.instances {
		if i.ID != omit && i.NodeID == node && IsLive(i.State) {
			c := m.migrationInstanceCostLocked(i)
			used.RAM += c.RAM
			used.CPU += c.CPU
			used.VCPU += c.VCPU
		}
	}
	for _, a := range m.migrationAdmissions {
		if !a.Pending() || a.DestinationNodeID != node || a.InstanceID == omit {
			continue
		}
		i := m.instances[a.InstanceID]
		if i.NodeID == node && i.WakeID == a.DestinationWakeID && i.State == string(StateRunning) {
			continue
		}
		used.RAM += int64(a.RAMMB)
		used.CPU += int64(a.CPUMillicores)
		used.VCPU += int64(a.VCPU)
	}
	for _, r := range m.migrationSources {
		if r.SourceNodeID == node {
			c := m.migrationSourceCostLocked(r, false)
			used.RAM += c.RAM
			used.CPU += c.CPU
			used.VCPU += c.VCPU
		}
	}
	return used
}

func migrationCapacityFits(n ComputeNode, used capacityResources) bool {
	return (n.AdmissionCeilingMB <= 0 || used.RAM <= int64(n.AdmissionCeilingMB)) && (n.VPCPUs <= 0 || used.CPU <= int64(n.VPCPUs)*1000) && (n.VCPUBudget <= 0 || used.VCPU <= int64(n.VCPUBudget))
}

func (m *MemStore) checkMigrationAdmissionCapacityLocked(i Instance) error {
	if !IsLive(i.State) || !m.nodeMigrationCapacityPendingLocked(i.NodeID) {
		return nil
	}
	u, c := m.migrationNodeUsedLocked(i.NodeID, i.ID), m.migrationInstanceCostLocked(i)
	u.RAM += c.RAM
	u.CPU += c.CPU
	u.VCPU += c.VCPU
	if !migrationCapacityFits(m.computeNodes[i.NodeID], u) {
		return ErrNodeCapacity
	}
	return nil
}

func (m *MemStore) ReserveMigrationAdmission(ctx context.Context, p MigrationAdmissionPlan) (MigrationAdmission, error) {
	p = normalizeMigrationAdmissionPlan(p)
	if err := validateMigrationAdmissionPlan(p); err != nil {
		return MigrationAdmission{}, err
	}
	if err := ctx.Err(); err != nil {
		return MigrationAdmission{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if a, ok := m.migrationAdmissions[p.ID]; ok {
		if a.MigrationAdmissionPlan != p {
			return MigrationAdmission{}, ErrConflict
		}
		return cloneMigrationAdmission(a), nil
	}
	ctx = WithNodeAdmission(ctx, NodeAdmission{NodeID: p.DestinationNodeID, Generation: p.DestinationGeneration})
	if err := m.checkNodeAdmissionLocked(ctx, p.DestinationNodeID); err != nil {
		return MigrationAdmission{}, err
	}
	i := m.instances[p.InstanceID]
	source, bound := m.migrationSources[p.LeaseToken]
	if !bound || source.Phase != MigrationSourceBound || source.InstanceID != p.InstanceID || source.SourceNodeID != p.SourceNodeID || source.SourceWakeID != p.SourceWakeID {
		return MigrationAdmission{}, ErrConflict
	}
	lease, exists := m.migrationLeases[p.LeaseToken]
	if !exists || lease.InstanceID != p.InstanceID || lease.SourceNodeID != p.SourceNodeID || lease.Pending || m.migrationResumeFences[lease.LeaseToken] || !time.Now().Before(lease.LeaseExpiresAt) || lease.MemStorageKey != p.MemStorageKey || lease.VMStateStorageKey != p.VMStateStorageKey || p.ExpiresAt.After(lease.LeaseExpiresAt) || !time.Now().Before(p.ExpiresAt) || i.NodeID != p.SourceNodeID || i.WakeID != p.SourceWakeID || i.State != string(StateMigrating) || i.LeaseToken != p.LeaseToken || m.migrationPendingLocked(p.InstanceID, "", false) {
		return MigrationAdmission{}, ErrConflict
	}
	c := m.migrationInstanceCostLocked(i)
	if int64(p.RAMMB) < c.RAM || int64(p.CPUMillicores) < c.CPU || int64(p.VCPU) < c.VCPU {
		return MigrationAdmission{}, ErrConflict
	}
	u := m.migrationNodeUsedLocked(p.DestinationNodeID, "")
	u.RAM += int64(p.RAMMB)
	u.CPU += int64(p.CPUMillicores)
	u.VCPU += int64(p.VCPU)
	if !migrationCapacityFits(m.computeNodes[p.DestinationNodeID], u) {
		return MigrationAdmission{}, ErrNodeCapacity
	}
	prior := m.serviceCapacitySnapshotLocked()
	if m.migrationAdmissions == nil {
		m.migrationAdmissions = make(map[string]MigrationAdmission)
	}
	a := MigrationAdmission{MigrationAdmissionPlan: p, Phase: MigrationReserved}
	m.migrationAdmissions[p.ID] = a
	if err := m.checkServiceCapacityChangeLocked(prior, false); err != nil {
		delete(m.migrationAdmissions, p.ID)
		return MigrationAdmission{}, err
	}
	return cloneMigrationAdmission(a), nil
}

func (m *MemStore) MigrationAdmissionByID(ctx context.Context, id string) (MigrationAdmission, error) {
	if err := ctx.Err(); err != nil {
		return MigrationAdmission{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.migrationAdmissions[id]
	if !ok {
		return MigrationAdmission{}, ErrNotFound
	}
	return cloneMigrationAdmission(a), nil
}

func (m *MemStore) StartMigrationAdoption(ctx context.Context, id, node, digest string) (MigrationAdmission, bool, error) {
	if err := ctx.Err(); err != nil {
		return MigrationAdmission{}, false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.migrationAdmissions[id]
	if !ok {
		return a, false, ErrNotFound
	}
	if a.DestinationNodeID != node || a.RequestDigest != digest {
		return MigrationAdmission{}, false, ErrConflict
	}
	switch a.Phase {
	case MigrationAdopted, MigrationCommitted:
		return cloneMigrationAdmission(a), false, nil
	case MigrationAdopting:
		return cloneMigrationAdmission(a), false, ErrMigrationAdoptionUnresolved
	case MigrationReserved:
	default:
		return cloneMigrationAdmission(a), false, ErrConflict
	}
	if !time.Now().Before(a.ExpiresAt) {
		return a, false, ErrConflict
	}
	if err := m.checkNodeAdmissionLocked(WithNodeAdmission(ctx, NodeAdmission{NodeID: node, Generation: a.DestinationGeneration}), node); err != nil {
		return a, false, err
	}
	a.Phase = MigrationAdopting
	m.migrationAdmissions[id] = a
	return cloneMigrationAdmission(a), true, nil
}

func (m *MemStore) AcknowledgeMigrationAdoption(ctx context.Context, id, node, digest string, receipt []byte) error {
	if len(receipt) == 0 || len(receipt) > api.MigrationAdoptionReceiptMaxBytes {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.migrationAdmissions[id]
	if !ok {
		return ErrNotFound
	}
	if a.DestinationNodeID != node || a.RequestDigest != digest {
		return ErrConflict
	}
	if a.Phase == MigrationAdopted || a.Phase == MigrationCommitted {
		if bytes.Equal(a.AdoptionReceipt, receipt) {
			return nil
		}
		return ErrConflict
	}
	if a.Phase != MigrationAdopting {
		return ErrConflict
	}
	a.Phase = MigrationAdopted
	a.AdoptionReceipt = bytes.Clone(receipt)
	m.migrationAdmissions[id] = a
	return nil
}

func (m *MemStore) RequestMigrationAdmissionCleanup(ctx context.Context, id, node string) (MigrationAdmission, error) {
	if err := ctx.Err(); err != nil {
		return MigrationAdmission{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.migrationAdmissions[id]
	if !ok {
		return a, ErrNotFound
	}
	if a.DestinationNodeID != node {
		return MigrationAdmission{}, ErrConflict
	}
	switch a.Phase {
	case MigrationReserved:
		a.Phase, a.CleanupProof = MigrationCleaned, "no_dispatch"
	case MigrationAdopting, MigrationAdopted:
		a.Phase = MigrationCleaning
	}
	m.migrationAdmissions[id] = a
	return cloneMigrationAdmission(a), nil
}

func (m *MemStore) AcknowledgeMigrationAdmissionCleanup(ctx context.Context, id, node, proof string) error {
	if proof == "" || len(proof) > api.MigrationCleanupProofMaxBytes {
		return ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	a, ok := m.migrationAdmissions[id]
	if !ok {
		return ErrNotFound
	}
	if a.DestinationNodeID != node {
		return ErrConflict
	}
	if a.Phase == MigrationCleaned {
		if a.CleanupProof == proof {
			return nil
		}
		return ErrConflict
	}
	if a.Phase != MigrationCleaning {
		return ErrConflict
	}
	a.Phase, a.CleanupProof = MigrationCleaned, proof
	m.migrationAdmissions[id] = a
	return nil
}

func (m *MemStore) ListMigrationAdmissionsForRecovery(ctx context.Context, node string, now time.Time, after string, limit int) ([]MigrationAdmission, error) {
	if limit < 1 || limit > api.MigrationAdmissionRecoveryBatchMax {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []MigrationAdmission{}
	n, exists := m.computeNodes[node]
	for _, a := range m.migrationAdmissions {
		if a.DestinationNodeID == node && a.ID > after && a.Pending() && (!a.ExpiresAt.After(now) || a.Phase == MigrationCleaning || !exists || !n.Lifecycle.IsAdmitting() || n.LifecycleGeneration != a.DestinationGeneration) {
			out = append(out, cloneMigrationAdmission(a))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// The lease update serializes source resumption with a new inbound reservation.
func (m *MemStore) FenceMigrationSourceResume(ctx context.Context, id, lease string) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.migrationPendingLocked(id, lease, true) {
		return false, nil
	}
	if r, ok := m.migrationSources[lease]; ok && r.Phase != MigrationSourceBound {
		return false, nil
	}
	if l, ok := m.migrationLeases[lease]; ok {
		if l.InstanceID != id {
			return false, ErrConflict
		}
		if m.migrationResumeFences == nil {
			m.migrationResumeFences = make(map[string]bool)
		}
		m.migrationResumeFences[lease] = true
		m.migrationLeases[lease] = l
	}
	return true, nil
}

func (m *MemStore) validateMigrationAdmissionCommitLocked(ctx context.Context, i Instance, from, to, lease string) error {
	id := migrationAdmissionID(ctx)
	for _, a := range m.migrationAdmissions {
		if a.InstanceID == i.ID && a.SourceNodeID == from && a.DestinationNodeID == to && a.LeaseToken == lease {
			if a.Phase != MigrationAdopted || i.WakeID != a.DestinationWakeID || i.State != string(StateRunning) || id != "" && id != a.ID {
				return ErrConflict
			}
		}
	}
	if id != "" {
		a, ok := m.migrationAdmissions[id]
		if !ok || a.InstanceID != i.ID || a.SourceNodeID != from || a.DestinationNodeID != to || a.LeaseToken != lease || a.DestinationWakeID != i.WakeID || a.Phase != MigrationAdopted {
			return ErrConflict
		}
	}
	return nil
}

func (m *MemStore) consumeMigrationAdmissionLocked(i Instance, from, lease string) {
	for id, a := range m.migrationAdmissions {
		if a.InstanceID == i.ID && a.SourceNodeID == from && a.DestinationNodeID == i.NodeID && a.DestinationWakeID == i.WakeID && a.LeaseToken == lease && a.Phase == MigrationAdopted {
			floor := m.capacityInstanceResources[i.ID]
			floor.RAM, floor.CPU, floor.VCPU = max(floor.RAM, int64(a.RAMMB)), max(floor.CPU, int64(a.CPUMillicores)), max(floor.VCPU, int64(a.VCPU))
			m.capacityInstanceResources[i.ID] = floor
			if source, ok := m.migrationSources[lease]; ok {
				source.Phase = MigrationSourceRetiring
				m.migrationSources[lease] = source
			}
			a.Phase = MigrationCommitted
			m.migrationAdmissions[id] = a
		}
	}
}
