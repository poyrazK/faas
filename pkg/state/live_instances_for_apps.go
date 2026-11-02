package state

import (
	"context"
	"sort"
)

// LiveInstancesForAppsReader lists the live instances of a set of apps in one
// query. The reaper ticks every 10 s and used to call ListInstancesForApp once
// per owned app, which returns every wake an app ever had: on the production
// control plane that carried thousands of parked rows per app for a handful of
// live ones. Callers fall back to ListInstancesForApp when the store does not
// implement it.
type LiveInstancesForAppsReader interface {
	ListLiveInstancesForApps(ctx context.Context, appIDs []string) ([]Instance, error)
}

// ListLiveInstancesForApps returns the instances of appIDs in a live state
// (IsLive), grouped by app with the newest start first, as ListInstancesForApp
// orders one app's rows. It reads instances_app_idx (app_id, state).
func (s *PgStore) ListLiveInstancesForApps(ctx context.Context, appIDs []string) ([]Instance, error) {
	if len(appIDs) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode, request_count
		   from instances
		  where app_id = any($1::uuid[])
		    and state = any($2::text[])
		  order by app_id, started_at desc`, appIDs, LiveStates())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ResidentInstancesOnNodeReader lists the live instances physically on one node
// (ADR 20261102072403). schedd's stats poller reads its own node's every
// 200 ms; it used to read every live instance in the fleet and filter.
type ResidentInstancesOnNodeReader interface {
	ListResidentInstancesOnNode(ctx context.Context, nodeID string) ([]Instance, error)
}

// ListResidentInstancesOnNode returns the instances in a live state (IsLive) on
// nodeID, newest start first, through instances_resident_node_idx.
func (s *PgStore) ListResidentInstancesOnNode(ctx context.Context, nodeID string) ([]Instance, error) {
	if !validUUID(nodeID) {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx,
		`select id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode, request_count
		   from instances
		  where node_id = $1::uuid
		    and state in ('waking','cold_booting','running','draining','snapshotting','migrating','warm','frozen')
		  order by started_at desc`, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// ListResidentInstancesOnNode mirrors PgStore.ListResidentInstancesOnNode.
func (m *MemStore) ListResidentInstancesOnNode(_ context.Context, nodeID string) ([]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, ins := range m.instances {
		if ins.NodeID == nodeID && IsLive(ins.State) {
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}

// ListLiveInstancesForApps mirrors PgStore.ListLiveInstancesForApps.
func (m *MemStore) ListLiveInstancesForApps(_ context.Context, appIDs []string) ([]Instance, error) {
	want := make(map[string]struct{}, len(appIDs))
	for _, id := range appIDs {
		want[id] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, ins := range m.instances {
		if _, ok := want[ins.AppID]; ok && IsLive(ins.State) {
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].AppID != out[j].AppID {
			return out[i].AppID < out[j].AppID
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}
