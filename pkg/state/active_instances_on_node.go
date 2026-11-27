package state

import (
	"context"
	"sort"
	"strings"
)

// ListActiveInstancesOnNodeID is ListInstancesOnNodeID limited to instances
// admitted or serving on the node (waking, cold_booting, running). meterd's
// fleet stats refresh used ListInstancesOnNodeID to count these and read every
// instance a node ever hosted: 14,318 rows and ~740 ms per node on
// production-us. The literal state list lets PostgreSQL use the partial index
// instances_live_node_id_idx.
func (s *PgStore) ListActiveInstancesOnNodeID(ctx context.Context, nodeID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `
		select i.id, coalesce(i.app_id::text, ''), coalesce(i.deployment_id::text, ''), i.state, coalesce(i.netns,''), coalesce(i.guest_uid,0),
		       coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at, i.node_id, i.wake_id, i.framework_ready_at, i.tail_count, i.mode, i.request_count
		  from instances i
		 where i.node_id = $1
		   and i.state in ('waking', 'cold_booting', 'running')
		 order by i.started_at desc nulls last, i.id::text desc`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListActiveInstancesOnNodeID mirrors PgStore.ListActiveInstancesOnNodeID.
func (m *MemStore) ListActiveInstancesOnNodeID(_ context.Context, nodeID string) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, ins := range m.instances {
		if ins.NodeID != nodeID {
			continue
		}
		switch State(ins.State) {
		case StateWaking, StateColdBooting, StateRunning:
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}

// ResidentStates are the instance states that hold a VM on a node: serving,
// starting, paused in a warm pool or frozen, draining, snapshotting or
// migrating. They are exactly LiveStates (IsLive); parked, stopped, failed
// and evicted rows are history.
func ResidentStates() []State {
	out := make([]State, 0, len(liveStates))
	for _, st := range liveStates {
		out = append(out, State(st))
	}
	return out
}

// ListResidentInstancesOnNodeID is ListInstancesOnNodeID limited to resident
// instances (ResidentStates), newest first. It filters the physical
// instances.node_id like ListInstancesOnNodeID, so a VM mid-migration still
// shows on the node it occupies. The literal state list matches the partial
// index instances_recovery_node_live_idx; ListInstancesOnNodeID reads every
// instance the node ever hosted (14,318 rows on production-us).
func (s *PgStore) ListResidentInstancesOnNodeID(ctx context.Context, nodeID string) ([]Instance, error) {
	rows, err := s.pool.Query(ctx, `
		select i.id, coalesce(i.app_id::text, ''), coalesce(i.deployment_id::text, ''), i.state, coalesce(i.netns,''), coalesce(i.guest_uid,0),
		       coalesce(host(i.host_ip),''), i.ram_mb, i.started_at, i.last_request_at, i.parked_at, i.node_id, i.wake_id, i.framework_ready_at, i.tail_count, i.mode, i.request_count
		  from instances i
		 where i.node_id = $1
		   and i.state in ('running', 'cold_booting', 'waking', 'draining', 'snapshotting', 'migrating', 'warm', 'frozen')
		 order by i.started_at desc nulls last, i.id::text desc`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListResidentInstancesOnNodeID mirrors PgStore.ListResidentInstancesOnNodeID.
func (m *MemStore) ListResidentInstancesOnNodeID(_ context.Context, nodeID string) ([]Instance, error) {
	resident := make(map[State]bool)
	for _, st := range ResidentStates() {
		resident[st] = true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Instance, 0)
	for _, ins := range m.instances {
		// Lowercased like the callers' IsLive checks: MemStore accepts any
		// case, while PostgreSQL's CHECK only allows lowercase states.
		if ins.NodeID == nodeID && resident[State(strings.ToLower(ins.State))] {
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].StartedAt.After(out[j].StartedAt)
		}
		return out[i].ID > out[j].ID
	})
	return out, nil
}
