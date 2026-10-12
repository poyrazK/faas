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
	now   func() time.Time
}

// NewMemStore returns an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{nodes: map[string]Node{}, vms: map[string]VM{}, now: time.Now}
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
	node, ok := pickNode(nodes, m.usageLocked(), vm.VolumeBytes)
	if !ok {
		return VM{}, ErrNoCapacity
	}
	now := m.now()
	vm.NodeID, vm.State, vm.Bootstrapped, vm.LastError = node.ID, StateProvisioning, false, ""
	vm.CreatedAt, vm.StateChangedAt, vm.LastActiveAt = now, now, time.Time{}
	m.vms[vm.DatabaseID] = vm
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
	return nil
}
