package vmmdmount

// adr: 435. A lease protects materialization, not runtime admission.

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var (
	ErrMountCapacity = errors.New("vmmdmount: mount capacity exhausted")
	ErrMountBusy     = errors.New("vmmdmount: mount has an active owner")
)

// MountLease is an owner-held capacity reservation. The holder must either
// release it after its operation or hand an attached mount to a legacy caller.
// All fields are protected by registry.mu; callers cannot construct a lease.
type MountLease struct {
	registry   *Registry
	mountpoint string
	closed     bool
	cleanupErr error
}

// ReserveMount refuses before the owner fetches bytes or creates a mount.
func (r *Registry) ReserveMount(ctx context.Context) (*MountLease, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(r.entries)+len(r.reservations) >= r.cap {
		return nil, ErrMountCapacity
	}
	lease := &MountLease{registry: r}
	r.reservations[lease] = true
	return lease, nil
}

// Attach binds this reservation to exactly one newly created mount. Sweeps,
// external unmount requests and eviction cannot release the attached mount.
func (l *MountLease) Attach(mountpoint string, kind MountKind, key, source string) error {
	if l == nil || l.registry == nil || mountpoint == "" {
		return fmt.Errorf("vmmdmount: invalid mount lease")
	}
	r := l.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if l.closed || !r.reservations[l] || l.mountpoint != "" {
		return ErrMountBusy
	}
	if _, exists := r.entries[mountpoint]; exists {
		return ErrMountBusy
	}
	if kind != MountKindParentExt4 && kind != MountKindOverlayParent && kind != MountKindRuntimeScanOverlay {
		return fmt.Errorf("vmmdmount: unsupported leased mount kind")
	}
	r.entries[mountpoint] = MountEntry{Kind: kind, StorageKey: key, SrcPath: source, MountedAt: time.Now()}
	r.owners[mountpoint] = l
	delete(r.reservations, l)
	l.mountpoint = mountpoint
	return nil
}

// HandOff ends protection for the legacy mountpoint API. Its caller and the
// orphan sweep now own cleanup. Verified materialization never uses this path.
func (l *MountLease) HandOff() error {
	if l == nil || l.registry == nil {
		return fmt.Errorf("vmmdmount: invalid mount lease")
	}
	r := l.registry
	r.mu.Lock()
	defer r.mu.Unlock()
	if l.closed || l.mountpoint == "" || r.owners[l.mountpoint] != l || r.releasing[l.mountpoint] {
		return ErrMountBusy
	}
	delete(r.owners, l.mountpoint)
	l.closed = true
	return nil
}

// Release closes the operation. Cleanup failure remains a failed receipt and
// leaves the exact entry available to a subsequent orphan sweep.
func (l *MountLease) Release(ctx context.Context) error {
	if l == nil || l.registry == nil {
		return fmt.Errorf("vmmdmount: invalid mount lease")
	}
	r := l.registry
	r.mu.Lock()
	if l.closed {
		err := l.cleanupErr
		r.mu.Unlock()
		return err
	}
	if l.mountpoint == "" {
		delete(r.reservations, l)
		l.closed = true
		r.mu.Unlock()
		return nil
	}
	r.mu.Unlock()
	_, err := r.umount(ctx, l.mountpoint, l)
	return err
}
