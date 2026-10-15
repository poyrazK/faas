package state

import (
	"context"
	"fmt"
)

// ParkedDeploymentLister reads every app's latest parked deployment for an
// account in one query. The app list used LatestParkedDeploymentForApp once
// per app; with 100 apps on a busy control plane the request deadline expired
// part-way and the rest of the list came back without its parked references
// (production-us hunt #10, H10-71).
type ParkedDeploymentLister interface {
	ListLatestParkedDeploymentPerApp(ctx context.Context, accountID string) (map[string]Deployment, error)
}

// ListLatestParkedDeploymentPerApp returns each of the account's non-deleted
// apps' most recently parked deployment, keyed by app ID. Apps with no parked
// deployment are absent. It matches LatestParkedDeploymentForApp per app.
func (s *PgStore) ListLatestParkedDeploymentPerApp(ctx context.Context, accountID string) (map[string]Deployment, error) {
	rows, err := s.pool.Query(ctx,
		`select distinct on (d.app_id) `+deploymentSelectColumnsQualified+`
		 from deployments d join apps a on a.id = d.app_id
		 where a.account_id = $1 and a.status <> 'deleted' and d.parked_reason is not null
		 order by d.app_id, d.parked_at desc nulls last, d.id desc`,
		accountID)
	if err != nil {
		return nil, fmt.Errorf("state: list latest parked deployments: %w", err)
	}
	defer rows.Close()
	deployments, err := scanDeployments(rows)
	if err != nil {
		return nil, fmt.Errorf("state: scan latest parked deployments: %w", err)
	}
	latest := make(map[string]Deployment, len(deployments))
	for _, deployment := range deployments {
		latest[deployment.AppID] = deployment
	}
	return latest, nil
}

// ListLatestParkedDeploymentPerApp mirrors the PgStore projection.
func (m *MemStore) ListLatestParkedDeploymentPerApp(_ context.Context, accountID string) (map[string]Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	owned := make(map[string]struct{})
	for _, app := range m.apps {
		if app.AccountID == accountID && app.Status != AppDeleted {
			owned[app.ID] = struct{}{}
		}
	}
	latest := make(map[string]Deployment)
	for _, d := range m.deployments {
		if d.ParkedReason == "" {
			continue
		}
		if _, ok := owned[d.AppID]; !ok {
			continue
		}
		current, ok := latest[d.AppID]
		if !ok || (d.ParkedAt != nil && (current.ParkedAt == nil || d.ParkedAt.After(*current.ParkedAt))) {
			latest[d.AppID] = d
		}
	}
	return latest, nil
}
