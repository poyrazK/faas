// Package ingressroute selects serving deployments for raw customer sessions.
package ingressroute

import (
	"context"
	"errors"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type DeploymentSource interface {
	LiveDeployments(context.Context, string) ([]state.Deployment, error)
}

// Deployment draws one traffic bucket per connection/peer session. Callers
// keep established sessions pinned and wake this exact bucket when it is cold.
func Deployment(ctx context.Context, source DeploymentSource, app string, draw uint64) (string, error) {
	rows, err := deploymentRows(ctx, source, app)
	if err != nil {
		return "", err
	}
	return Select(rows, app, draw)
}

// Security decisions cannot use the ordinary routing cache: a quarantine is
// authoritative even while an old instance is still running or being stopped.
func deploymentRows(ctx context.Context, source DeploymentSource, app string) ([]state.Deployment, error) {
	if fresh, ok := source.(interface {
		LiveDeploymentsForRawIngress(context.Context, string) ([]state.Deployment, error)
	}); ok {
		return fresh.LiveDeploymentsForRawIngress(ctx, app)
	}
	return source.LiveDeployments(ctx, app)
}

// RequireDeployment rechecks the bucket after selection or wake without drawing a new
// one. Ordinary weight changes do not move an already admitted connection.
func RequireDeployment(ctx context.Context, source DeploymentSource, app, deployment string) error {
	rows, err := deploymentRows(ctx, source, app)
	if err != nil {
		return err
	}
	found := false
	for _, row := range productionRows(rows, app) {
		if row.ParkedReason == string(state.ParkReasonSecurityScanRegressed) && (row.TrafficPercent > 0 || row.ID == deployment) {
			return errors.New("raw ingress deployment is security quarantined")
		}
		found = found || row.ID == deployment
	}
	if !found {
		return errors.New("raw ingress deployment is no longer live")
	}
	return ctx.Err()
}

func Select(rows []state.Deployment, app string, draw uint64) (string, error) {
	eligible := make([]state.Deployment, 0, len(rows))
	seen := make(map[string]bool)
	total := 0
	for _, row := range productionRows(rows, app) {
		if row.TrafficPercent == 0 {
			continue
		}
		if row.ParkedReason == string(state.ParkReasonSecurityScanRegressed) {
			return "", errors.New("raw ingress deployment is security quarantined")
		}
		if row.TrafficPercent < 0 || row.ID == "" || seen[row.ID] || row.TrafficPercent > api.DeploymentTrafficPercentTotal {
			return "", errors.New("invalid serving deployment weights")
		}
		seen[row.ID] = true
		eligible = append(eligible, row)
		total += row.TrafficPercent
		if total > api.DeploymentTrafficPercentTotal {
			return "", errors.New("serving deployment weights exceed total")
		}
	}
	if total != api.DeploymentTrafficPercentTotal {
		return "", errors.New("no serving deployment for raw ingress")
	}
	sort.Slice(eligible, func(i, j int) bool { return eligible[i].ID < eligible[j].ID })
	slot := int(draw % uint64(total))
	for _, row := range eligible {
		if slot < row.TrafficPercent {
			return row.ID, nil
		}
		slot -= row.TrafficPercent
	}
	return "", errors.New("no serving deployment bucket")
}

// Filter ownership and lifecycle before choosing a lane: another app or a
// superseded production row cannot displace this app's legacy default lane.
func productionRows(rows []state.Deployment, app string) []state.Deployment {
	live := make([]state.Deployment, 0, len(rows))
	for _, row := range rows {
		if row.AppID == app && row.Status == state.DeployLive {
			live = append(live, row)
		}
	}
	return state.ProductionRoutingDeployments(live)
}
