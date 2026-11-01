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
	// drills are the restore drills by id.
	drills map[string]Drill
	now    func() time.Time
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{nodes: map[string]Node{}, vms: map[string]VM{}, leases: map[string]time.Time{}, ledger: map[string][]UsageRow{}, drills: map[string]Drill{}, now: time.Now}
}

var _ Store = (*MemStore)(nil)

func (m *MemStore) UpsertNode(_ context.Context, node Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	node.BootSlots = bootSlots(node)
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

func (m *MemStore) Loads(context.Context) (map[string]NodeLoad, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return loads(m.usageLocked()), nil
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
		if vm.MoveNode != "" {
			m := usage[vm.MoveNode]
			m.volumeBytes += vm.VolumeBytes
			usage[vm.MoveNode] = m
		}
		if holdsMemory(vm.State) {
			u.activeMB += billableMB(vm.MemMiB)
		}
		if vm.State == StateWaking {
			u.waking++
		}
		usage[vm.NodeID] = u
		if vm.StandbyNode != "" {
			s := usage[vm.StandbyNode]
			s.volumeBytes += vm.VolumeBytes
			if vm.StandbyRunning {
				s.activeMB += billableMB(vm.MemMiB)
			}
			usage[vm.StandbyNode] = s
		}
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
	usage := m.usageLocked()
	node, ok := pickNode(nodes, usage, vm.VolumeBytes, vm.NodeID, vm.Major)
	if !ok {
		return VM{}, ErrNoCapacity
	}
	vm.StandbyNode, vm.StandbyRunning, vm.StandbySynced, vm.PromotePending = "", false, false, false
	if vm.Replicated {
		standby, ok := pickStandby(nodes, usage, vm, node.ID)
		if !ok {
			return VM{}, ErrNoCapacity
		}
		vm.StandbyNode = standby.ID
	}
	now := m.now()
	vm.NodeID, vm.State, vm.Bootstrapped, vm.LastError = node.ID, StateProvisioning, false, ""
	vm.CreatedAt, vm.StateChangedAt, vm.LastActiveAt = now, now, time.Time{}
	m.vms[vm.DatabaseID] = vm
	m.openLocked(vm.DatabaseID, MeterStorage, vm.VolumeBytes, now)
	m.syncStandbyLedgerLocked(vm, now)
	return vm, nil
}

// syncStandbyLedgerLocked opens and closes the standby's ledger intervals
// to match vm, starting a new interval when a rate changed.
func (m *MemStore) syncStandbyLedgerLocked(vm VM, at time.Time) {
	want := standbyRates(vm)
	for _, meter := range []string{MeterStandbyCompute, MeterStandbyStorage} {
		rate, on := want[meter]
		for _, r := range m.ledger[vm.DatabaseID] {
			if r.Meter == meter && r.End.IsZero() && (!on || r.Rate != rate) {
				m.closeLocked(vm.DatabaseID, meter, at)
			}
		}
		if on {
			m.openLocked(vm.DatabaseID, meter, rate, at)
		}
	}
}

func (m *MemStore) AdmitWake(_ context.Context, id string, keepStandbySynced bool) (VM, error) {
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
	if !ok {
		return VM{}, ErrNodeUnavailable
	}
	if err := admitWakeOn(node, m.usageLocked()[node.ID], vm.MemMiB); err != nil {
		return VM{}, err
	}
	// The primary boots asynchronous: its standby no longer holds every
	// commit once it may commit without it, unless the wake resumes it.
	vm.StandbySynced = vm.StandbySynced && keepStandbySynced
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
	for _, meter := range rateMeters {
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
	usage := m.usageLocked()
	if err := resizeFits(node, usage[node.ID], vm, shape); err != nil {
		return err
	}
	if standby, ok := m.nodes[vm.StandbyNode]; ok {
		if err := standbyResizeFits(standby, usage[standby.ID], vm, shape); err != nil {
			return err
		}
	}
	var standby string
	if shape.Replicated && !vm.Replicated {
		nodes := make([]Node, 0, len(m.nodes))
		for _, n := range m.nodes {
			nodes = append(nodes, n)
		}
		sized := vm
		sized.VolumeBytes = shape.VolumeBytes
		picked, ok := pickStandby(nodes, usage, sized, vm.NodeID)
		if !ok {
			return ErrNoCapacity
		}
		standby = picked.ID
	}
	grew := shape.VolumeBytes != vm.VolumeBytes
	replicationChange(&vm, shape, standby)
	vm.VcpuCount, vm.MemMiB, vm.VolumeBytes, vm.ResizePending = shape.VcpuCount, shape.MemMiB, shape.VolumeBytes, true
	m.vms[id] = vm
	m.syncStandbyLedgerLocked(vm, at)
	if grew {
		m.closeLocked(id, MeterStorage, at)
		m.openLocked(id, MeterStorage, shape.VolumeBytes, at)
	}
	return nil
}

func (m *MemStore) ClearStandbySlotDrop(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return ErrNotFound
	}
	vm.DropStandbySlot = false
	m.vms[id] = vm
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

func (m *MemStore) BeginMove(_ context.Context, id, target string, lease time.Duration) (VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return VM{}, ErrNotFound
	}
	if vm.State != StateParked || vm.ResizePending {
		return VM{}, ErrConflict
	}
	nodes := make([]Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		nodes = append(nodes, n)
	}
	node, err := moveTarget(nodes, m.usageLocked(), vm, target)
	if err != nil {
		return VM{}, err
	}
	now := m.now()
	vm.State, vm.StateChangedAt, vm.LastError = StateMoving, now, ""
	vm.MoveNode, vm.MoveLeaseUntil = node.ID, now.Add(lease)
	m.vms[id] = vm
	return vm, nil
}

func (m *MemStore) RenewMove(_ context.Context, id string, held time.Time, lease time.Duration) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok || vm.State != StateMoving || !vm.MoveLeaseUntil.Equal(held) {
		return time.Time{}, ErrConflict
	}
	vm.MoveLeaseUntil = m.now().Add(lease)
	m.vms[id] = vm
	return vm.MoveLeaseUntil, nil
}

func (m *MemStore) FinishMove(_ context.Context, id string, held time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok || vm.State != StateMoving || !vm.MoveLeaseUntil.Equal(held) {
		return ErrConflict
	}
	vm.NodeID, vm.MoveNode, vm.MoveLeaseUntil = vm.MoveNode, "", time.Time{}
	vm.State, vm.StateChangedAt = StateParked, m.now()
	m.vms[id] = vm
	return nil
}

func (m *MemStore) AbortMove(_ context.Context, id string, held time.Time, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok || vm.State != StateMoving {
		return ErrConflict
	}
	if held.IsZero() && !vm.MoveLeaseUntil.Before(m.now()) || !held.IsZero() && !vm.MoveLeaseUntil.Equal(held) {
		return ErrConflict
	}
	vm.MoveNode, vm.MoveLeaseUntil = "", time.Time{}
	vm.State, vm.StateChangedAt, vm.LastError = StateParked, m.now(), lastError
	m.vms[id] = vm
	return nil
}

func (m *MemStore) BeginUpgrade(_ context.Context, id string, major int, operation string, lease time.Duration) (VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return VM{}, ErrNotFound
	}
	if vm.State != StateParked || !vm.Bootstrapped || vm.ResizePending || vm.PromotePending {
		return VM{}, ErrConflict
	}
	node, ok := m.nodes[vm.NodeID]
	if !ok {
		return VM{}, ErrNodeUnavailable
	}
	if err := admitUpgradeOn(node, m.usageLocked()[node.ID], vm, major); err != nil {
		return VM{}, err
	}
	now := m.now()
	vm.State, vm.StateChangedAt, vm.LastError = StateUpgrading, now, ""
	vm.Upgrade, vm.UpgradeLeaseUntil = Upgrade{Operation: operation, From: vm.Major, To: major}, now.Add(lease)
	m.vms[id] = vm
	return vm, nil
}

func (m *MemStore) ClaimUpgrade(_ context.Context, id string, held time.Time, lease time.Duration) (time.Time, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok || vm.State != StateUpgrading || !m.leaseHeldLocked(vm.UpgradeLeaseUntil, held) {
		return time.Time{}, ErrConflict
	}
	vm.UpgradeLeaseUntil = m.now().Add(lease)
	m.vms[id] = vm
	return vm.UpgradeLeaseUntil, nil
}

func (m *MemStore) FinishUpgrade(_ context.Context, id string, held time.Time, to State, until, backupAt time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok || vm.State != StateUpgrading || held.IsZero() || !vm.UpgradeLeaseUntil.Equal(held) || (to != StateParked && to != StateRunning) {
		return ErrConflict
	}
	now := m.now()
	vm.MajorHistory = append(slices.Clone(vm.MajorHistory), MajorSpan{Major: vm.Major, Until: until})
	vm.Major, vm.Upgrade.Result, vm.UpgradeLeaseUntil, vm.LastBackupAt = vm.Upgrade.To, UpgradeOK, time.Time{}, backupAt
	vm.State, vm.StateChangedAt, vm.LastError = to, now, ""
	if to == StateRunning {
		vm.LastActiveAt = now
		m.openLocked(id, MeterCompute, computeRate(vm), now)
	}
	m.vms[id] = vm
	return nil
}

func (m *MemStore) AbortUpgrade(_ context.Context, id string, held time.Time, to State, lastError string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok || vm.State != StateUpgrading || !m.leaseHeldLocked(vm.UpgradeLeaseUntil, held) || (to != StateParked && to != StateFailed) {
		return ErrConflict
	}
	vm.Upgrade.Result, vm.UpgradeLeaseUntil = upgradeResultFor(to), time.Time{}
	vm.State, vm.StateChangedAt, vm.LastError = to, m.now(), lastError
	m.vms[id] = vm
	return nil
}

// leaseHeldLocked reports whether held is the lease until, or is zero
// and until has lapsed.
func (m *MemStore) leaseHeldLocked(until, held time.Time) bool {
	if held.IsZero() {
		return until.Before(m.now())
	}
	return until.Equal(held)
}

func (m *MemStore) ClaimStandby(_ context.Context, id string) (VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return VM{}, ErrNotFound
	}
	if vm.State != StateRunning || vm.StandbyNode == "" || vm.StandbyRunning {
		return VM{}, ErrConflict
	}
	node, ok := m.nodes[vm.StandbyNode]
	if !ok {
		return VM{}, ErrNodeUnavailable
	}
	if err := claimStandbyOn(node, m.usageLocked()[node.ID], vm.MemMiB); err != nil {
		return VM{}, err
	}
	vm.StandbyRunning = true
	m.vms[id] = vm
	m.syncStandbyLedgerLocked(vm, m.now())
	return vm, nil
}

func (m *MemStore) ReleaseStandby(_ context.Context, id string, keepSynced bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return ErrNotFound
	}
	vm.StandbyRunning, vm.StandbySynced = false, vm.StandbySynced && keepSynced
	m.vms[id] = vm
	m.syncStandbyLedgerLocked(vm, m.now())
	return nil
}

func (m *MemStore) MarkStandbySynced(_ context.Context, id string, synced bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return ErrNotFound
	}
	if synced && (vm.State != StateRunning || !vm.StandbyRunning) {
		return ErrConflict
	}
	vm.StandbySynced = synced
	m.vms[id] = vm
	return nil
}

func (m *MemStore) ReplaceStandby(_ context.Context, id string, avoid ...string) (VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return VM{}, ErrNotFound
	}
	if !vm.Replicated || vm.StandbyRunning || vm.State == StateMoving || vm.State == StateDeleting {
		return VM{}, ErrConflict
	}
	nodes := make([]Node, 0, len(m.nodes))
	for _, n := range m.nodes {
		nodes = append(nodes, n)
	}
	standby, ok := pickStandby(nodes, m.usageLocked(), vm, append([]string{vm.NodeID, vm.StandbyNode}, avoid...)...)
	if !ok {
		return VM{}, ErrNoCapacity
	}
	vm.StandbyNode, vm.StandbySynced = standby.ID, false
	m.vms[id] = vm
	m.syncStandbyLedgerLocked(vm, m.now())
	return vm, nil
}

func (m *MemStore) Failover(_ context.Context, id string) (VM, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return VM{}, ErrNotFound
	}
	if !canFailover(vm) {
		return VM{}, ErrConflict
	}
	now := m.now()
	wasActive := isActive(vm.State)
	vm.NodeID, vm.StandbyNode, vm.StandbySynced, vm.PromotePending = vm.StandbyNode, "", false, !vm.StandbyRunning
	vm.State = StateParked
	if vm.StandbyRunning {
		vm.State = StateRunning
	}
	vm.StandbyRunning, vm.LastError, vm.StateChangedAt = false, errCodeFailedOver, now
	m.vms[id] = vm
	m.syncStandbyLedgerLocked(vm, now)
	switch {
	case isActive(vm.State) && !wasActive:
		m.openLocked(id, MeterCompute, computeRate(vm), now)
	case !isActive(vm.State) && wasActive:
		m.closeLocked(id, MeterCompute, now)
	}
	return vm, nil
}

func (m *MemStore) FinishPromotion(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	vm, ok := m.vms[id]
	if !ok {
		return ErrNotFound
	}
	vm.PromotePending = false
	m.vms[id] = vm
	return nil
}

func (m *MemStore) StartDrill(_ context.Context, d Drill) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.drills[d.ID]; ok {
		return false, nil
	}
	d.StartedAt, d.Result = m.now(), DrillRunning
	m.drills[d.ID] = d
	return true, nil
}

func (m *MemStore) RunningDrills(context.Context) ([]Drill, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Drill
	for _, d := range m.drills {
		if d.Result == DrillRunning {
			out = append(out, d)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.Before(out[j].StartedAt) })
	return out, nil
}

func (m *MemStore) FinishDrill(_ context.Context, id string, result DrillResult, recoveredTo time.Time, errorCode string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.drills[id]
	if !ok || d.Result != DrillRunning {
		return nil
	}
	d.Result, d.FinishedAt, d.RecoveredTo, d.ErrorCode = result, m.now(), recoveredTo, errorCode
	m.drills[id] = d
	return nil
}

func (m *MemStore) DrillsByNode(context.Context) (map[string]DrillSummary, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[string]DrillSummary{}
	for _, d := range m.drills {
		sum := out[d.NodeID]
		if d.StartedAt.After(sum.LastStarted) {
			sum.LastStarted = d.StartedAt
		}
		if d.Result == DrillOK && d.FinishedAt.After(sum.LastOK) {
			sum.LastOK = d.FinishedAt
		}
		out[d.NodeID] = sum
	}
	return out, nil
}

func (m *MemStore) PruneDrills(_ context.Context, before time.Time) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for id, d := range m.drills {
		if d.Result != DrillRunning && d.StartedAt.Before(before) {
			delete(m.drills, id)
			n++
		}
	}
	return n, nil
}
