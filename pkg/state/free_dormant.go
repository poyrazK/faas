package state

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// DormantFreeAppLister lists the apps the Free-tier disk reaper (spec §4.3,
// ADR-20261105120000) drops snapshots for: every possible billing subject on
// Free, a snapshot on disk, no live instance, and no request, start, park or
// deployment since cutoff. At most limit apps are returned, ordered by id.
type DormantFreeAppLister interface {
	ListDormantFreeSnapshotApps(ctx context.Context, cutoff time.Time, limit int) ([]string, error)
}

var (
	_ DormantFreeAppLister = (*PgStore)(nil)
	_ DormantFreeAppLister = (*MemStore)(nil)
)

func (s *PgStore) ListDormantFreeSnapshotApps(ctx context.Context, cutoff time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	ids, err := sqlc.New().ListDormantFreeSnapshotApps(ctx, s.pool, sqlc.ListDormantFreeSnapshotAppsParams{
		Cutoff:  pgtype.Timestamptz{Time: cutoff, Valid: true},
		MaxApps: int32(min(limit, 1<<30)),
	})
	if err != nil {
		return nil, fmt.Errorf("state: list dormant free apps: %w", err)
	}
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, uuidString(id))
	}
	return out, nil
}

// ListDormantFreeSnapshotApps mirrors the Postgres query.
func (m *MemStore) ListDormantFreeSnapshotApps(_ context.Context, cutoff time.Time, limit int) ([]string, error) {
	if limit <= 0 {
		return nil, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	withSnapshot := map[string]bool{}
	for _, snap := range m.snapshots {
		if snap.DeletePending {
			continue
		}
		if dep, ok := m.deployments[snap.DeploymentID]; ok {
			withSnapshot[dep.AppID] = true
		}
	}
	recent := map[string]bool{}
	for _, dep := range m.deployments {
		if !dep.CreatedAt.Before(cutoff) {
			recent[dep.AppID] = true
		}
	}
	for _, ins := range m.instances {
		switch ins.State {
		case string(StateParked), string(StateStopped), string(StateFailed):
		default:
			recent[ins.AppID] = true
			continue
		}
		for _, at := range []time.Time{ins.LastRequestAt, ins.StartedAt, ins.ParkedAt} {
			if !at.IsZero() && !at.Before(cutoff) {
				recent[ins.AppID] = true
			}
		}
	}
	var out []string
	for id, app := range m.apps {
		if app.Status != AppActive && app.Status != AppEvictedCold && app.Status != "" {
			continue
		}
		if !withSnapshot[id] || recent[id] || !app.CreatedAt.Before(cutoff) {
			continue
		}
		if acct, ok := m.accounts[app.AccountID]; !ok || acct.Plan != api.PlanFree {
			continue
		}
		if org, ok := m.orgs[app.OrgID]; ok && !org.Personal && org.Plan != api.PlanFree {
			continue
		}
		out = append(out, id)
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
