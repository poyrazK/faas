package state

import (
	"context"
	"sort"
)

// The min-instances floor reconciler (pkg/sched/floor) ticks every second on
// every scheduler. Walking every deployment, history included, and reading
// each live deployment's app was the largest steady Postgres load on the
// production control plane: about 120 app reads per scheduler per second
// while no app had a floor. These listings return only the live deployments
// of apps that can carry one, so an idle fleet costs one indexed query per
// tick. The reconciler still decides with the full App and Deployment.

// floorCandidateFilter is the SQL form of appMayHaveFloor: a superset of
// App.EffectiveMinInstances() > 0 or Deployment.EffectiveMinInstances() > 0.
// Every live deployment of a matching app is returned, so the reconciler's
// serving-scope check (servingScopes, inheritsNoFloor) sees the same rows it
// did before.
const floorCandidateFilter = `
	    and a.status <> 'deleted'
	    and d.status = 'live'
	    and (a.min_instances > 0
	         or (jsonb_typeof(a.scaling_policy->'min_instances') = 'number'
	             and (a.scaling_policy->>'min_instances')::numeric > 0)
	         or (jsonb_typeof(a.scaling_policy->'schedules') = 'array'
	             and jsonb_array_length(a.scaling_policy->'schedules') > 0)
	         or exists (select 1 from deployments f
	                     where f.app_id = a.id and f.status = 'live' and f.min_instances > 0))
	  order by d.created_at desc`

// ListFloorDeployments returns the live deployments of every non-deleted app
// that can carry a min-instances floor.
func (s *PgStore) ListFloorDeployments(ctx context.Context) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx,
		`select `+deploymentSelectColumnsQualified+`
		   from deployments d
		   join apps a on a.id = d.app_id
		  where true`+floorCandidateFilter)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// ListFloorDeploymentsByNodeID is ListFloorDeployments for the apps owned by
// one compute node (the floor reconciler's owner shard).
func (s *PgStore) ListFloorDeploymentsByNodeID(ctx context.Context, nodeID string) ([]Deployment, error) {
	rows, err := s.pool.Query(ctx,
		`select `+deploymentSelectColumnsQualified+`
		   from deployments d
		   join apps a on a.id = d.app_id
		  where a.node_id = $1`+floorCandidateFilter, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// ListFloorDeployments mirrors PgStore.ListFloorDeployments.
func (m *MemStore) ListFloorDeployments(_ context.Context) ([]Deployment, error) {
	return m.listFloorDeployments(func(App) bool { return true }), nil
}

// ListFloorDeploymentsByNodeID mirrors PgStore.ListFloorDeploymentsByNodeID.
func (m *MemStore) ListFloorDeploymentsByNodeID(_ context.Context, nodeID string) ([]Deployment, error) {
	return m.listFloorDeployments(func(a App) bool { return a.NodeID == nodeID }), nil
}

func (m *MemStore) listFloorDeployments(owned func(App) bool) []Deployment {
	m.mu.Lock()
	defer m.mu.Unlock()
	deploymentFloor := map[string]bool{}
	for _, d := range m.deployments {
		if d.Status == DeployLive && d.MinInstances > 0 {
			deploymentFloor[d.AppID] = true
		}
	}
	var out []Deployment
	for _, d := range m.deployments {
		if d.Status != DeployLive {
			continue
		}
		a, ok := m.apps[d.AppID]
		if !ok || a.Status == AppDeleted || !owned(a) || !appMayHaveFloor(a, deploymentFloor[a.ID]) {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// appMayHaveFloor reports whether any floor source is set: the app's
// min_instances, its scaling policy's min_instances or schedules, or a live
// deployment's own min_instances (liveDeploymentFloor).
func appMayHaveFloor(a App, liveDeploymentFloor bool) bool {
	if a.MinInstances > 0 || liveDeploymentFloor {
		return true
	}
	p := a.ScalingPolicy
	return p != nil && (p.MinInstances > 0 || len(p.Schedules) > 0)
}
