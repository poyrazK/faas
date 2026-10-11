package vmmdgrpc

import "sync"

// DivergedInstances records instances whose source no longer matches their
// immutable artifact because vmmd served them a developer live patch
// (ADR-740). Such an instance must never become a snapshot: snapshots are a
// cache of the artifact, never the source of truth (ADR-005). vmmd marks an
// instance when it serves a patch, before the guest writes anything, so a
// crash mid-apply cannot leave a partially patched instance unmarked.
type DivergedInstances struct {
	mu        sync.Mutex
	instances map[string]struct{}
}

// NewDivergedInstances returns an empty registry.
func NewDivergedInstances() *DivergedInstances {
	return &DivergedInstances{instances: map[string]struct{}{}}
}

// Mark records that instance may have applied a live patch.
func (d *DivergedInstances) Mark(instance string) {
	if d == nil || instance == "" {
		return
	}
	d.mu.Lock()
	d.instances[instance] = struct{}{}
	d.mu.Unlock()
}

// Has reports whether instance may have applied a live patch.
func (d *DivergedInstances) Has(instance string) bool {
	if d == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	_, ok := d.instances[instance]
	return ok
}

// Forget drops an instance once its VM is gone.
func (d *DivergedInstances) Forget(instance string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	delete(d.instances, instance)
	d.mu.Unlock()
}
