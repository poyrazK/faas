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
	rows, err := source.LiveDeployments(ctx, app)
	if err != nil {
		return "", err
	}
	return Select(rows, app, draw)
}

func Select(rows []state.Deployment, app string, draw uint64) (string, error) {
	eligible := make([]state.Deployment, 0, len(rows))
	seen := make(map[string]bool)
	total := 0
	for _, row := range rows {
		if row.AppID != app || row.Status != state.DeployLive || row.TrafficPercent == 0 {
			continue
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
