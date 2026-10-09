package apphealth

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// EvidenceStore reads recorded platform evidence. It cannot wake a workload.
type EvidenceStore interface {
	LiveDeployments(context.Context, string) ([]state.Deployment, error)
	ListDeploymentsForApp(context.Context, string, int, int) ([]state.Deployment, error)
	ComputeNodeByID(context.Context, string) (state.ComputeNode, error)
}

// Collect is shared by on-demand reads and the independent history collector.
func Collect(ctx context.Context, store EvidenceStore, client *promql.Client, app state.App, metricsAllowed bool) api.AppHealthResponse {
	e := Evidence{App: app, MetricsAllowed: metricsAllowed}
	collectDeployments(ctx, store, &e)
	collectInstances(ctx, store, &e)
	if e.MetricsAllowed && e.DeploymentsKnown {
		var ids []string
		for _, d := range servingDeployments(e.Live) {
			ids = append(ids, d.ID)
		}
		e.Metrics = appmetrics.FetchRequestHealth(ctx, client, app.ID, ids)
	}
	e.Now = time.Now().UTC()
	return Evaluate(e)
}

func collectDeployments(ctx context.Context, store EvidenceStore, e *Evidence) {
	live, err := store.LiveDeployments(ctx, e.App.ID)
	if err == nil {
		e.Live, e.DeploymentsKnown = live, true
	}
	history, err := store.ListDeploymentsForApp(ctx, e.App.ID, api.AppHealthDeploymentHistoryLimit, 0)
	if err != nil {
		return
	}
	e.HistoryComplete = len(history) < api.AppHealthDeploymentHistoryLimit
	for _, d := range history {
		if d.Scope == "" || d.Scope == "default" {
			e.Latest = &d
			break
		}
	}
}

func collectInstances(ctx context.Context, store EvidenceStore, e *Evidence) {
	lister, ok := store.(interface {
		ListActiveInstancesForApp(context.Context, string, int) ([]state.Instance, error)
	})
	if !ok {
		return
	}
	instances, err := lister.ListActiveInstancesForApp(ctx, e.App.ID, api.AppHealthInstanceLimit+1)
	if err != nil || len(instances) > api.AppHealthInstanceLimit {
		return
	}
	e.Instances, e.InstancesKnown = instances, true
	ids := make([]string, 0, len(instances))
	e.Nodes = make(map[string]state.ComputeNode)
	seen := make(map[string]bool)
	for _, i := range instances {
		ids = append(ids, i.ID)
		if seen[i.NodeID] || i.NodeID == "" {
			continue
		}
		seen[i.NodeID] = true
		node, err := store.ComputeNodeByID(ctx, i.NodeID)
		if err == nil {
			e.Nodes[i.NodeID] = node
		}
	}
	if reader, ok := store.(state.InstanceReadinessBySourceReader); ok {
		e.Readiness, _ = reader.LatestInstanceReadinessBySource(ctx, ids)
	}
}
