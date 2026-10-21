package state

import (
	"context"
	"sort"
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
