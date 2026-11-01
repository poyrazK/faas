package dbsched

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbarchive"
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
	// Snapshot names the source's snapshot at Target (snapshot.go), whose
	// hold keeps Target restorable outside the source's restore window.
	Snapshot string
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
	if r.Snapshot != "" && !dbarchive.ValidHoldName(r.Snapshot) {
		return errors.New("dbsched: invalid snapshot name")
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
// where the source's archive is local; with a shared archive it goes to
// another node when that one is full, draining or lost. The database stays
// provisioning until it has recovered, promoted and taken its own first
// base backup, then parks; a failed restore leaves it failed. Repeating a
// request returns the same database; another source or target is
// ErrConflict.
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
	if req.Snapshot != "" {
		// The snapshot's hold must pin exactly the target.
		hold, err := s.getHold(ctx, source, req.Snapshot)
		if errors.Is(err, ErrNotFound) {
			return VM{}, fmt.Errorf("%w: no snapshot %s of %s", ErrNotFound, req.Snapshot, source.DatabaseID)
		}
		if err != nil {
			return VM{}, err
		}
		if !hold.PointInTime.Equal(req.Target) {
			return VM{}, fmt.Errorf("%w: snapshot %s holds another point", ErrConflict, req.Snapshot)
		}
	}
	if err := s.restorable(source, req); err != nil {
		return VM{}, err
	}
	pin, err := s.restoreNode(ctx, source)
	if err != nil {
		return VM{}, err
	}
	place := VM{
		DatabaseID: req.DatabaseID, NodeID: pin, Major: req.Major, VcpuCount: req.VcpuCount, MemMiB: req.MemMiB,
		VolumeBytes: req.VolumeBytes, AlwaysOn: req.AlwaysOn, RestoreWindowSeconds: req.RestoreWindowSeconds,
		RestoreSource: source.DatabaseID, RestoreTarget: req.Target, Replicated: req.Replicated,
	}
	vm, err := s.store.Place(ctx, place)
	if errors.Is(err, ErrNoCapacity) && pin != "" && s.cfg.SharedArchive {
		// The source's node is full; any node reads the archive from the
		// bucket.
		place.NodeID = ""
		vm, err = s.store.Place(ctx, place)
	}
	if err != nil {
		return vm, err
	}
	return s.resumeExisting(ctx, vm, source.DatabaseID, req.Target)
}

// restoreNode is the node a restore must land on: the source's while it is
// active, otherwise any ("") when the archive is shared.
func (s *Scheduler) restoreNode(ctx context.Context, source VM) (string, error) {
	node, err := s.node(ctx, source.NodeID)
	if err != nil && !errors.Is(err, ErrNodeUnavailable) {
		return "", err
	}
	if err == nil && node.Lifecycle == NodeActive {
		return source.NodeID, nil
	}
	if !s.cfg.SharedArchive {
		return "", fmt.Errorf("%w: the source's node is %s and archives are not shared", ErrNodeUnavailable, cmp.Or(node.Lifecycle, "gone"))
	}
	return "", nil
}

// sourceNodeLost reports whether the operator has declared the source's
// node unavailable or removed it: its archive is then whatever reached
// the bucket, and the source cannot flush past the target.
func (s *Scheduler) sourceNodeLost(ctx context.Context, sourceID string) (bool, error) {
	source, err := s.store.Get(ctx, sourceID)
	if err != nil {
		return false, err
	}
	node, err := s.node(ctx, source.NodeID)
	if errors.Is(err, ErrNodeUnavailable) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return node.Lifecycle == NodeUnavailable, nil
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
	case source.State == StateUpgrading:
		// Its old major's archive is complete only once pg_upgrade ran.
		return fmt.Errorf("%w: source %s is upgrading; restore it once the upgrade ended", ErrConflict, source.DatabaseID)
	case req.Major != source.MajorAt(req.Target):
		return fmt.Errorf("%w: postgres major %d differs from the %d the source ran at that point", ErrInvalid, req.Major, source.MajorAt(req.Target))
	case req.VolumeBytes < source.VolumeBytes:
		return fmt.Errorf("%w: volume smaller than the source's", ErrInvalid)
	case req.Target.Before(source.CreatedAt):
		return fmt.Errorf("%w: point in time precedes the source", ErrInvalid)
	case req.Target.After(s.now()):
		return fmt.Errorf("%w: point in time is in the future", ErrInvalid)
	case req.Snapshot == "" && source.RestoreWindowSeconds > 0 && req.Target.Before(s.now().Add(-time.Duration(source.RestoreWindowSeconds)*time.Second)):
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
	source, err := s.store.Get(ctx, vm.RestoreSource)
	if err != nil {
		return fmt.Errorf("restore source: %w", err)
	}
	lost, err := s.sourceNodeLost(ctx, vm.RestoreSource)
	if err != nil {
		return fmt.Errorf("restore source: %w", err)
	}
	switch {
	case lost && !s.cfg.SharedArchive:
		return fmt.Errorf("%w: the source's node is lost and archives are not shared", ErrNodeUnavailable)
	case lost:
		// Recover to the target, or to the end of what reached the bucket
		// if that ends first; RestoreRecoveredTo records how far it got.
		s.log.Warn("dbsched: restore source's node is unavailable; recovering from the bucket, at most to the target",
			"database_id", vm.DatabaseID, "source", vm.RestoreSource, "target", vm.RestoreTarget)
		vm.RestoreUntilEnd = true
	case vm.Major != source.Major, source.State == StateFailed:
		// The target precedes the source's upgrade off this major, whose
		// cluster archived its last WAL before pg_upgrade ran; or the
		// source cannot run to commit past the target. Either way the
		// archive holds all there is: recover to the target or its end.
		s.log.Info("dbsched: restore source cannot extend this archive; recovering at most to the target",
			"database_id", vm.DatabaseID, "source", vm.RestoreSource, "target", vm.RestoreTarget, "major", vm.Major, "source_state", source.State)
		vm.RestoreUntilEnd = true
	default:
		if err := s.flushSource(ctx, vm); err != nil {
			return fmt.Errorf("flush source archive: %w", err)
		}
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
	s.recordRecoveredTo(ctx, node, vm)
	// The restored database archives to its own archive from its new
	// timeline; its first base backup makes it restorable in turn.
	s.takeBackup(ctx, node, vm.DatabaseID)
	if err := s.vmm.Stop(ctx, node, vm.DatabaseID); err != nil {
		return err
	}
	return s.store.Transition(ctx, vm.DatabaseID, []State{StateProvisioning}, StateParked, TransitionOptions{Bootstrapped: true})
}

// flushSource makes the source's archive cover the restore target.
func (s *Scheduler) flushSource(ctx context.Context, vm VM) error {
	return s.flushArchive(ctx, vm.RestoreSource, vm.RestoreTarget)
}

// flushArchive makes database id's archive reach after. Recovery to a
// time stops only at a commit past it and fails if the archive ends
// first, so the database (woken if parked) commits after it and ships
// that WAL.
func (s *Scheduler) flushArchive(ctx context.Context, id string, after time.Time) error {
	source, err := s.Wake(ctx, id)
	if err != nil {
		return err
	}
	node, err := s.node(ctx, source.NodeID)
	if err != nil {
		return err
	}
	resp, err := s.vmm.Control(ctx, node, source.DatabaseID, dbguest.Request{Op: dbguest.OpFlushWAL, After: after})
	_ = s.Touch(ctx, source.DatabaseID)
	if err != nil || resp.Error != "" {
		return errors.Join(err, guestError(resp))
	}
	return nil
}

// recordRecoveredTo stores how far the restore replayed. It is evidence for
// operators, so a failure to read or record it does not fail the restore.
func (s *Scheduler) recordRecoveredTo(ctx context.Context, node Node, vm VM) {
	resp, err := s.vmm.Control(ctx, node, vm.DatabaseID, dbguest.Request{Op: dbguest.OpStatus})
	if err != nil || resp.RecoveredTo.IsZero() {
		return
	}
	if err := s.store.RecordRestore(ctx, vm.DatabaseID, resp.RecoveredTo); err != nil {
		s.log.Warn("dbsched: record restore point", "database_id", vm.DatabaseID, "err", err)
	}
	s.log.Info("dbsched: restore recovered", "database_id", vm.DatabaseID, "target", vm.RestoreTarget,
		"recovered_to", resp.RecoveredTo, "until_end", vm.RestoreUntilEnd)
}
