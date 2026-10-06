package fcvm

import "sort"

// InstanceInventory reports actual Firecracker processes, rather than cgroup
// metrics or the Manager's retained cleanup entries. Unknown VMMs cannot assert
// absence. Holding the Manager lock protects the wake-to-live handoff.
func (m *Manager) InstanceInventory() ([]string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	reader, ok := m.vmm.(interface{ ProcessInventory() []string })
	if !ok {
		return nil, false
	}
	return reader.ProcessInventory(), true
}

// ProcessInventory includes booting and paused processes. Only the process
// waiter removes an exited child; Destroy still owns network/lease cleanup.
func (v *JailerVMM) ProcessInventory() []string {
	v.mu.Lock()
	defer v.mu.Unlock()
	ids := make([]string, 0, len(v.proc))
	for id := range v.proc {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
