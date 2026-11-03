package dbsched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

// Moving a database to another node (ADR-1027 operations). The database is
// parked, its volume copied byte for byte to the new node, and its record
// switched there; it then wakes on the new node like after any park. The
// copy keeps PostgreSQL's timeline, so the archive simply continues: the
// old node first ships to the shared bucket every archive file it still
// holds, and drops its copy once the database is gone from it. Until the
// switch the old volume is untouched, so any failure leaves the database
// parked where it was and discards the copy.

const errCodeMove = "move_failed"

// Move moves a database to target, or to the active node with the most
// free volume capacity when target is empty. A running database is parked
// for the move and woken on its new node, as is an always-on one; clients
// see one stop and boot, plus the copy. A database that is not parked or
// running is ErrConflict. It needs a shared archive.
func (s *Scheduler) Move(ctx context.Context, id, target string) (VM, error) {
	unlock := s.lock(id)
	defer unlock()
	moving, wake, err := s.beginMoveLocked(ctx, id, target)
	if err != nil {
		return moving, err
	}
	return s.finishMoveLocked(ctx, moving, wake)
}

// StartMove is an operator's move: it parks the database and records it
// moving under the move's lease, returning it with its MoveNode, and
// finishes the move in the background. It returns once the move is
// durable, so the operator's intent is answered within its claim, while a
// copy can take hours. ctx bounds the background copy as well: a schedd
// that stops aborts the move, leaving the database where it was. The
// outcome is the database's node or its last_error (move_failed), and
// schedd_database_moves_total.
func (s *Scheduler) StartMove(ctx context.Context, id, target string) (VM, error) {
	// A database moving here holds its lock for the whole copy: a second
	// request must not wait for it past the intent's claim.
	unlock, ok := s.lockWithin(ctx, id, s.cfg.StartMoveWait)
	if !ok {
		return VM{}, fmt.Errorf("%w: the database is busy (moving, waking or parking); try again", ErrConflict)
	}
	moving, wake, err := s.beginMoveLocked(ctx, id, target)
	if err != nil {
		unlock()
		return moving, err
	}
	s.moveWG.Add(1)
	go func() {
		defer s.moveWG.Done()
		defer unlock()
		_, _ = s.finishMoveLocked(ctx, moving, wake) // finishMoveLocked logs and counts the outcome
	}()
	return moving, nil
}

// lockWithin takes the database's lock if it frees within d.
func (s *Scheduler) lockWithin(ctx context.Context, id string, d time.Duration) (func(), bool) {
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for {
		if unlock, ok := s.tryLock(id); ok {
			return unlock, true
		}
		select {
		case <-ctx.Done():
			return nil, false
		case <-deadline.C:
			return nil, false
		case <-tick.C:
		}
	}
}

// beginMoveLocked parks the database if it runs and marks it moving to
// target. It reports whether the database should run again afterwards.
// A failure leaves it as it was.
func (s *Scheduler) beginMoveLocked(ctx context.Context, id, target string) (VM, bool, error) {
	if !s.cfg.SharedArchive {
		return VM{}, false, fmt.Errorf("%w: moving a database needs a shared archive", ErrInvalid)
	}
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return vm, false, err
	}
	wake := vm.AlwaysOn
	switch vm.State {
	case StateRunning:
		wake = true
		if err := s.parkLocked(ctx, id); err != nil {
			return vm, false, err
		}
	case StateParked:
	default:
		return vm, false, ErrConflict
	}
	moving, err := s.store.BeginMove(ctx, id, target, s.cfg.MoveLease)
	if err != nil {
		if wake {
			_, _ = s.wakeLocked(ctx, id) // back up where it was
		}
		return vm, false, err
	}
	return moving, wake, nil
}

// finishMoveLocked copies, switches and wakes a database beginMoveLocked
// marked moving; a failure aborts the move and wakes it where it was.
func (s *Scheduler) finishMoveLocked(ctx context.Context, moving VM, wake bool) (VM, error) {
	id := moving.DatabaseID
	started := s.now()
	err := s.runMove(ctx, moving)
	s.cfg.Metrics.moved(err)
	if err != nil {
		s.log.Warn("dbsched: move failed", "database_id", id, "from", moving.NodeID, "to", moving.MoveNode, "err", err)
		if wake {
			_, _ = s.wakeLocked(context.WithoutCancel(ctx), id)
		}
		after, getErr := s.store.Get(context.WithoutCancel(ctx), id)
		if getErr != nil {
			after = moving
		}
		return after, err
	}
	s.log.Info("dbsched: moved", "database_id", id, "from", moving.NodeID, "to", moving.MoveNode, "took", s.now().Sub(started))
	if wake {
		return s.wakeLocked(ctx, id)
	}
	return s.Get(ctx, id)
}

// runMove copies, switches and cleans up under the move's lease. A failure
// before the switch aborts the move and discards the copy.
func (s *Scheduler) runMove(ctx context.Context, vm VM) error {
	from, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return s.abortMove(ctx, vm, vm.MoveLeaseUntil, err)
	}
	to, err := s.node(ctx, vm.MoveNode)
	if err != nil {
		return s.abortMove(ctx, vm, vm.MoveLeaseUntil, err)
	}
	lease := &heldLease{held: vm.MoveLeaseUntil}
	copyCtx, cancel := context.WithTimeout(ctx, s.cfg.MoveTimeout)
	renewed := make(chan struct{})
	go func() {
		defer close(renewed)
		s.renewMove(copyCtx, vm.DatabaseID, lease, cancel)
	}()
	err = s.copyVolume(copyCtx, from, to, vm.DatabaseID)
	cancel()
	<-renewed
	held := lease.get()
	if err != nil {
		return s.abortMove(ctx, vm, held, err)
	}
	if err := s.store.FinishMove(ctx, vm.DatabaseID, held); err != nil {
		// The switch may have committed before the error reached us.
		if now, getErr := s.store.Get(context.WithoutCancel(ctx), vm.DatabaseID); getErr == nil && now.NodeID == to.ID {
			return s.releaseOld(ctx, from, vm.DatabaseID)
		}
		return s.abortMove(ctx, vm, held, fmt.Errorf("switch: %w", err))
	}
	return s.releaseOld(ctx, from, vm.DatabaseID)
}

// copyVolume puts the database's volume on to and the old node's archive
// files in the bucket. A leftover copy from an earlier failed move goes
// first.
func (s *Scheduler) copyVolume(ctx context.Context, from, to Node, id string) error {
	if err := s.vmm.DiscardVolume(ctx, to, id); err != nil {
		return fmt.Errorf("clear destination: %w", err)
	}
	if err := s.vmm.CopyVolume(ctx, from, to, id); err != nil {
		return fmt.Errorf("copy volume: %w", err)
	}
	if err := s.vmm.SyncArchive(ctx, from, id); err != nil {
		return fmt.Errorf("ship the archive to the bucket: %w", err)
	}
	return nil
}

// releaseOld removes the moved database's old volume and archive copy. The
// move is done either way: CollectOrphans retries a failure.
func (s *Scheduler) releaseOld(ctx context.Context, from Node, id string) error {
	if err := s.vmm.ReleaseVolume(context.WithoutCancel(ctx), from, id); err != nil {
		s.log.Warn("dbsched: release of a moved database's old volume failed; the orphan sweep retries", "database_id", id, "node_id", from.ID, "err", err)
	}
	return nil
}

// abortMove leaves the database parked on its node and discards the copy,
// returning cause.
func (s *Scheduler) abortMove(ctx context.Context, vm VM, held time.Time, cause error) error {
	ctx = context.WithoutCancel(ctx)
	if err := s.store.AbortMove(ctx, vm.DatabaseID, held, errCodeMove); err != nil {
		s.log.Warn("dbsched: abort move", "database_id", vm.DatabaseID, "err", err)
	}
	s.discardCopy(ctx, vm)
	return cause
}

// discardCopy deletes a failed move's copy unless the database lives
// there after all.
func (s *Scheduler) discardCopy(ctx context.Context, vm VM) {
	now, err := s.store.Get(ctx, vm.DatabaseID)
	if err != nil || now.NodeID == vm.MoveNode || now.State == StateMoving {
		return
	}
	to, err := s.node(ctx, vm.MoveNode)
	if err == nil {
		err = s.vmm.DiscardVolume(ctx, to, vm.DatabaseID)
	}
	if err != nil {
		s.log.Warn("dbsched: discard a failed move's copy; the orphan sweep retries", "database_id", vm.DatabaseID, "node_id", vm.MoveNode, "err", err)
	}
}

// heldLease is the lease a running move or upgrade holds, renewed in the
// background.
type heldLease struct {
	mu   sync.Mutex
	held time.Time
}

func (l *heldLease) get() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held
}

func (l *heldLease) set(held time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held = held
}

// keepLease renews lease, length long, every third of it until ctx ends;
// a renewal failing while ctx lives calls lost.
func keepLease(ctx context.Context, lease *heldLease, length time.Duration, renew func(context.Context, time.Time) (time.Time, error), lost func(error)) {
	ticker := time.NewTicker(length / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		next, err := renew(ctx, lease.get())
		if err != nil {
			if ctx.Err() == nil {
				lost(err)
			}
			return
		}
		lease.set(next)
	}
}

// renewMove keeps the move's lease until ctx ends; losing it calls lost.
func (s *Scheduler) renewMove(ctx context.Context, id string, lease *heldLease, lost func()) {
	keepLease(ctx, lease, s.cfg.MoveLease, func(ctx context.Context, held time.Time) (time.Time, error) {
		return s.store.RenewMove(ctx, id, held, s.cfg.MoveLease)
	}, func(err error) {
		s.log.Warn("dbsched: move lease lost", "database_id", id, "err", err)
		lost()
	})
}

// SettleMoves aborts moves whose lease lapsed, left by a schedd replica
// that stopped mid-move, and discards their copies. It reports how many it
// aborted.
func (s *Scheduler) SettleMoves(ctx context.Context) (int, error) {
	moving, err := s.store.List(ctx, StateMoving)
	if err != nil {
		return 0, err
	}
	settled := 0
	for _, vm := range moving {
		unlock, ok := s.tryLock(vm.DatabaseID)
		if !ok {
			continue // this process is moving it
		}
		err := s.store.AbortMove(ctx, vm.DatabaseID, time.Time{}, errCodeMove)
		if err == nil {
			s.discardCopy(ctx, vm)
			settled++
			s.log.Warn("dbsched: settled an abandoned move", "database_id", vm.DatabaseID, "from", vm.NodeID, "to", vm.MoveNode)
		} else if !errors.Is(err, ErrConflict) {
			s.log.Warn("dbsched: settle move", "database_id", vm.DatabaseID, "err", err)
		}
		unlock()
	}
	return settled, nil
}

// EvacuateDraining moves databases off draining nodes, one at a time, until
// none is left to move or ctx ends. Parked databases move; running ones
// move when they park, except always-on ones, which never park and so move
// with a stop and boot. A production-class database with a synchronized
// standby switches over to it instead (switchover.go), running or not,
// and moves only if that fails. It reports how many it moved.
func (s *Scheduler) EvacuateDraining(ctx context.Context) (int, error) {
	if !s.cfg.EvacuateDraining || !s.cfg.SharedArchive {
		return 0, nil
	}
	moved := 0
	failed := map[string]bool{}
	for ctx.Err() == nil {
		vm, ok, err := s.nextEvacuation(ctx, failed)
		if err != nil || !ok {
			return moved, err
		}
		if switchable(vm) {
			err := s.Switchover(ctx, vm.DatabaseID)
			if err == nil {
				moved++
				continue
			}
			s.log.Warn("dbsched: evacuation switchover failed; moving instead", "database_id", vm.DatabaseID, "node_id", vm.NodeID, "err", err)
		}
		after, err := s.Move(ctx, vm.DatabaseID, "")
		if err != nil || after.NodeID == vm.NodeID {
			// No room elsewhere, or the copy failed: try the others, and
			// this one again next sweep.
			failed[vm.DatabaseID] = true
			if err != nil && !errors.Is(err, ErrNoCapacity) {
				s.log.Warn("dbsched: evacuation move failed", "database_id", vm.DatabaseID, "node_id", vm.NodeID, "err", err)
			}
			continue
		}
		moved++
	}
	return moved, ctx.Err()
}

// nextEvacuation picks a database on a draining node to move.
func (s *Scheduler) nextEvacuation(ctx context.Context, skip map[string]bool) (VM, bool, error) {
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return VM{}, false, err
	}
	draining := map[string]bool{}
	for _, n := range nodes {
		if n.Lifecycle == NodeDraining {
			draining[n.ID] = true
		}
	}
	if len(draining) == 0 {
		return VM{}, false, nil
	}
	vms, err := s.store.List(ctx, StateParked, StateRunning)
	if err != nil {
		return VM{}, false, err
	}
	for _, vm := range vms {
		if draining[vm.NodeID] && !skip[vm.DatabaseID] && !vm.ResizePending && (vm.State == StateParked || vm.AlwaysOn || switchable(vm)) {
			return vm, true, nil
		}
	}
	return VM{}, false, nil
}
