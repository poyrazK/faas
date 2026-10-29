package state

import (
	"context"
	"sort"
	"time"

	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// RouteMonitorIncidentAlert is one production route monitor incident a
// route_monitor_incidents alert counted.
type RouteMonitorIncidentAlert struct {
	ID, Status, Reason   string
	Method, Path, Signal string
	OpenedAt             time.Time
}

// RouteHealthHoldAlert is one canary a route_health_holds alert counted:
// held now (blocked_unknown, blocked_regressed) or aborted in the window.
type RouteHealthHoldAlert struct {
	DeploymentID, Status, Reason string
	UpdatedAt                    time.Time
}

// RouteAlertStore reads what the route_monitor_incidents and
// route_health_holds alert metrics count (ADR-20261028110044); meterd's
// evaluator type-asserts it.
type RouteAlertStore interface {
	// RouteMonitorIncidentsSince returns up to limit of the app's monitor
	// incidents open at any time at or after since, newest first, and how
	// many there are in all.
	RouteMonitorIncidentsSince(ctx context.Context, appID string, since time.Time, limit int) ([]RouteMonitorIncidentAlert, int64, error)
	// RouteHealthHoldsSince returns up to limit of the app's canaries the
	// route health guard holds now or aborted at or after since, newest
	// first, and how many there are in all.
	RouteHealthHoldsSince(ctx context.Context, appID string, since time.Time, limit int) ([]RouteHealthHoldAlert, int64, error)
}

func (s *PgStore) RouteMonitorIncidentsSince(ctx context.Context, appID string, since time.Time, limit int) ([]RouteMonitorIncidentAlert, int64, error) {
	rows, err := sqlc.New().RouteMonitorIncidentsSince(ctx, s.pool, sqlc.RouteMonitorIncidentsSinceParams{
		AppID: appID, Since: pgTimestamptzUTC(since), RowLimit: int32(limit),
	})
	if err != nil || len(rows) == 0 {
		return nil, 0, err
	}
	out := make([]RouteMonitorIncidentAlert, 0, len(rows))
	for _, r := range rows {
		out = append(out, RouteMonitorIncidentAlert{ID: r.ID, Status: r.Status, Reason: r.Reason, Method: r.Method, Path: r.Path,
			Signal: r.Signal, OpenedAt: r.OpenedAt.Time.UTC()})
	}
	return out, rows[0].Total, nil
}

func (s *PgStore) RouteHealthHoldsSince(ctx context.Context, appID string, since time.Time, limit int) ([]RouteHealthHoldAlert, int64, error) {
	rows, err := sqlc.New().RouteHealthHoldsSince(ctx, s.pool, sqlc.RouteHealthHoldsSinceParams{
		AppID: appID, Since: pgTimestamptzUTC(since), RowLimit: int32(limit),
	})
	if err != nil || len(rows) == 0 {
		return nil, 0, err
	}
	out := make([]RouteHealthHoldAlert, 0, len(rows))
	for _, r := range rows {
		out = append(out, RouteHealthHoldAlert{DeploymentID: r.DeploymentID, Status: r.Status, Reason: r.Reason, UpdatedAt: r.UpdatedAt.Time.UTC()})
	}
	return out, rows[0].Total, nil
}

// ObservedRouteLabels returns which of labels the app served at or after
// since (ADR-20261028110044).
func (s *PgStore) ObservedRouteLabels(ctx context.Context, accountID, appID string, since time.Time, labels []string) ([]string, error) {
	return sqlc.New().ObservedRouteLabels(ctx, s.pool, sqlc.ObservedRouteLabelsParams{
		AppID: appID, AccountID: accountID, Since: pgTimestamptzUTC(since), Labels: labels,
	})
}

// RouteMonitorIncidentsSince mirrors the Postgres read.
func (m *MemStore) RouteMonitorIncidentsSince(_ context.Context, appID string, since time.Time, limit int) ([]RouteMonitorIncidentAlert, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RouteMonitorIncidentAlert
	for _, i := range m.routeMonitorIncidents[appID] {
		if i.Status != "open" && (i.ClosedAt == nil || i.ClosedAt.Before(since)) {
			continue
		}
		alert := RouteMonitorIncidentAlert{ID: i.ID, Status: i.Status, Reason: i.OpeningReport.Reason, OpenedAt: i.OpenedAt.UTC()}
		if len(i.Evidence) > 0 {
			alert.Method, alert.Path, alert.Signal = i.Evidence[0].Method, i.Evidence[0].Path, i.Evidence[0].Signal
		}
		out = append(out, alert)
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].OpenedAt.Equal(out[b].OpenedAt) {
			return out[a].OpenedAt.After(out[b].OpenedAt)
		}
		return out[a].ID > out[b].ID
	})
	total := int64(len(out))
	return out[:min(limit, len(out))], total, nil
}

// RouteHealthHoldsSince mirrors the Postgres read without decision reasons.
func (m *MemStore) RouteHealthHoldsSince(_ context.Context, appID string, since time.Time, limit int) ([]RouteHealthHoldAlert, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []RouteHealthHoldAlert
	for id, st := range m.routeHealthNotifications {
		d, ok := m.deployments[id]
		if !ok || d.AppID != appID {
			continue
		}
		held := (st.Status == "blocked_unknown" || st.Status == "blocked_regressed") && d.Status == DeployLive && d.DeletedAt == nil &&
			d.CanaryTotalSteps > 0 && d.CanaryStep < d.CanaryTotalSteps
		if held || st.Status == "aborted" && !st.Since.Before(since) {
			out = append(out, RouteHealthHoldAlert{DeploymentID: id, Status: st.Status, UpdatedAt: st.Since})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.After(out[j].UpdatedAt)
		}
		return out[i].DeploymentID < out[j].DeploymentID
	})
	total := int64(len(out))
	return out[:min(limit, len(out))], total, nil
}
