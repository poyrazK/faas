package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
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
// deployment non-live in one statement. A concurrent cutover holds the app
// lock and inserts only fresh deadlines, so already-expired rows cannot be
// resurrected by a later replacement.
func (s *PgStore) ExpireRevisionPins(ctx context.Context) (int64, error) {
	tag, err := s.pool.Exec(ctx, `with expired as (
		delete from deployment_revision_pins p
		 where p.expires_at <= now()
		   and not exists (
		     select 1 from project_release_members rm
		     join project_release_sets rs on rs.id = rm.release_id
		     where rm.deployment_id = p.deployment_id
		       and (rs.active or rs.expires_at > now())
		   )
		 returning p.deployment_id
	)
	update deployments d set status = 'superseded', traffic_percent = 0
	  from expired e
	 where d.id = e.deployment_id and d.status = 'live' and d.traffic_percent = 0`)
	if err != nil {
		return 0, fmt.Errorf("state: expire revision pins: %w", err)
	}
	return tag.RowsAffected(), nil
}

func (m *MemStore) ResolveRevisionPin(_ context.Context, appID, scope, deploymentID string) (Deployment, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted || app.Manifest.RevisionPinTTLSeconds <= 0 {
		return Deployment{}, ErrNotFound
	}
	dep, ok := m.deployments[deploymentID]
	if !ok || dep.AppID != appID || normalizedDeploymentScope(dep.Scope) != normalizedDeploymentScope(scope) || dep.Status != DeployLive {
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
	for id, until := range m.revisionPins {
		if time.Now().Before(until) {
			continue
		}
		if m.deploymentInUsableReleaseLocked(id) {
			continue
		}
		delete(m.revisionPins, id)
		dep := m.deployments[id]
		if dep.Status == DeployLive && dep.TrafficPercent == 0 {
			dep.Status = DeploySuperseded
			m.deployments[id] = dep
			expired++
		}
	}
	return expired, nil
}
