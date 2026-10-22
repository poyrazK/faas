package sched

// lost_vms.go — VMs the recovery arbiter declared dead with their node.
//
// RecreateInstance parks the rows of an unreachable node as if their VMs had
// died with it. When the node was only slow to answer, the VMs keep running
// behind parked rows: invisible to admission, re-created by the next wake,
// and never reclaimed while vmmd is up. On production-us a starved control
// plane timed pings out ~22 times in 36 minutes and left 53 such VMs holding
// 5.5 GB (hunt #9, H9-171). The engine remembers each VM it parked this way
// and destroys it through vmmd once the node answers a ping again.

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

const (
	// lostVMRetention bounds how long a destroy keeps being retried. vmmd's
	// own sweep of VMs whose row is no longer live covers what remains.
	lostVMRetention = time.Hour
	// lostVMPassTimeout bounds one destroy pass over a node.
	lostVMPassTimeout = 2 * time.Minute
)

// lostVMSet is the per-node set of instance ids whose VM may still be
// running behind a parked row.
type lostVMSet struct {
	mu      sync.Mutex
	byNode  map[string]map[string]time.Time
	passing map[string]bool
}

func (s *lostVMSet) add(nodeID, instanceID string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.byNode == nil {
		s.byNode = map[string]map[string]time.Time{}
	}
	if s.byNode[nodeID] == nil {
		s.byNode[nodeID] = map[string]time.Time{}
	}
	if _, ok := s.byNode[nodeID][instanceID]; !ok {
		s.byNode[nodeID][instanceID] = now
	}
}

func (s *lostVMSet) forget(nodeID, instanceID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.byNode[nodeID], instanceID)
	if len(s.byNode[nodeID]) == 0 {
		delete(s.byNode, nodeID)
	}
}

// beginPass returns the node's pending ids and claims the node's pass, or
// nil when there is nothing to do or a pass is already running.
func (s *lostVMSet) beginPass(nodeID string) map[string]time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.byNode[nodeID]) == 0 || s.passing[nodeID] {
		return nil
	}
	if s.passing == nil {
		s.passing = map[string]bool{}
	}
	s.passing[nodeID] = true
	pending := make(map[string]time.Time, len(s.byNode[nodeID]))
	for id, since := range s.byNode[nodeID] {
		pending[id] = since
	}
	return pending
}

func (s *lostVMSet) endPass(nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.passing, nodeID)
}

// noteLostVM records that instanceID's row was parked while nodeID looked
// dead, so its VM may still be running there.
func (e *Engine) noteLostVM(nodeID, instanceID string) {
	if e == nil || nodeID == "" || instanceID == "" {
		return
	}
	e.lostVMs.add(nodeID, instanceID, time.Now())
}

// DestroyLostVMs starts a background destroy of the VMs the recovery arbiter
// parked on nodeID while it looked dead. The heartbeat calls it after every
// answered ping, so it returns at once; a node with nothing pending costs a
// map lookup.
func (e *Engine) DestroyLostVMs(ctx context.Context, nodeID string) {
	if e == nil {
		return
	}
	pending := e.lostVMs.beginPass(nodeID)
	if pending == nil {
		return
	}
	go func() {
		defer e.lostVMs.endPass(nodeID)
		passCtx, cancel := context.WithTimeout(detachedServiceContext(ctx), lostVMPassTimeout)
		defer cancel()
		e.destroyLostVMs(passCtx, nodeID, pending)
	}()
}

// destroyLostVMs destroys each pending VM whose row is still not live and
// returns how many it destroyed. A failed destroy stays pending for the next
// answered ping until lostVMRetention.
func (e *Engine) destroyLostVMs(ctx context.Context, nodeID string, pending map[string]time.Time) int {
	destroyed := 0
	for instanceID, since := range pending {
		ins, err := e.store.InstanceByID(ctx, instanceID)
		switch {
		case err == nil && state.IsLive(ins.State):
			// The row serves again; whatever runs under this id is not lost.
			e.lostVMs.forget(nodeID, instanceID)
			continue
		case err != nil && !errors.Is(err, state.ErrNotFound):
			e.log.Warn("sched: lost VM: load instance", "node_id", nodeID, "instance_id", instanceID, "err", err)
			continue
		}
		if err := e.timedDestroy(ctx, nodeID, instanceID, DestroyTimeout); err != nil {
			if time.Since(since) > lostVMRetention {
				e.lostVMs.forget(nodeID, instanceID)
				e.log.Warn("sched: lost VM: giving up on destroy", "node_id", nodeID, "instance_id", instanceID, "err", err)
				continue
			}
			e.log.Warn("sched: lost VM: destroy failed; retrying on the next answered ping",
				"node_id", nodeID, "instance_id", instanceID, "err", err)
			continue
		}
		e.lostVMs.forget(nodeID, instanceID)
		destroyed++
		e.log.Info("sched: lost VM: destroyed a VM the recovery arbiter had parked",
			"node_id", nodeID, "instance_id", instanceID, "parked_for", time.Since(since).Round(time.Second).String())
	}
	return destroyed
}
