package fcvm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

type nativeProcessRecoveryRuntime struct {
	journal      *nativeLaunchJournal
	retirer      nativeProcessRetirer
	support      func() error
	resources    func(Lease, netns.Config) error
	startTime    func(int) (uint64, error)
	mounts       func(string) ([]string, error)
	unmount      func(context.Context, string) error
	inventory    func([]Lease) error
	helperGroups nativeHostHelperGroups
	loopMounts   nativeLoopMountBackend
	// Startup/test wiring only; ordinary release selection uses the staged
	// helper belonging to this vmmd executable.
	helper     string
	mu         sync.Mutex
	owned      map[string]string // launch generation registered by this daemon
	daemonLock *os.File          // held for this daemon's lifetime, never by a boot RPC
	lockWait   time.Duration
}

// WithNativeProcessRecovery enables the journal-backed lifecycle. Configure it
// before constructing Manager, and recover ownership before serving requests.
// It remains opt-in until the dedicated native VM/leak acceptance gates pass.
func (v *JailerVMM) WithNativeProcessRecovery() *JailerVMM {
	loops := newNativeLoopMountBackend()
	v.nativeRecovery = &nativeProcessRecoveryRuntime{
		journal:      &nativeLaunchJournal{root: filepath.Join(v.chrootBase, ".native-processes"), loopMounts: loops},
		retirer:      nativeProcessRetirer{probe: nativeProcessProbe{root: "/proc", chrootBase: v.chrootBase}},
		owned:        make(map[string]string),
		lockWait:     v.readyTimeout,
		inventory:    nativeNetworkInventory,
		helperGroups: newNativeHostHelperGroups(),
		loopMounts:   loops,
		support: func() error {
			handle, err := openNativeProcess(os.Getpid())
			if err != nil {
				return err
			}
			return handle.Close()
		},
	}
	v.nativeRecovery.resources = v.nativeResourcesRemoved
	return v
}

func (v *JailerVMM) nativeRecoveryRuntime() *nativeProcessRecoveryRuntime { return v.nativeRecovery }

func (v *JailerVMM) checkNativeRecoveryMode() error {
	if v.nativeRecovery != nil {
		return nil
	}
	path := filepath.Join(v.chrootBase, ".native-processes")
	if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	// Switching back to the legacy allocator/reapers must not bypass journal
	// ownership, even when a daemon previously failed partway through startup.
	return errors.New("native recovery: existing ownership journal requires native_process_recovery")
}

func (r *nativeProcessRecoveryRuntime) generation(instance string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.owned[instance]
}

func (r *nativeProcessRecoveryRuntime) remember(record nativeLaunchRecord) {
	r.mu.Lock()
	r.owned[record.Lease.Instance] = record.Generation
	r.mu.Unlock()
}

// Capture both prepared/unfinished journal frames and every actual native UID.
// Completed records are still checked, but do not reserve reusable slots.
func (v *JailerVMM) nativeRecoveryLeases(ctx context.Context) ([]Lease, error) {
	r := v.nativeRecovery
	if err := r.support(); err != nil {
		return nil, fmt.Errorf("native recovery support: %w", err)
	}
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		return nil, err
	}
	records, err := r.journal.records(ctx)
	if err != nil {
		return nil, err
	}
	helperJournal := nativeHostHelperJournal{owner: r.journal, groups: r.helperGroups}
	helperRecords, err := helperJournal.allRecords(ctx, records)
	if err != nil {
		return nil, err
	}
	if r.helperGroups != nil {
		if err := r.helperGroups.Inventory(helperRecords); err != nil {
			return nil, err
		}
	} else if len(helperRecords) != 0 {
		return nil, errors.New("native recovery: host helper kernel support is unavailable")
	}
	loopJournal := nativeLoopMountJournal{owner: r.journal, backend: r.loopMounts}
	loopRecords, err := loopJournal.allRecords(ctx, records)
	if err != nil {
		return nil, err
	}
	if r.loopMounts != nil {
		for _, record := range loopRecords {
			if record.Removed {
				if err := r.loopMounts.Removed(record, loopJournal.point(record)); err != nil {
					return nil, err
				}
			}
		}
		if err := r.loopMounts.Inventory(loopRecords); err != nil {
			return nil, err
		}
	} else if len(loopRecords) != 0 {
		return nil, errors.New("native recovery: loop mount kernel support is unavailable")
	}
	byID := make(map[string]nativeLaunchRecord, len(records))
	var leases []Lease
	for _, record := range records {
		byID[record.Lease.Instance] = record
		if !record.ResourcesRemoved {
			leases = append(leases, record.Lease)
		} else if err := r.resources(record.Lease, nativeLeaseNetwork(record.Lease)); err != nil {
			return nil, fmt.Errorf("native recovery: acknowledged resources reappeared: %w", err)
		}
	}
	processes, err := r.retirer.probe.processes(ctx)
	if err != nil {
		return nil, err
	}
	for _, process := range processes {
		if record, found := byID[process.Instance]; found && record.ResourcesRemoved {
			return nil, errors.New("native recovery: retired journal still has a kernel task")
		}
		leases = append(leases, process.lease())
	}
	if err := r.inventory(leases); err != nil {
		return nil, err
	}
	// A legacy fork before exec may have no recognizable command line yet.
	// Its already-created chroot cannot be treated as an absent launch.
	entries, err := os.ReadDir(v.chrootBase)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !nativeExecutableName(entry.Name(), "firecracker") {
			continue
		}
		children, err := os.ReadDir(filepath.Join(v.chrootBase, entry.Name()))
		if err != nil {
			return nil, err
		}
		for _, child := range children {
			if _, found := byID[child.Name()]; !found {
				return nil, fmt.Errorf("native recovery: chroot %s has no launch provenance", child.Name())
			}
		}
	}
	return leases, ctx.Err()
}

// Prepare before network/artifact/native effects. A completed old instance can
// be reused only after its exit AND resources were durably acknowledged.
func (v *JailerVMM) prepareNativeLease(ctx context.Context, lease Lease) error {
	r := v.nativeRecovery
	if r == nil {
		return nil
	}
	if err := r.acquireDaemonOwnership(ctx); err != nil {
		return err
	}
	roots, err := v.nativeInstanceRoots(lease.Instance)
	if err != nil {
		return err
	}
	for _, root := range roots {
		if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
			if err != nil {
				return err
			}
			return errors.New("native recovery: existing chroot must be retired before a new lease")
		}
	}
	record, err := r.journal.read(lease.Instance)
	if errors.Is(err, os.ErrNotExist) {
		if err := r.journal.prepare(ctx, lease); err != nil {
			return err
		}
		record, err = r.journal.read(lease.Instance)
	} else if err == nil {
		record, err = r.journal.replace(ctx, lease, record.Generation, true)
	}
	if err != nil {
		return err
	}
	r.remember(record)
	return nil
}

// Stable per-instance locks fence launch/stop. The process-lifetime root lock
// also prevents a second allocator from creating records during recovery or
// allocating the same UID from its own empty map. It is close-on-exec; only
// daemon process death releases it in production.
func (r *nativeProcessRecoveryRuntime) acquireDaemonOwnership(ctx context.Context) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.daemonLock != nil {
		return nil
	}
	budget := r.lockWait
	if budget <= 0 {
		budget = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	if err := os.MkdirAll(r.journal.root, 0o700); err != nil {
		return err
	}
	if err := checkNativeJournalPath(r.journal.root, true); err != nil {
		return err
	}
	lock, err := lockNativeJournalFile(ctx, filepath.Join(r.journal.root, ".daemon-owner.lock"))
	if err != nil {
		return fmt.Errorf("native recovery: daemon ownership: %w", err)
	}
	r.daemonLock = lock
	return nil
}

// A restore fallback retains its original lease and network. Only its local
// registered producer can replace the prior confirmed native incarnation.
func (v *JailerVMM) ensureNativeLaunch(ctx context.Context, lease Lease) error {
	r := v.nativeRecovery
	if r == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	record, err := r.journal.read(lease.Instance)
	if err != nil {
		return err
	}
	if r.generation(lease.Instance) != record.Generation || !sameNativeJournalLease(record.Lease, lease) {
		return errors.New("native recovery: lease has no local launch producer")
	}
	if record.Revoked {
		record, err = r.journal.replace(ctx, lease, record.Generation, false)
		if err != nil {
			return err
		}
		r.remember(record)
	} else if record.Authorized {
		return errors.New("native recovery: lease already has an authorized process")
	}
	return nil
}

func (v *JailerVMM) nativeRetirementRecord(ctx context.Context, lease Lease) (nativeLaunchRecord, error) {
	r := v.nativeRecovery
	record, err := r.journal.read(lease.Instance)
	if err != nil {
		return record, err
	}
	if lease.UID != 0 && !sameNativeJournalLease(record.Lease, lease) {
		return record, errors.New("native recovery: retirement lease differs from launch ownership")
	}
	return r.journal.retire(ctx, lease.Instance, r.retirer)
}

func (v *JailerVMM) confirmNativeCleanup(ctx context.Context, lease Lease, nc netns.Config) error {
	r := v.nativeRecovery
	if r == nil {
		return nil
	}
	record, err := r.journal.read(lease.Instance)
	if err != nil {
		return err
	}
	if !sameNativeJournalLease(record.Lease, lease) || !record.ExitConfirmed || !record.Revoked {
		return errors.New("native recovery: resource cleanup has no matching retirement")
	}
	if err := r.resources(lease, nc); err != nil {
		return err
	}
	if err := r.journal.confirmResourcesRemoved(ctx, record); err != nil {
		return err
	}
	r.mu.Lock()
	delete(r.owned, lease.Instance)
	r.mu.Unlock()
	return nil
}

func (v *JailerVMM) nativeResourcesRemoved(lease Lease, nc netns.Config) error {
	owner, err := v.nativeRecovery.journal.read(lease.Instance)
	if err != nil {
		return err
	}
	helpers := nativeHostHelperJournal{owner: v.nativeRecovery.journal, groups: v.nativeRecovery.helperGroups}
	if err := helpers.requireRemoved(owner); err != nil {
		return err
	}
	loops := nativeLoopMountJournal{owner: v.nativeRecovery.journal, backend: v.nativeRecovery.loopMounts}
	if err := loops.requireRemoved(owner); err != nil {
		return err
	}
	roots, err := v.nativeInstanceRoots(lease.Instance)
	if err != nil {
		return err
	}
	for _, path := range append(roots, nativeCgroupScope(lease)) {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			if err != nil {
				return err
			}
			return fmt.Errorf("native recovery: resource still exists: %s", path)
		}
	}
	if !lease.Networkless {
		return nativeNetworkRemoved(nc)
	}
	return nil
}

// Firecracker upgrades change the jailer's directory basename. Recovery must
// examine older managed versions as well as the currently resolved binary.
func (v *JailerVMM) nativeInstanceRoots(instance string) ([]string, error) {
	if !validNativeInstanceName(instance) {
		return nil, errors.New("native recovery: invalid chroot instance")
	}
	entries, err := os.ReadDir(v.chrootBase)
	if err != nil {
		return nil, err
	}
	roots := []string{filepath.Dir(v.chrootRoot(instance))}
	for _, entry := range entries {
		if !nativeExecutableName(entry.Name(), "firecracker") {
			continue
		}
		if !entry.IsDir() {
			return nil, errors.New("native recovery: managed chroot parent is not a directory")
		}
		if entry.Name() == v.fcName {
			continue
		}
		roots = append(roots, filepath.Join(v.chrootBase, entry.Name(), instance))
	}
	return roots, nil
}

func nativeCgroupScope(lease Lease) string {
	parent := ParentCgroupFor(lease.Plan)
	if lease.IsBuilder {
		parent = BuilderCgroupParent
	}
	return filepath.Join(cgroupRoot, parent, PerInstanceScope(lease.Instance))
}

func nativeLeaseNetwork(lease Lease) netns.Config {
	if lease.Networkless {
		return netns.Config{Instance: lease.Instance}
	}
	nc := netns.NewConfig(lease.Instance, lease.Netns, lease.VethHost, lease.VethPeer, lease.HostIP)
	// Recovery cannot infer whether the private fabric was attached. Check
	// both deterministic host interfaces before releasing the slot.
	nc.PrivateVethHost, nc.PrivateVethPeer = privateVethNames(lease.Slot)
	return nc
}
