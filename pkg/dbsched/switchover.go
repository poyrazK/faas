package dbsched

import (
	"context"
	"fmt"
)

// Planned switchover (ADR-1027 production class). A production-class
// database whose standby is synchronized can change nodes in seconds,
// losing nothing, by handing itself to its standby instead of copying
// its volume: the evacuation of a draining node does so before it falls
// back to a move. It is the failover path with a clean stop first:
//
//   - A running database's primary stops cleanly while the standby is
//     still synchronous, so the standby holds every commit, and the old
//     primary takes no more. The standby is then promoted at once, and
//     clients reconnect to it.
//   - A parked database's record moves to its standby's node, whose next
//     boot promotes the standby volume.
//
// The monitor then places a new standby, which copies the new primary.
// A promotion that fails after the clean stop parks the database where it
// was, standby still trusted, so its next wake resumes it there.

// switchable reports whether vm can switch over to its standby now. A
// running database's standby must be running; nodes are checked by
// Switchover.
func switchable(vm VM) bool {
	if !vm.Replicated || !vm.StandbySynced || vm.StandbyNode == "" || vm.ResizePending || vm.PromotePending {
		return false
	}
	return vm.State == StateParked || vm.State == StateRunning && vm.StandbyRunning
}

// Switchover hands a production-class database to its synchronized
// standby. A database without one, or whose standby's node is not active,
// is ErrConflict.
func (s *Scheduler) Switchover(ctx context.Context, id string) error {
	unlock := s.lock(id)
	defer unlock()
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if !switchable(vm) {
		return fmt.Errorf("%w: %s has no synchronized standby to switch over to", ErrConflict, id)
	}
	standby, err := s.node(ctx, vm.StandbyNode)
	if err != nil || standby.Lifecycle != NodeActive {
		return fmt.Errorf("%w: the standby of %s is on a node that is not active", ErrConflict, id)
	}
	primary, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return err
	}
	if vm.State == StateRunning {
		if err := s.vmm.Stop(ctx, primary, id); err != nil {
			return fmt.Errorf("dbsched: switchover %s: stop the primary: %w", id, err)
		}
	}
	if err := s.failoverLocked(ctx, id, primary, failoverSwitchover); err != nil {
		if vm.State == StateRunning {
			s.parkAfterFailedSwitchover(context.WithoutCancel(ctx), vm)
		}
		return fmt.Errorf("dbsched: switchover %s: %w", id, err)
	}
	s.log.Info("dbsched: switched over to the standby", "database_id", id, "from", vm.NodeID, "to", vm.StandbyNode, "state", vm.State)
	return nil
}

// parkAfterFailedSwitchover parks a running database whose primary
// stopped cleanly for a switchover whose promotion then failed: its
// standby stops cleanly after it and stays trusted, and the next wake
// resumes both where they were.
func (s *Scheduler) parkAfterFailedSwitchover(ctx context.Context, vm VM) {
	if err := s.store.Transition(ctx, vm.DatabaseID, []State{StateRunning}, StateParking, TransitionOptions{}); err != nil {
		s.log.Warn("dbsched: park after a failed switchover", "database_id", vm.DatabaseID, "err", err)
		return
	}
	s.stopStandby(ctx, vm, true)
	if err := s.store.Transition(ctx, vm.DatabaseID, []State{StateParking}, StateParked, TransitionOptions{}); err != nil {
		s.log.Warn("dbsched: park after a failed switchover", "database_id", vm.DatabaseID, "err", err)
	}
	s.freed.signal(vm.NodeID)
}
