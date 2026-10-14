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
	if !s.cfg.SharedArchive {
		return VM{}, fmt.Errorf("%w: moving a database needs a shared archive", ErrInvalid)
	}
	unlock := s.lock(id)
	defer unlock()
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return vm, err
	}
	wake := vm.AlwaysOn
	switch vm.State {
	case StateRunning:
		wake = true
		if err := s.parkLocked(ctx, id); err != nil {
			return vm, err
		}
	case StateParked:
	default:
		return vm, ErrConflict
	}
	moving, err := s.store.BeginMove(ctx, id, target, s.cfg.MoveLease)
	if err != nil {
		if wake {
			_, _ = s.wakeLocked(ctx, id) // back up where it was
		}
		return vm, err
	}
	started := s.now()
	if err := s.runMove(ctx, moving); err != nil {
		s.log.Warn("dbsched: move failed", "database_id", id, "from", moving.NodeID, "to", moving.MoveNode, "err", err)
		if wake {
			_, _ = s.wakeLocked(context.WithoutCancel(ctx), id)
		}
		after, getErr := s.store.Get(context.WithoutCancel(ctx), id)
		if getErr != nil {
			after = vm
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
	lease := &moveLease{held: vm.MoveLeaseUntil}
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

// moveLease is the lease a running move holds, renewed in the background.
type moveLease struct {
	mu   sync.Mutex
	held time.Time
}

func (l *moveLease) get() time.Time {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.held
}

// renewMove keeps the move's lease until ctx ends; losing it calls lost.
func (s *Scheduler) renewMove(ctx context.Context, id string, lease *moveLease, lost func()) {
	ticker := time.NewTicker(s.cfg.MoveLease / 3)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		next, err := s.store.RenewMove(ctx, id, lease.get(), s.cfg.MoveLease)
		if err != nil {
			if ctx.Err() == nil {
				s.log.Warn("dbsched: move lease lost", "database_id", id, "err", err)
				lost()
			}
			return
		}
		lease.mu.Lock()
		lease.held = next
		lease.mu.Unlock()
	}
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
// with a stop and boot. It reports how many it moved.
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
		if draining[vm.NodeID] && !skip[vm.DatabaseID] && !vm.ResizePending && (vm.State == StateParked || vm.AlwaysOn) {
			return vm, true, nil
		}
	}
	return VM{}, false, nil
}
