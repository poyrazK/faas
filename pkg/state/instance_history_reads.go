package state

import (
	"context"
	"sort"
)

// An app keeps a row for every wake for the retention window (30 days), so a
// read of its whole history carries thousands of rows for a busy app. These
// narrower reads serve schedd paths that need only some of them (ADR
// 20261103131546).

// InstancesByIDsReader reads instance rows by ID in one query. schedd's
// admission repair reads the rows its ledger holds for an app instead of the
// app's whole history.
type InstancesByIDsReader interface {
	InstancesByIDs(ctx context.Context, ids []string) (map[string]Instance, error)
}

// AppInstancesInStatesReader reads one app's instances in the given states.
// schedd's deleted-app cleanup acts only on live and account-deleting rows.
type AppInstancesInStatesReader interface {
	ListAppInstancesInStates(ctx context.Context, appID string, states []string) ([]Instance, error)
}

var (
	_ InstancesByIDsReader       = (*PgStore)(nil)
	_ InstancesByIDsReader       = (*MemStore)(nil)
	_ AppInstancesInStatesReader = (*PgStore)(nil)
	_ AppInstancesInStatesReader = (*MemStore)(nil)
)

const instanceHistoryReadColumns = `id, coalesce(app_id::text, ''), coalesce(deployment_id::text, ''), state, coalesce(netns,''), coalesce(guest_uid,0),
		        coalesce(host(host_ip),''), ram_mb, started_at, last_request_at, parked_at, node_id, wake_id, framework_ready_at, tail_count, mode, request_count`

// InstancesByIDs returns the rows among ids, keyed by ID, with the columns
// ListInstancesForApp reads. An ID with no row is absent.
func (s *PgStore) InstancesByIDs(ctx context.Context, ids []string) (map[string]Instance, error) {
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if validUUID(id) {
			valid = append(valid, id)
		}
	}
	out := make(map[string]Instance, len(valid))
	if len(valid) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `select `+instanceHistoryReadColumns+`
		   from instances where id = any($1::uuid[])`, valid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	instances, err := scanInstances(rows)
	if err != nil {
		return nil, err
	}
	for _, ins := range instances {
		out[ins.ID] = ins
	}
	return out, nil
}

// ListAppInstancesInStates returns appID's instances in states, newest start
// first, through instances_app_idx (app_id, state).
func (s *PgStore) ListAppInstancesInStates(ctx context.Context, appID string, states []string) ([]Instance, error) {
	if !validUUID(appID) || len(states) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `select `+instanceHistoryReadColumns+`
		   from instances
		  where app_id = $1::uuid
		    and state = any($2::text[])
		  order by started_at desc`, appID, states)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanInstances(rows)
}

// InstancesByIDs mirrors PgStore.InstancesByIDs.
func (m *MemStore) InstancesByIDs(_ context.Context, ids []string) (map[string]Instance, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]Instance, len(ids))
	for _, id := range ids {
		if ins, ok := m.instances[id]; ok {
			out[id] = ins
		}
	}
	return out, nil
}

// ListAppInstancesInStates mirrors PgStore.ListAppInstancesInStates.
func (m *MemStore) ListAppInstancesInStates(_ context.Context, appID string, states []string) ([]Instance, error) {
	want := make(map[string]struct{}, len(states))
	for _, st := range states {
		want[st] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Instance
	for _, ins := range m.instances {
		if _, ok := want[ins.State]; ok && ins.AppID == appID {
			out = append(out, ins)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	return out, nil
}
