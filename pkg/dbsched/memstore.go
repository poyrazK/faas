package dbsched

import (
	"context"
	"slices"
	"sort"
	"sync"
	"time"
)

// MemStore is the in-memory Store for tests and single-process wiring.
type MemStore struct {
	mu    sync.Mutex
	nodes map[string]Node
	vms   map[string]VM
	// leases are restore leases by database id.
	leases map[string]time.Time
	// ledger is the usage ledger by database id.
	ledger map[string][]UsageRow
	now    func() time.Time
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{nodes: map[string]Node{}, vms: map[string]VM{}, leases: map[string]time.Time{}, ledger: map[string][]UsageRow{}, now: time.Now}
}

var _ Store = (*MemStore)(nil)

func (m *MemStore) UpsertNode(_ context.Context, node Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nodes[node.ID] = node
	return nil
}

func (m *MemStore) Nodes(context.Context) ([]Node, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		out = append(out, n)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *MemStore) Get(_ context.Context, id string) (VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return VM{}, ErrNotFound
	}
	return vm, nil
}

func (m *MemStore) List(_ context.Context, states ...State) ([]VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []VM
	for _, vm := range m.vms {
		if slices.Contains(states, vm.State) {
			out = append(out, vm)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].DatabaseID < out[j].DatabaseID })
	return out, nil
}

func (m *MemStore) usageLocked() map[string]nodeUsage {
	usage := map[string]nodeUsage{}
	for _, vm := range m.vms {
		u := usage[vm.NodeID]
		u.volumeBytes += vm.VolumeBytes
		if isActive(vm.State) {
			u.activeMB += billableMB(vm.MemMiB)
		}
		usage[vm.NodeID] = u
	}
	return usage
}

func (m *MemStore) Place(_ context.Context, vm VM) (VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.vms[vm.DatabaseID]; ok {
		return existing, nil
	}
	nodes := make([]Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		nodes = append(nodes, n)
	}
	node, ok := pickNode(nodes, m.usageLocked(), vm.VolumeBytes, vm.NodeID)
	if !ok {
		return VM{}, ErrNoCapacity
	}
	now := m.now()
	vm.NodeID, vm.State, vm.Bootstrapped, vm.LastError = node.ID, StateProvisioning, false, ""
	vm.CreatedAt, vm.StateChangedAt, vm.LastActiveAt = now, now, time.Time{}
	m.vms[vm.DatabaseID] = vm
	m.openLocked(vm.DatabaseID, MeterStorage, vm.VolumeBytes, now)
	return vm, nil
}

func (m *MemStore) AdmitWake(_ context.Context, id string) (VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return VM{}, ErrNotFound
	}
	if vm.State == StateWaking || vm.State == StateRunning {
		return vm, nil
	}
	if vm.State != StateParked {
		return VM{}, ErrConflict
	}
	node, ok := m.nodes[vm.NodeID]
	if !ok || node.Lifecycle == NodeUnavailable {
		return VM{}, ErrNodeUnavailable
	}
	if !admits(node, m.usageLocked()[node.ID], vm.MemMiB) {
		return VM{}, ErrNoCapacity
	}
	vm.State, vm.StateChangedAt = StateWaking, m.now()
	m.vms[id] = vm
	m.openLocked(id, MeterCompute, computeRate(vm), vm.StateChangedAt)
	return vm, nil
}

func (m *MemStore) Transition(_ context.Context, id string, from []State, to State, opts TransitionOptions) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return ErrNotFound
	}
	if !slices.Contains(from, vm.State) {
		return ErrConflict
	}
	now := m.now()
	vm.State, vm.StateChangedAt, vm.LastError = to, now, opts.LastError
	vm.Bootstrapped = vm.Bootstrapped || opts.Bootstrapped
	if to == StateRunning {
		vm.LastActiveAt = now
	}
	m.vms[id] = vm
	if isActive(to) {
		m.openLocked(id, MeterCompute, computeRate(vm), now)
	} else {
		m.closeLocked(id, MeterCompute, now)
	}
	return nil
}

func (m *MemStore) Touch(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok || vm.State != StateRunning {
		return ErrConflict
	}
	vm.LastActiveAt = at
	m.vms[id] = vm
	return nil
}

func (m *MemStore) RecordBackup(_ context.Context, id string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok || vm.State == StateDeleting {
		return ErrConflict
	}
	vm.LastBackupAt = at
	m.vms[id] = vm
	return nil
}

func (m *MemStore) SetAlwaysOn(_ context.Context, id string, alwaysOn bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return ErrNotFound
	}
	if vm.State == StateDeleting {
		return ErrConflict
	}
	vm.AlwaysOn = alwaysOn
	m.vms[id] = vm
	return nil
}

func (m *MemStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return nil
	}
	if vm.State != StateDeleting {
		return ErrConflict
	}
	delete(m.vms, id)
	for _, meter := range []string{MeterCompute, MeterStorage, MeterHistory} {
		m.closeLocked(id, meter, m.now())
	}
	return nil
}

func (m *MemStore) ClaimRestore(_ context.Context, id string, held time.Time, lease time.Duration) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return time.Time{}, ErrNotFound
	}
	current, leased := m.leases[id]
	now := m.now()
	if vm.State != StateProvisioning || vm.RestoreSource == "" || (leased && !current.Before(now) && !current.Equal(held)) {
		return time.Time{}, ErrConflict
	}
	next := now.Add(lease)
	m.leases[id] = next
	return next, nil
}

func (m *MemStore) RecordRestore(_ context.Context, id string, recoveredTo time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok || vm.State != StateProvisioning || vm.RestoreSource == "" {
		return ErrConflict
	}
	vm.RestoreRecoveredTo = recoveredTo
	m.vms[id] = vm
	return nil
}

func (m *MemStore) openLocked(id, meter string, rate int64, at time.Time) {
	for _, r := range m.ledger[id] {
		if r.Meter == meter && r.End.IsZero() {
			return
		}
	}
	m.ledger[id] = append(m.ledger[id], UsageRow{Meter: meter, Start: at, Rate: rate})
}

func (m *MemStore) closeLocked(id, meter string, at time.Time) {
	for i, r := range m.ledger[id] {
		if r.Meter == meter && r.End.IsZero() {
			if at.Before(r.Start) {
				at = r.Start
			}
			m.ledger[id][i].End = at
		}
	}
}

func (m *MemStore) SampleUsage(_ context.Context, id, meter string, rate int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.ledger[id] {
		if r.Meter == meter && r.End.IsZero() && r.Rate == rate {
			return nil
		}
	}
	m.closeLocked(id, meter, at)
	m.openLocked(id, meter, rate, at)
	return nil
}

func (m *MemStore) AddEgress(_ context.Context, id string, bytes int64, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	hour := usageHour(at)
	for i, r := range m.ledger[id] {
		if r.Meter == MeterEgress && r.Start.Equal(hour) {
			m.ledger[id][i].Rate += bytes
			return nil
		}
	}
	m.ledger[id] = append(m.ledger[id], UsageRow{Meter: MeterEgress, Start: hour, Rate: bytes})
	return nil
}

func (m *MemStore) Usage(_ context.Context, id string, from, to time.Time) (Usage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rows, ok := m.ledger[id]
	if !ok {
		return Usage{}, ErrNotFound
	}
	return sumUsage(rows, from, to, m.now()), nil
}

func (m *MemStore) PruneUsage(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, rows := range m.ledger {
		kept := rows[:0]
		for _, r := range rows {
			old := (r.Meter == MeterEgress && r.Start.Before(before)) || (!r.End.IsZero() && r.End.Before(before))
			if old {
				n++
				continue
			}
			kept = append(kept, r)
		}
		if len(kept) == 0 {
			delete(m.ledger, id)
		} else {
			m.ledger[id] = kept
		}
	}
	return n, nil
}

func (m *MemStore) Resize(_ context.Context, id string, shape Shape, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return ErrNotFound
	}
	if err := checkResize(vm, shape); err != nil {
		return err
	}
	if vm.shape() == shape {
		return nil
	}
	node, ok := m.nodes[vm.NodeID]
	if !ok {
		return ErrNodeUnavailable
	}
	if err := resizeFits(node, m.usageLocked()[node.ID], vm, shape); err != nil {
		return err
	}
	grew := shape.VolumeBytes != vm.VolumeBytes
	vm.VcpuCount, vm.MemMiB, vm.VolumeBytes, vm.ResizePending = shape.VcpuCount, shape.MemMiB, shape.VolumeBytes, true
	m.vms[id] = vm
	if grew {
		m.closeLocked(id, MeterStorage, at)
		m.openLocked(id, MeterStorage, shape.VolumeBytes, at)
	}
	return nil
}

func (m *MemStore) FinishResize(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return ErrNotFound
	}
	vm.ResizePending = false
	m.vms[id] = vm
	return nil
}
