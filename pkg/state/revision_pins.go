package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RevisionPinStore resolves an exact client-selected deployment. A missing,
// disabled, expired, foreign, or non-live revision is never a fallback to the
// current deployment: the gateway must reject the request before waking.
type RevisionPinStore interface {
	ResolveRevisionPin(context.Context, string, string, string) (Deployment, error)
	ExpireRevisionPins(context.Context) (int64, error)
}

func (s *PgStore) ResolveRevisionPin(ctx context.Context, appID, scope, deploymentID string) (Deployment, error) {
	var manifestJSON []byte
	if err := s.pool.QueryRow(ctx, `select coalesce(manifest, '{}'::jsonb) from apps where id = $1 and status <> 'deleted'`, appID).Scan(&manifestJSON); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Deployment{}, ErrNotFound
		}
		return Deployment{}, fmt.Errorf("state: resolve revision pin app: %w", err)
	}
	var manifest AppManifest
	if err := json.Unmarshal(manifestJSON, &manifest); err != nil {
		return Deployment{}, fmt.Errorf("state: resolve revision pin manifest: %w", err)
	}
	if manifest.RevisionPinTTLSeconds <= 0 {
		return Deployment{}, ErrNotFound
	}
	dep, err := scanDeploymentWithRootfs(s.pool.QueryRow(ctx,
		`select `+deploymentSelectColumnsWithRootfs+`
		   from deployments d
		  where d.id = $1 and d.app_id = $2 and d.scope = $3 and d.status = 'live'
		    and (d.traffic_percent > 0 or exists (
		      select 1 from deployment_revision_pins p
		       where p.deployment_id = d.id and p.expires_at > now()))`,
		deploymentID, appID, normalizedDeploymentScope(scope)))
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, ErrNotFound) {
		return Deployment{}, ErrNotFound
	}
	if err != nil {
		return Deployment{}, fmt.Errorf("state: resolve revision pin deployment: %w", err)
	}
	return dep, nil
}

// ExpireRevisionPins removes the right to reach old code and makes its
// deployment non-live atomically. App locks serialize admission and cutover;
// the expiry statement rechecks retention after acquiring those locks.
func (s *PgStore) ExpireRevisionPins(ctx context.Context) (int64, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return 0, fmt.Errorf("state: expire revision pins begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	apps, err := q.LockExpiredRevisionPinApps(ctx, tx, api.RevisionPinCleanupPageMax)
	if err != nil {
		return 0, fmt.Errorf("state: expire revision pins lock apps: %w", err)
	}
	count, err := q.ExpireRetainedDeploymentRevisionPins(ctx, tx, sqlc.ExpireRetainedDeploymentRevisionPinsParams{
		AppIds: apps, PageLimit: api.RevisionPinCleanupPageMax})
	if err != nil {
		return 0, fmt.Errorf("state: expire revision pins: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("state: expire revision pins commit: %w", err)
	}
	return count, nil
}

func (m *MemStore) ResolveRevisionPin(_ context.Context, appID, scope, deploymentID string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.resolveRevisionPinLocked(appID, scope, deploymentID)
}

func (m *MemStore) resolveRevisionPinLocked(appID, scope, deploymentID string) (Deployment, error) {
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted || app.DeletedAt != nil || app.Manifest.RevisionPinTTLSeconds <= 0 {
		return Deployment{}, ErrNotFound
	}
	dep, ok := m.deployments[deploymentID]
	if !ok || dep.AppID != appID || normalizedDeploymentScope(dep.Scope) != normalizedDeploymentScope(scope) || dep.Status != DeployLive || dep.DeletedAt != nil {
		return Deployment{}, ErrNotFound
	}
	if dep.TrafficPercent == 0 {
		expires, ok := m.revisionPins[deploymentID]
		if !ok || !time.Now().Before(expires) {
			return Deployment{}, ErrNotFound
		}
	}
	return dep, nil
}

func (m *MemStore) ExpireRevisionPins(_ context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var expired int64
	pruned := 0
	ids := make(map[string]struct{}, len(m.revisionPins)+len(m.operationCodePins))
	for id := range m.revisionPins {
		ids[id] = struct{}{}
	}
	for id := range m.operationCodePins {
		ids[id] = struct{}{}
	}
	now := time.Now()
	for id := range ids {
		if m.revisionPins[id].After(now) || m.operationCodePins[id].After(now) ||
			m.operationRetainsDeploymentLocked(id) || m.deploymentInUsableReleaseLocked(id) {
			continue
		}
		if pruned >= api.RevisionPinCleanupPageMax {
			break
		}
		delete(m.revisionPins, id)
		delete(m.operationCodePins, id)
		pruned++
		if dep, exists := m.deployments[id]; exists && dep.Status == DeployLive && dep.TrafficPercent == 0 {
			dep.Status = DeploySuperseded
			m.deployments[id] = dep
			expired++
		}
	}
	return expired, nil
}
