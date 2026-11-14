package state

import (
	"context"
	"slices"
	"sort"
)

// imaged's security and evidence sweeps act only on serving deployments, but
// read them through ListAllDeployments: every deployment of every listed app,
// history included, about 2,000 buffers and 96 ms a call on the production
// control plane (13.9 s at worst). This listing returns only the rows in the
// requested statuses, with the same app filter and order.

// statusDeploymentsQuery materializes the listed apps first, as
// floorDeploymentsQuery does: most live deployments belong to deleted apps,
// so starting from deployments would read the app of each of them.
const statusDeploymentsQuery = `with listed_apps as materialized (
		select a.id from apps a where a.status <> 'deleted'
	)
	select ` + deploymentSelectColumnsQualified + `
	  from listed_apps la
	  join deployments d on d.app_id = la.id
	 where d.status = any($1::text[])
	 order by d.created_at desc`

// ListDeploymentsWithStatus returns the deployments in one of statuses whose
// app is not deleted, newest first: ListAllDeployments narrowed to those
// statuses.
func (s *PgStore) ListDeploymentsWithStatus(ctx context.Context, statuses ...DeploymentStatus) ([]Deployment, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	values := make([]string, len(statuses))
	for i, status := range statuses {
		values[i] = string(status)
	}
	rows, err := s.pool.Query(ctx, statusDeploymentsQuery, values)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// ListDeploymentsWithStatus mirrors PgStore.ListDeploymentsWithStatus.
func (m *MemStore) ListDeploymentsWithStatus(_ context.Context, statuses ...DeploymentStatus) ([]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Deployment
	for _, d := range m.deployments {
		if !slices.Contains(statuses, d.Status) {
			continue
		}
		if a, ok := m.apps[d.AppID]; !ok || a.Status == AppDeleted {
			continue
		}
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// Every node's imaged ran the security and evidence sweeps over the whole
// fleet's serving deployments, so the CP read, and every rescan it started,
// was repeated once per node (bug hunt O32). This listing narrows them to the
// deployments of apps the node owns, plus apps whose owner is not active
// (draining, retired) so no deployment goes unswept while it waits to be
// reassigned. Those few are swept by every node, as before.
const statusDeploymentsForNodeQuery = `with listed_apps as materialized (
		select a.id from apps a
		 where a.status <> 'deleted'
		   and (a.node_id = $2::uuid
		        or not exists (select 1 from compute_nodes n where n.id = a.node_id and n.active))
	)
	select ` + deploymentSelectColumnsQualified + `
	  from listed_apps la
	  join deployments d on d.app_id = la.id
	 where d.status = any($1::text[])
	 order by d.created_at desc`

// ListDeploymentsWithStatusForNode is ListDeploymentsWithStatus narrowed to
// the apps nodeID owns and the apps whose owner is not an active node.
func (s *PgStore) ListDeploymentsWithStatusForNode(ctx context.Context, nodeID string, statuses ...DeploymentStatus) ([]Deployment, error) {
	if len(statuses) == 0 {
		return nil, nil
	}
	values := make([]string, len(statuses))
	for i, status := range statuses {
		values[i] = string(status)
	}
	rows, err := s.pool.Query(ctx, statusDeploymentsForNodeQuery, values, nodeID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanDeployments(rows)
}

// ListDeploymentsWithStatusForNode mirrors PgStore.ListDeploymentsWithStatusForNode.
func (m *MemStore) ListDeploymentsWithStatusForNode(ctx context.Context, nodeID string, statuses ...DeploymentStatus) ([]Deployment, error) {
	all, err := m.ListDeploymentsWithStatus(ctx, statuses...)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := all[:0]
	for _, d := range all {
		owner := m.apps[d.AppID].NodeID
		if owner == nodeID {
			out = append(out, d)
			continue
		}
		if n, ok := m.computeNodes[owner]; !ok || !n.Active {
			out = append(out, d)
		}
	}
	return out, nil
}
