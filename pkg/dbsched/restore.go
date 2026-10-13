package dbsched

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Point-in-time restore (ADR-1027 durability). A restore is a new database
// recovered from another's archive. It can take far longer than an RPC, so
// Restore only places it; the restore itself runs in the background under
// a lease in database_vms that the running replica renews, and any replica
// resumes one whose lease lapsed.

const errCodeRestore = "restore_failed"

// restorePoll is how often a restore polls the recovering guest.
const restorePoll = 500 * time.Millisecond

// RestoreRequest describes a database created as a point-in-time restore of
// another.
type RestoreRequest struct {
	ProvisionRequest
	// Source is the database whose archive the new one recovers from.
	Source string
	// Target is the instant to recover the source to.
	Target time.Time
}

// Validate rejects a request that cannot become a restore.
func (r RestoreRequest) Validate() error {
	if err := r.ProvisionRequest.Validate(); err != nil {
		return err
	}
	source, err := uuid.Parse(r.Source)
	if err != nil {
		return fmt.Errorf("dbsched: invalid restore source: %w", err)
	}
	if source == uuid.MustParse(r.DatabaseID) {
		return errors.New("dbsched: a database cannot restore into itself")
	}
	if r.Target.IsZero() {
		return errors.New("dbsched: a restore needs a point in time")
	}
	return nil
}

// restoreRun is one restore this process is running.
type restoreRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// Restore places a database recovered from req.Source as of req.Target and
// starts restoring it in the background. It lands on the source's node,
// where the source's base backups are. The database stays provisioning
// until it has recovered, promoted and taken its own first base backup,
// then parks; a failed restore leaves it failed. Repeating a request
// returns the same database; another source or target is ErrConflict.
func (s *Scheduler) Restore(ctx context.Context, req RestoreRequest) (VM, error) {
	if err := req.Validate(); err != nil {
		return VM{}, fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	req.DatabaseID = uuid.MustParse(req.DatabaseID).String()
	// Stores keep microseconds; a retry must compare equal.
	req.Target = req.Target.UTC().Truncate(time.Microsecond)
	source, err := s.store.Get(ctx, req.Source)
	if err != nil {
		return VM{}, err
	}
	existing, err := s.store.Get(ctx, req.DatabaseID)
	if err == nil {
		return s.resumeExisting(ctx, existing, source.DatabaseID, req.Target)
	}
	if !errors.Is(err, ErrNotFound) {
		return VM{}, err
	}
	if err := s.restorable(source, req); err != nil {
		return VM{}, err
	}
	vm, err := s.store.Place(ctx, VM{
		DatabaseID: req.DatabaseID, NodeID: source.NodeID, Major: req.Major, VcpuCount: req.VcpuCount, MemMiB: req.MemMiB,
		VolumeBytes: req.VolumeBytes, AlwaysOn: req.AlwaysOn, RestoreWindowSeconds: req.RestoreWindowSeconds,
		RestoreSource: source.DatabaseID, RestoreTarget: req.Target,
	})
	if err != nil {
		return vm, err
	}
	return s.resumeExisting(ctx, vm, source.DatabaseID, req.Target)
}

func (s *Scheduler) resumeExisting(ctx context.Context, vm VM, source string, target time.Time) (VM, error) {
	if vm.RestoreSource != source || !vm.RestoreTarget.Equal(target) {
		return vm, ErrConflict
	}
	s.resumeRestore(ctx, vm)
	return vm, nil
}

// restorable checks a new restore against its source.
func (s *Scheduler) restorable(source VM, req RestoreRequest) error {
	switch {
	case !source.Bootstrapped || source.LastBackupAt.IsZero():
		return fmt.Errorf("%w: source %s has no base backup", ErrInvalid, source.DatabaseID)
	case req.Major != source.Major:
		return fmt.Errorf("%w: postgres major %d differs from the source's %d", ErrInvalid, req.Major, source.Major)
	case req.VolumeBytes < source.VolumeBytes:
		return fmt.Errorf("%w: volume smaller than the source's", ErrInvalid)
	case req.Target.Before(source.CreatedAt):
		return fmt.Errorf("%w: point in time precedes the source", ErrInvalid)
	case req.Target.After(s.now()):
		return fmt.Errorf("%w: point in time is in the future", ErrInvalid)
	case source.RestoreWindowSeconds > 0 && req.Target.Before(s.now().Add(-time.Duration(source.RestoreWindowSeconds)*time.Second)):
		// dbbackupd keeps the archive at least this long; older points may
		// already be pruned.
		return fmt.Errorf("%w: point in time is outside the source's restore window", ErrInvalid)
	}
	return nil
}

// ResumeRestores starts every provisioning restore not already running
// here; the lease keeps two replicas from running the same one.
func (s *Scheduler) ResumeRestores(ctx context.Context) error {
	vms, err := s.store.List(ctx, StateProvisioning)
	if err != nil {
		return err
	}
	for _, vm := range vms {
		s.resumeRestore(ctx, vm)
	}
	return nil
}

// resumeRestore runs vm's restore in the background unless this process
// already is. The restore outlives the call that started it.
func (s *Scheduler) resumeRestore(ctx context.Context, vm VM) {
	if vm.State != StateProvisioning || vm.RestoreSource == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.RestoreTimeout)
	run := &restoreRun{cancel: cancel, done: make(chan struct{})}
	if _, running := s.restores.LoadOrStore(vm.DatabaseID, run); running {
		cancel()
		return
	}
	s.restoreWG.Add(1)
	go func() {
		defer s.restoreWG.Done()
		defer close(run.done)
		defer s.restores.Delete(vm.DatabaseID)
		defer cancel()
		s.runRestore(ctx, vm.DatabaseID)
	}()
}

// stopRestore cancels this process's restore of id, if any, and waits for
// it to wind down.
func (s *Scheduler) stopRestore(id string) {
	if v, ok := s.restores.Load(id); ok {
		run := v.(*restoreRun)
		run.cancel()
		<-run.done
	}
}

func (s *Scheduler) runRestore(ctx context.Context, id string) {
	lease, err := s.store.ClaimRestore(ctx, id, time.Time{}, s.cfg.RestoreLease)
	if err != nil {
		// Another replica holds it, or it is no longer restoring.
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	var lost atomic.Bool
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		s.renewRestore(ctx, id, lease, func() { lost.Store(true); cancel() })
	}()
	defer func() { cancel(); <-renewed }()
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return
	}
	node, err := s.node(ctx, vm.NodeID)
	if err == nil {
		err = s.restore(ctx, node, vm)
	}
	if err == nil {
		return
	}
	if lost.Load() {
		// Deleted, or the store was unreachable: whoever holds the
		// database next stops the VM.
		s.log.Warn("dbsched: restore lease lost", "database_id", id, "err", err)
		return
	}
	s.log.Warn("dbsched: restore failed", "database_id", id, "source", vm.RestoreSource, "err", err)
	cleanup := context.WithoutCancel(ctx)
	if node.ID != "" {
		_ = s.vmm.Stop(cleanup, node, id)
	}
	_ = s.store.Transition(cleanup, id, []State{StateProvisioning}, StateFailed, TransitionOptions{LastError: errCodeRestore})
}

// renewRestore keeps the lease until ctx ends; failing to renew it calls
// lost.
func (s *Scheduler) renewRestore(ctx context.Context, id string, lease time.Time, lost func()) {
	ticker := time.NewTicker(s.cfg.RestoreLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		next, err := s.store.ClaimRestore(ctx, id, lease, s.cfg.RestoreLease)
		if err != nil {
			if ctx.Err() == nil {
				lost()
			}
			return
		}
		lease = next
	}
}

// restore recovers vm into its volume, backs it up and parks it.
func (s *Scheduler) restore(ctx context.Context, node Node, vm VM) error {
	if err := s.flushSource(ctx, vm); err != nil {
		return fmt.Errorf("flush source archive: %w", err)
	}
	// A previous attempt may have left the VM running; the guest restarts
	// an unfinished extraction on the next boot.
	if err := s.vmm.Stop(ctx, node, vm.DatabaseID); err != nil {
		return err
	}
	if err := s.vmm.CreateVolume(ctx, node, vm.DatabaseID, vm.VolumeBytes); err != nil {
		return err
	}
	if err := s.vmm.Start(ctx, node, vm); err != nil {
		return err
	}
	if err := s.pollReady(ctx, node, vm.DatabaseID, s.cfg.RestoreTimeout, restorePoll); err != nil {
		return err
	}
	// The restored database archives to its own archive from its new
	// timeline; its first base backup makes it restorable in turn.
	s.takeBackup(ctx, node, vm.DatabaseID)
	if err := s.vmm.Stop(ctx, node, vm.DatabaseID); err != nil {
		return err
	}
	return s.store.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true})
}

// flushSource makes the source's archive cover the restore target.
// Recovery to a time stops only at a commit past it and fails if the
// archive ends first, so the source (woken if parked) commits after the
// target and ships that WAL.
func (s *Scheduler) flushSource(ctx context.Context, vm VM) error {
	source, err := s.Wake(ctx, vm.RestoreSource)
	if err != nil {
		return err
	}
	node, err := s.node(ctx, source.NodeID)
	if err != nil {
		return err
	}
	resp, err := s.vmm.Control(ctx, node, source.DatabaseID, dbguest.Request{Op: dbguest.OpFlushWAL, After: vm.RestoreTarget})
	_ = s.Touch(ctx, source.DatabaseID)
	if err != nil || resp.Error != "" {
		return errors.Join(err, guestError(resp))
	}
	return nil
}
