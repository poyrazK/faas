package dbsched

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/dbguest"
)

// Production class (ADR-1027): a replicated database runs a hot standby
// with its own volume on a second node, streaming from its primary. While
// the standby is synchronous every commit waits until the standby has
// flushed it, so the standby holds every acknowledged commit and may take
// over when the primary's node is lost.
//
// StandbySynced records exactly that, and it is the only thing a failover
// trusts. It is set only after the primary reports the standby
// synchronous, and cleared before a primary may commit without its
// standby: before a wake that does not resume it (every primary boots
// asynchronous) and before the scheduler turns a primary asynchronous
// because its standby stopped keeping up. A clean park stops the primary
// first, so a standby that was synchronous stays so while parked.
//
// Standbys attach in the background after a wake: the wake never waits
// for its standby. A wake does resume a standby that was synchronized when
// the database parked, on a node still active: the record keeps trusting
// it, and the primary turns synchronous before it serves its first
// client, so no commit is acknowledged without the standby even before
// it is back. Commits wait for its boot. If it is not back within
// StandbyResumeTimeout, or cannot be claimed, the record stops trusting
// it first and the primary turns asynchronous.

// defaultStandbyResumeTimeout bounds a standby's resume (Config).
const defaultStandbyResumeTimeout = 30 * time.Second

// errStandbyNotNeeded: the database no longer wants this standby (it
// parked, or its standby moved) by the time a step ran.
var errStandbyNotNeeded = errors.New("dbsched: the standby is no longer needed")

// kickStandby attaches a running replicated database's standby in the
// background, one attach per database at a time in this process. The
// attach outlives ctx, keeping only its values.
func (s *Scheduler) kickStandby(ctx context.Context, id string) {
	if _, busy := s.attaching.LoadOrStore(id, struct{}{}); busy {
		return
	}
	s.attachTried.Store(id, s.now())
	s.standbyWG.Add(1)
	go func() {
		defer s.standbyWG.Done()
		defer s.attaching.Delete(id)
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), s.cfg.StandbySeedTimeout+s.cfg.StandbySyncTimeout+time.Minute)
		defer cancel()
		started := s.now()
		err := s.attachStandby(ctx, id)
		switch {
		case err == nil:
			s.log.Info("dbsched: standby synchronous", "database_id", id, "took", s.now().Sub(started))
		case !errors.Is(err, errStandbyNotNeeded):
			s.log.Warn("dbsched: standby attach failed", "database_id", id, "err", err)
		}
	}()
}

// attachStandby boots a running replicated database's standby and makes it
// synchronous. A failed attach leaves the primary asynchronous and the
// standby stopped, for the monitor to retry.
func (s *Scheduler) attachStandby(ctx context.Context, id string) error {
	vm, primary, standby, fresh, err := s.claimStandby(ctx, id)
	if err != nil {
		if !errors.Is(err, errStandbyNotNeeded) {
			s.relaxPrimary(context.WithoutCancel(ctx), id)
		}
		return err
	}
	if vm.StandbySynced {
		// Resuming: commits wait until the standby is back. Turning wake
		// resumes off leaves this bound at its default.
		timeout := s.cfg.StandbyResumeTimeout
		if timeout < 0 {
			timeout = defaultStandbyResumeTimeout
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err = s.bootStandby(ctx, vm, primary, standby, fresh); err == nil {
		err = s.finishAttach(ctx, vm, standby)
	}
	if err != nil {
		s.abandonStandby(context.WithoutCancel(ctx), id, primary, standby)
	}
	return err
}

// claimStandby, under the database's lock, gives a running replicated
// database a usable standby node and claims the standby's memory there.
// fresh reports a standby node newly chosen: anything of the database
// there is stale (an old primary's volume after a failover) and goes
// before the standby is seeded.
func (s *Scheduler) claimStandby(ctx context.Context, id string) (vm VM, primary, standby Node, fresh bool, err error) {
	unlock := s.lock(id)
	defer unlock()
	vm, err = s.store.Get(ctx, id)
	if err != nil {
		return VM{}, Node{}, Node{}, false, err
	}
	if vm.State != StateRunning || !vm.Replicated || vm.StandbyRunning {
		return VM{}, Node{}, Node{}, false, errStandbyNotNeeded
	}
	if current, err := s.node(ctx, vm.StandbyNode); vm.StandbyNode == "" || err != nil || current.Lifecycle != NodeActive {
		// Not onto a node the monitor finds silent.
		var silent []string
		s.nodeFailing.Range(func(k, _ any) bool { silent = append(silent, k.(string)); return true })
		if vm, err = s.store.ReplaceStandby(ctx, id, silent...); err != nil {
			return VM{}, Node{}, Node{}, false, fmt.Errorf("dbsched: place a standby: %w", err)
		}
		fresh = true
	}
	if primary, err = s.node(ctx, vm.NodeID); err != nil {
		return VM{}, Node{}, Node{}, false, err
	}
	if standby, err = s.node(ctx, vm.StandbyNode); err != nil {
		return VM{}, Node{}, Node{}, false, err
	}
	if vm, err = s.store.ClaimStandby(ctx, id); err != nil {
		return VM{}, Node{}, Node{}, false, fmt.Errorf("dbsched: claim the standby: %w", err)
	}
	return vm, primary, standby, fresh, nil
}

// bootStandby starts the standby, which copies its primary first if its
// volume is new, and waits until the primary reports it synchronous. On a
// fresh standby node any volume of the database is stale and discarded.
func (s *Scheduler) bootStandby(ctx context.Context, vm VM, primary, standby Node, fresh bool) error {
	id := vm.DatabaseID
	// A standby left by an earlier attempt goes first.
	if err := s.vmm.Stop(ctx, standby, id); err != nil {
		return fmt.Errorf("stop a leftover standby: %w", err)
	}
	if fresh {
		if err := s.vmm.DiscardVolume(ctx, standby, id); err != nil {
			return fmt.Errorf("discard a stale volume: %w", err)
		}
	}
	if err := s.vmm.CreateVolume(ctx, standby, id, vm.VolumeBytes); err != nil {
		return fmt.Errorf("create the standby volume: %w", err)
	}
	if err := s.vmm.GrowVolume(ctx, standby, id, vm.VolumeBytes); err != nil {
		return fmt.Errorf("size the standby volume: %w", err)
	}
	// A resumed standby's primary is synchronous already, and stays so.
	prepare := dbguest.ReplicationAsync
	if vm.StandbySynced {
		prepare = dbguest.ReplicationSync
	}
	if _, err := s.vmm.Replication(ctx, primary, id, prepare, standby.ID); err != nil {
		return fmt.Errorf("prepare the primary: %w", err)
	}
	if err := s.vmm.StartStandby(ctx, standby, primary, vm); err != nil {
		return fmt.Errorf("boot the standby: %w", err)
	}
	if err := s.awaitStandby(ctx, standby, id); err != nil {
		return err
	}
	if _, err := s.vmm.Replication(ctx, primary, id, dbguest.ReplicationSync, standby.ID); err != nil {
		return fmt.Errorf("make the standby synchronous: %w", err)
	}
	return s.awaitSynchronous(ctx, primary, id)
}

// awaitStandby waits until the standby replays from its primary.
func (s *Scheduler) awaitStandby(ctx context.Context, standby Node, id string) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.StandbySeedTimeout)
	defer cancel()
	for {
		resp, err := s.vmm.Control(ctx, standby, id, dbguest.Request{Op: dbguest.OpStatus})
		switch {
		case err == nil && resp.State == dbguest.StateStandby:
			return nil
		case err == nil && resp.State == dbguest.StateFailed:
			return fmt.Errorf("standby failed: %w", guestError(resp))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("standby not replaying: %w", errors.Join(ctx.Err(), err))
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// awaitSynchronous waits until the primary reports its standby
// synchronous.
func (s *Scheduler) awaitSynchronous(ctx context.Context, primary Node, id string) error {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.StandbySyncTimeout)
	defer cancel()
	for {
		status, err := s.vmm.Replication(ctx, primary, id, "", "")
		if err == nil && status.Synchronous {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("standby not synchronous: %w", errors.Join(ctx.Err(), err))
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// finishAttach records the standby synchronized if the database still runs
// with it.
func (s *Scheduler) finishAttach(ctx context.Context, vm VM, standby Node) error {
	unlock := s.lock(vm.DatabaseID)
	defer unlock()
	cur, err := s.store.Get(ctx, vm.DatabaseID)
	if err != nil {
		return err
	}
	if cur.State != StateRunning || !cur.StandbyRunning || cur.StandbyNode != standby.ID || cur.NodeID != vm.NodeID {
		return errStandbyNotNeeded
	}
	return s.store.MarkStandbySynced(ctx, vm.DatabaseID, true)
}

// abandonStandby undoes a failed attach: the primary stops waiting for the
// standby (after the record says it may not be promoted), and the standby
// stops.
func (s *Scheduler) abandonStandby(ctx context.Context, id string, primary, standby Node) {
	unlock := s.lock(id)
	defer unlock()
	cur, err := s.store.Get(ctx, id)
	if err != nil {
		return
	}
	if cur.NodeID == primary.ID && isActive(cur.State) {
		if err := s.store.MarkStandbySynced(ctx, id, false); err != nil {
			s.log.Warn("dbsched: unsync an abandoned standby", "database_id", id, "err", err)
			return
		}
		if _, err := s.vmm.Replication(ctx, primary, id, dbguest.ReplicationAsync, standby.ID); err != nil {
			s.log.Warn("dbsched: make a primary asynchronous", "database_id", id, "err", err)
		}
	}
	if err := s.vmm.Stop(ctx, standby, id); err != nil {
		s.log.Warn("dbsched: stop an abandoned standby", "database_id", id, "node_id", standby.ID, "err", err)
	}
	if cur.StandbyNode == standby.ID && cur.StandbyRunning {
		_ = s.store.ReleaseStandby(ctx, id, false)
	}
}

// RestartStandby restarts a running production-class database's standby
// onto its node's current base while the primary runs on. The standby
// stops cleanly and stays trusted: the primary is synchronous, so a commit
// waits until the standby is back and none is acknowledged without it.
// The attach then resumes it, as after a wake (within
// StandbyResumeTimeout, else it is given up record first). With resuming
// turned off the record stops trusting the standby and the primary turns
// asynchronous first, so commits never wait. A database whose standby is
// not running and synchronized is ErrConflict.
func (s *Scheduler) RestartStandby(ctx context.Context, id string) error {
	if err := s.stopStandbyForRestart(ctx, id); err != nil {
		return err
	}
	s.kickStandby(ctx, id)
	return nil
}

func (s *Scheduler) stopStandbyForRestart(ctx context.Context, id string) error {
	unlock := s.lock(id)
	defer unlock()
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if _, busy := s.attaching.Load(id); busy || vm.State != StateRunning || !vm.Replicated || !vm.StandbyRunning || !vm.StandbySynced {
		return fmt.Errorf("%w: %s has no running synchronized standby to restart", ErrConflict, id)
	}
	primary, err := s.node(ctx, vm.NodeID)
	if err != nil {
		return err
	}
	standby, err := s.node(ctx, vm.StandbyNode)
	if err != nil {
		return err
	}
	clean := s.cfg.StandbyResumeTimeout >= 0
	if !clean {
		if err := s.store.MarkStandbySynced(ctx, id, false); err != nil {
			return err
		}
		if _, err := s.vmm.Replication(ctx, primary, id, dbguest.ReplicationAsync, vm.StandbyNode); err != nil {
			return fmt.Errorf("dbsched: restart the standby of %s: make the primary asynchronous: %w", id, err)
		}
	}
	if err := s.vmm.Stop(ctx, standby, id); err != nil {
		return fmt.Errorf("dbsched: restart the standby of %s: stop it: %w", id, err)
	}
	return s.store.ReleaseStandby(ctx, id, clean)
}

// resumesStandby reports whether a wake resumes the database's standby: a
// production-class database whose standby was synchronized when it
// parked, on a node that is active and not silent.
func (s *Scheduler) resumesStandby(ctx context.Context, vm VM) bool {
	if s.cfg.StandbyResumeTimeout < 0 || !vm.Replicated || !vm.StandbySynced || vm.PromotePending || vm.StandbyNode == "" {
		return false
	}
	if _, silent := s.nodeFailing.Load(vm.StandbyNode); silent {
		return false
	}
	node, err := s.node(ctx, vm.StandbyNode)
	return err == nil && node.Lifecycle == NodeActive
}

// resumeSynchronous makes a woken primary wait for its standby before its
// first client. When the primary cannot be made synchronous, the record
// stops trusting the standby, and the database runs asynchronous, as any
// boot leaves it; no client has committed yet. An error means the record
// could not say so, and the wake must not go on.
func (s *Scheduler) resumeSynchronous(ctx context.Context, node Node, vm VM) error {
	_, err := s.vmm.Replication(ctx, node, vm.DatabaseID, dbguest.ReplicationSync, vm.StandbyNode)
	if err == nil {
		s.cfg.Metrics.resumedStandby(true)
		return nil
	}
	s.cfg.Metrics.resumedStandby(false)
	s.log.Warn("dbsched: resume a synchronized standby; the database wakes asynchronous", "database_id", vm.DatabaseID, "err", err)
	if err := s.store.MarkStandbySynced(ctx, vm.DatabaseID, false); err != nil {
		return fmt.Errorf("unsync the standby: %w", err)
	}
	return nil
}

// relaxPrimary turns a resumed primary asynchronous when its standby
// cannot attach at all: the record stops trusting the standby first, then
// the primary stops waiting for it. abandonStandby does the same for a
// claimed standby.
func (s *Scheduler) relaxPrimary(ctx context.Context, id string) {
	unlock := s.lock(id)
	defer unlock()
	cur, err := s.store.Get(ctx, id)
	if err != nil || cur.State != StateRunning || !cur.StandbySynced || cur.StandbyRunning {
		return
	}
	node, err := s.node(ctx, cur.NodeID)
	if err != nil {
		return
	}
	if err := s.store.MarkStandbySynced(ctx, id, false); err != nil {
		s.log.Warn("dbsched: unsync a standby that cannot attach", "database_id", id, "err", err)
		return
	}
	if _, err := s.vmm.Replication(ctx, node, id, dbguest.ReplicationAsync, cur.StandbyNode); err != nil {
		s.log.Warn("dbsched: make a primary asynchronous", "database_id", id, "err", err)
	}
}

// stopStandby stops a database's standby VM after its primary stopped.
// A clean stop after the primary's clean stop keeps the standby
// synchronized; anything else does not.
func (s *Scheduler) stopStandby(ctx context.Context, vm VM, primaryStoppedCleanly bool) {
	if !vm.StandbyRunning {
		return
	}
	node, err := s.node(ctx, vm.StandbyNode)
	if err == nil {
		err = s.vmm.Stop(ctx, node, vm.DatabaseID)
	}
	if err != nil {
		s.log.Warn("dbsched: stop a standby", "database_id", vm.DatabaseID, "node_id", vm.StandbyNode, "err", err)
	}
	if err := s.store.ReleaseStandby(ctx, vm.DatabaseID, primaryStoppedCleanly && err == nil); err != nil {
		s.log.Warn("dbsched: release a standby", "database_id", vm.DatabaseID, "err", err)
	}
}

// deleteStandby removes a deleted database's standby volume; an
// unreachable standby node leaves it to the orphan sweep.
func (s *Scheduler) deleteStandby(ctx context.Context, vm VM) {
	if vm.StandbyNode == "" {
		return
	}
	node, err := s.node(ctx, vm.StandbyNode)
	if err != nil || node.Lifecycle == NodeUnavailable {
		return
	}
	if err := s.vmm.Stop(ctx, node, vm.DatabaseID); err == nil {
		err = s.vmm.DiscardVolume(ctx, node, vm.DatabaseID)
		if err != nil {
			s.log.Warn("dbsched: delete a standby volume; the orphan sweep retries", "database_id", vm.DatabaseID, "node_id", node.ID, "err", err)
		}
	}
}

// CheckStandbys keeps replicated databases' standbys attached and
// synchronous. For a running database it attaches a missing standby,
// records a synchronous one, and turns the primary asynchronous at once
// when its standby stopped keeping up or its node drains or is lost,
// since every commit waits on a synchronous standby. A parked database's
// standby on such a node moves to another. It reports how many standbys
// it gave up on.
func (s *Scheduler) CheckStandbys(ctx context.Context) (int, error) {
	vms, err := s.store.List(ctx, StateRunning, StateParked)
	if err != nil {
		return 0, err
	}
	nodes, err := s.store.Nodes(ctx)
	if err != nil {
		return 0, err
	}
	byID := make(map[string]Node, len(nodes))
	for _, n := range nodes {
		byID[n.ID] = n
	}
	dropped := 0
	// Parked databases whose synchronized standby could take over, by
	// their primary's node: nothing runs there to probe, so the node is.
	parkedOn := map[string][]VM{}
	for _, vm := range vms {
		if vm.DropStandbySlot && vm.State == StateRunning {
			s.dropStandbySlot(ctx, vm, byID[vm.NodeID])
		}
		if !vm.Replicated {
			continue
		}
		standbyNode, known := byID[vm.StandbyNode]
		standbyGone := vm.StandbyNode != "" && (!known || standbyNode.Lifecycle != NodeActive)
		if vm.State == StateParked {
			if standbyGone {
				s.replaceParkedStandby(ctx, vm.DatabaseID)
			} else if vm.StandbySynced {
				parkedOn[vm.NodeID] = append(parkedOn[vm.NodeID], vm)
			}
			continue
		}
		if _, busy := s.attaching.Load(vm.DatabaseID); busy {
			continue
		}
		if !vm.StandbyRunning {
			if last, ok := s.attachTried.Load(vm.DatabaseID); !ok || s.now().Sub(last.(time.Time)) >= s.cfg.StandbyRetry {
				s.kickStandby(ctx, vm.DatabaseID)
			}
			continue
		}
		if s.checkStandby(ctx, vm, byID[vm.NodeID], standbyNode, standbyGone) {
			dropped++
		}
	}
	s.failoverParked(ctx, parkedOn, byID)
	return dropped, nil
}

// failoverParked probes, at once, each node holding parked databases'
// primaries, and fails those databases over to their synchronized
// standbys once their node has not answered for AutoFailoverAfter (the
// running databases' rule). Nothing runs on the silent node, so the
// promotion waits for the next wake, on the standby's node. A node the
// operator marked unavailable is settled by SettleLostNodes.
func (s *Scheduler) failoverParked(ctx context.Context, parkedOn map[string][]VM, byID map[string]Node) {
	if s.cfg.AutoFailoverAfter < 0 || len(parkedOn) == 0 {
		return
	}
	probed := make(map[string]error, len(parkedOn))
	var mu sync.Mutex
	var wg sync.WaitGroup
	for nodeID := range parkedOn {
		node, ok := byID[nodeID]
		if !ok || node.Lifecycle == NodeUnavailable {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.probeNode(ctx, node)
			mu.Lock()
			probed[nodeID] = err
			mu.Unlock()
		}()
	}
	wg.Wait()
	for nodeID, err := range probed {
		if err == nil {
			s.nodeFailing.Delete(nodeID)
			continue
		}
		if !unreachable(err) {
			continue
		}
		first, _ := s.nodeFailing.LoadOrStore(nodeID, s.now())
		silent := s.now().Sub(first.(time.Time))
		if silent < s.cfg.AutoFailoverAfter {
			continue
		}
		for _, vm := range parkedOn[nodeID] {
			standby, ok := byID[vm.StandbyNode]
			if !ok || s.probeNode(ctx, standby) != nil {
				s.log.Warn("dbsched: parked database's node unreachable, but its standby's node is not answering either; no failover",
					"database_id", vm.DatabaseID, "node_id", nodeID, "standby_node", vm.StandbyNode)
				continue
			}
			s.log.Warn("dbsched: parked database's node unreachable; failing over to its synchronized standby", "database_id", vm.DatabaseID,
				"node_id", nodeID, "unreachable_for", silent)
			if err := s.failover(ctx, vm.DatabaseID, byID[nodeID], failoverParked); err != nil {
				s.log.Warn("dbsched: automatic failover failed", "database_id", vm.DatabaseID, "err", err)
			}
		}
	}
}

// probeNode asks a node's vmmd for its database volumes, a cheap call
// that answers only if the node does.
func (s *Scheduler) probeNode(ctx context.Context, node Node) error {
	probe, cancel := context.WithTimeout(ctx, standbyProbeTimeout)
	defer cancel()
	_, err := s.vmm.ListVolumes(probe, node)
	return err
}

// checkStandby checks one running database's standby; it reports whether
// it gave the standby up.
func (s *Scheduler) checkStandby(ctx context.Context, vm VM, primary, standby Node, standbyGone bool) bool {
	id := vm.DatabaseID
	probe, cancel := context.WithTimeout(ctx, standbyProbeTimeout)
	status, err := s.vmm.Replication(probe, primary, id, "", "")
	cancel()
	if err != nil {
		// An unreachable primary cannot be made asynchronous: its commits
		// wait, as they must, until it is reachable again or its standby
		// takes over.
		if unreachable(err) {
			s.autoFailover(ctx, vm, primary, standby)
		}
		return false
	}
	s.nodeFailing.Delete(primary.ID)
	switch {
	case standbyGone || (status.Mode == dbguest.ReplicationSync && !status.Synchronous):
		s.unsyncedSince.Delete(id)
		reason := dropLagging
		if standbyGone {
			reason = dropNodeGone
		}
		return s.dropStandby(ctx, vm, primary, standby, standbyGone, reason)
	case status.Synchronous && !vm.StandbySynced:
		s.unsyncedSince.Delete(id)
		s.recordSynced(ctx, vm)
	case !status.Synchronous && status.Mode != dbguest.ReplicationSync:
		// Claimed but never synchronous, and not attaching here: an
		// attach that died with its schedd. Another replica's may still
		// be seeding, so it is given StandbyStaleClaim first.
		first, _ := s.unsyncedSince.LoadOrStore(id, s.now())
		if s.now().Sub(first.(time.Time)) >= s.cfg.StandbyStaleClaim {
			s.unsyncedSince.Delete(id)
			return s.dropStandby(ctx, vm, primary, standby, false, dropStaleClaim)
		}
	default:
		s.unsyncedSince.Delete(id)
	}
	return false
}

// dropStandby gives up a running database's standby: the record first
// says the standby may not be promoted, then the primary stops waiting for
// it, and the standby stops for the next attach. A standby whose node is
// going away loses its slot too: its replacement copies the primary anew.
func (s *Scheduler) dropStandby(ctx context.Context, vm VM, primary, standby Node, replace bool, reason string) bool {
	id := vm.DatabaseID
	unlock := s.lock(id)
	defer unlock()
	cur, err := s.store.Get(ctx, id)
	if err != nil || cur.State != StateRunning || !cur.StandbyRunning || cur.StandbyNode != vm.StandbyNode || cur.NodeID != primary.ID {
		return false
	}
	if err := s.store.MarkStandbySynced(ctx, id, false); err != nil {
		s.log.Warn("dbsched: unsync a lagging standby", "database_id", id, "err", err)
		return false
	}
	mode := dbguest.ReplicationAsync
	if replace {
		mode = dbguest.ReplicationOff
	}
	if _, err := s.vmm.Replication(ctx, primary, id, mode, standbyNodeArg(mode, cur.StandbyNode)); err != nil {
		// The record already forbids promotion; the next check retries.
		s.log.Warn("dbsched: make a primary asynchronous", "database_id", id, "err", err)
		return false
	}
	if err := s.vmm.Stop(ctx, standby, id); err != nil {
		s.log.Warn("dbsched: stop a dropped standby", "database_id", id, "node_id", standby.ID, "err", err)
	}
	if err := s.store.ReleaseStandby(ctx, id, false); err != nil {
		s.log.Warn("dbsched: release a dropped standby", "database_id", id, "err", err)
	}
	s.attachTried.Delete(id)
	s.cfg.Metrics.standbyDropped(reason)
	s.log.Warn("dbsched: standby dropped; the primary runs asynchronous until it attaches again", "database_id", id, "standby_node", cur.StandbyNode, "reason", reason)
	return true
}

// dropStandbySlot drops the replication slot a primary kept for a
// standby it no longer has, once the primary runs: the slot would hold WAL
// up to a quarter of the volume. Replication off drops only an inactive
// slot, so the mark is cleared only once the primary reports none.
func (s *Scheduler) dropStandbySlot(ctx context.Context, vm VM, primary Node) {
	if _, err := s.vmm.Replication(ctx, primary, vm.DatabaseID, dbguest.ReplicationOff, ""); err != nil {
		s.log.Warn("dbsched: drop a retired standby's slot", "database_id", vm.DatabaseID, "err", err)
		return
	}
	status, err := s.vmm.Replication(ctx, primary, vm.DatabaseID, "", "")
	if err != nil || status.Mode != dbguest.ReplicationOff {
		return
	}
	if err := s.store.ClearStandbySlotDrop(ctx, vm.DatabaseID); err != nil {
		s.log.Warn("dbsched: record a retired standby's slot dropped", "database_id", vm.DatabaseID, "err", err)
		return
	}
	s.log.Info("dbsched: retired standby's slot dropped", "database_id", vm.DatabaseID)
}

// standbyNodeArg is the replication control's standby node for mode.
func standbyNodeArg(mode dbguest.ReplicationMode, node string) string {
	if mode == dbguest.ReplicationOff {
		return ""
	}
	return node
}

// recordSynced records a standby the primary reports synchronous, for an
// attach whose last step did not reach the store.
func (s *Scheduler) recordSynced(ctx context.Context, vm VM) {
	unlock := s.lock(vm.DatabaseID)
	defer unlock()
	if err := s.store.MarkStandbySynced(ctx, vm.DatabaseID, true); err != nil && !errors.Is(err, ErrConflict) {
		s.log.Warn("dbsched: record a synchronous standby", "database_id", vm.DatabaseID, "err", err)
	}
}

// replaceParkedStandby moves a parked database's standby off a node that
// drains or is lost. The primary's volume holds everything, so nothing is
// lost; the next wake copies it to the new standby node.
func (s *Scheduler) replaceParkedStandby(ctx context.Context, id string) {
	unlock, ok := s.tryLock(id)
	if !ok {
		return
	}
	defer unlock()
	vm, err := s.store.ReplaceStandby(ctx, id)
	if err != nil {
		if !errors.Is(err, ErrConflict) {
			s.log.Warn("dbsched: replace a standby", "database_id", id, "err", err)
		}
		return
	}
	s.log.Info("dbsched: moved a parked database's standby", "database_id", id, "standby_node", vm.StandbyNode)
}

// RunStandbyLoop checks standbys every StandbyCheckInterval until ctx ends.
func (s *Scheduler) RunStandbyLoop(ctx context.Context) {
	ticker := time.NewTicker(s.cfg.StandbyCheckInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n, err := s.CheckStandbys(ctx); err != nil {
				s.log.Warn("dbsched: standby check failed", "err", err)
			} else if n > 0 {
				s.log.Warn("dbsched: dropped standbys", "count", n)
			}
		}
	}
}

// failover hands a database whose node is lost to its synchronized
// standby: a running standby is promoted now; a parked database's standby
// volume is promoted on its next boot. The old primary is stopped first
// if its node still answers, so it cannot take writes the standby will
// never see.
func (s *Scheduler) failover(ctx context.Context, id string, lost Node, trigger string) error {
	unlock := s.lock(id)
	defer unlock()
	return s.failoverLocked(ctx, id, lost, trigger)
}

// failoverLocked is failover with the database's lock held.
func (s *Scheduler) failoverLocked(ctx context.Context, id string, lost Node, trigger string) (err error) {
	vm, err := s.store.Get(ctx, id)
	if err != nil {
		return err
	}
	if vm.NodeID != lost.ID || !canFailover(vm) {
		return ErrConflict // already failed over, or nothing to promote
	}
	defer func() { s.cfg.Metrics.failedOver(trigger, err) }()
	standby, err := s.node(ctx, vm.StandbyNode)
	if err != nil || standby.Lifecycle == NodeUnavailable {
		return fmt.Errorf("%w: the standby's node is unavailable too", ErrNodeUnavailable)
	}
	fence, cancel := context.WithTimeout(ctx, 5*time.Second)
	_ = s.vmm.Stop(fence, lost, id)
	cancel()
	if vm.StandbyRunning {
		resp, err := s.vmm.Control(ctx, standby, id, dbguest.Request{Op: dbguest.OpPromote})
		promoted := err == nil && (resp.Error == "" || (resp.Error == dbguest.ErrCodeNotStandby && resp.State == dbguest.StateReady))
		if !promoted {
			return fmt.Errorf("dbsched: promote the standby of %s: %w", id, errors.Join(err, guestError(resp)))
		}
	}
	after, err := s.store.Failover(ctx, id)
	if err != nil {
		return err
	}
	s.attachTried.Delete(id)
	s.log.Warn("dbsched: failed over to the standby", "database_id", id, "from", lost.ID, "to", after.NodeID, "state", after.State, "promote_pending", after.PromotePending)
	return nil
}

// standbyProbeTimeout bounds one probe of a node, so an unreachable node
// cannot stall the monitor.
const standbyProbeTimeout = 3 * time.Second

// unreachable reports an error that says the node did not answer, as
// opposed to the node answering that something is wrong.
func unreachable(err error) bool {
	switch status.Code(err) {
	case codes.Unavailable, codes.DeadlineExceeded:
		return true
	}
	return errors.Is(err, context.DeadlineExceeded)
}

// autoFailover fails a running database over to its synchronized standby
// once its primary's node has not answered for AutoFailoverAfter, if the
// standby's node answers now and the standby is replaying. When schedd
// cannot reach the standby's node either, it is more likely cut off
// itself, and it does nothing. A primary that is merely cut off from
// schedd cannot commit once its synchronous standby is promoted; the
// orphan sweep stops it when its node is reachable again.
func (s *Scheduler) autoFailover(ctx context.Context, vm VM, primary, standby Node) {
	first, _ := s.nodeFailing.LoadOrStore(primary.ID, s.now())
	if s.cfg.AutoFailoverAfter < 0 || s.now().Sub(first.(time.Time)) < s.cfg.AutoFailoverAfter || !vm.StandbySynced {
		return
	}
	probe, cancel := context.WithTimeout(ctx, standbyProbeTimeout)
	resp, err := s.vmm.Control(probe, standby, vm.DatabaseID, dbguest.Request{Op: dbguest.OpStatus})
	cancel()
	if err != nil || resp.State != dbguest.StateStandby {
		s.log.Warn("dbsched: primary unreachable, but its standby is not answering either; no failover", "database_id", vm.DatabaseID,
			"node_id", primary.ID, "standby_node", standby.ID, "err", err)
		return
	}
	s.log.Warn("dbsched: primary unreachable; failing over to its synchronized standby", "database_id", vm.DatabaseID,
		"node_id", primary.ID, "unreachable_for", s.now().Sub(first.(time.Time)))
	if err := s.failover(ctx, vm.DatabaseID, primary, failoverRunning); err != nil {
		s.log.Warn("dbsched: automatic failover failed", "database_id", vm.DatabaseID, "err", err)
	}
}
