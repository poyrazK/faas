package fcvm

import "fmt"

// reserveRecovered must run before new boot/prepared-network admission. It is
// atomic: any corrupt lease or conflicting slot leaves all existing holdings
// intact. Even a revoked/exited launch keeps its slot until resource cleanup.
func (a *Allocator) reserveRecovered(leases []Lease) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	owners := make(map[int]string, len(a.byInstance)+len(a.reserved)+len(a.recovered)+len(leases))
	for id, slot := range a.byInstance {
		owners[slot] = id
	}
	for id, slot := range a.reserved {
		owners[slot] = id
	}
	for slot, id := range a.recovered {
		owners[slot] = id
	}
	needed := make(map[int]string)
	for _, lease := range leases {
		if err := validateNativeJournalLease(lease); err != nil {
			return err
		}
		if slot, ok := a.byInstance[lease.Instance]; ok {
			if slot != lease.Slot {
				return fmt.Errorf("fcvm: native recovery: instance %s already holds another slot", lease.Instance)
			}
			continue
		}
		if _, ok := a.reserved[lease.Instance]; ok {
			return fmt.Errorf("fcvm: native recovery: instance %s has a prepared reservation", lease.Instance)
		}
		if owner, ok := owners[lease.Slot]; ok {
			if owner != lease.Instance {
				return fmt.Errorf("fcvm: native recovery: slot %d belongs to another instance", lease.Slot)
			}
			continue
		}
		owners[lease.Slot], needed[lease.Slot] = lease.Instance, lease.Instance
	}
	// All requested new holdings must come from the actual free pool, not
	// simply from absence in one of the ownership maps.
	remaining := make(map[int]string, len(needed))
	for slot, id := range needed {
		remaining[slot] = id
	}
	free := make([]int, 0, len(a.free))
	for _, slot := range a.free {
		if _, found := needed[slot]; found {
			delete(remaining, slot)
		} else {
			free = append(free, slot)
		}
	}
	if len(remaining) != 0 {
		return fmt.Errorf("fcvm: native recovery: requested slot is not free")
	}
	if a.recovered == nil {
		a.recovered = make(map[int]string)
	}
	for slot, id := range needed {
		a.recovered[slot] = id
	}
	a.free = free
	return nil
}

// Call only after a revoked launch has an exact exit acknowledgement and its
// resources are removed. Ordinary Release cannot clear these quarantines.
func (a *Allocator) releaseRecovered(instance string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	found := false
	for slot, id := range a.recovered {
		if id == instance {
			delete(a.recovered, slot)
			a.free = append(a.free, slot)
			found = true
		}
	}
	if !found {
		return fmt.Errorf("fcvm: native recovery: instance %s has no recovered slots", instance)
	}
	return nil
}

// Caller holds a.mu.
func (a *Allocator) hasRecoveredInstance(instance string) bool {
	for _, id := range a.recovered {
		if id == instance {
			return true
		}
	}
	return false
}
