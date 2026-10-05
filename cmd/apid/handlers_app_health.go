package main

import (
	"context"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apphealth"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/state"
)

// Read-only evidence projection. Ownership is resolved before any telemetry
// fetch. It never calls a scheduler, VM manager, or customer endpoint.
func (s *server) getAppHealth(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug")) //nolint:contextcheck // loadApp uses r.Context().
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), api.AppHealthCollectionTimeout)
	defer cancel()
	evidence := apphealth.Evidence{App: app, MetricsAllowed: acct.Plan.PerAppMetricsAllowed()}
	s.collectHealthDeployments(ctx, &evidence)
	s.collectHealthInstances(ctx, &evidence)
	if evidence.MetricsAllowed {
		evidence.Metrics, evidence.MetricsSource = appmetrics.FetchRequestHealth(ctx, s.promqlClient, app.ID)
	}
	evidence.Now = time.Now()
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, apphealth.Evaluate(evidence))
}

func (s *server) collectHealthDeployments(ctx context.Context, e *apphealth.Evidence) {
	live, err := s.store.LiveDeployments(ctx, e.App.ID)
	if err == nil {
		e.Live = live
		e.DeploymentsKnown = true
	}
	history, err := s.store.ListDeploymentsForApp(ctx, e.App.ID, api.AppHealthDeploymentHistoryLimit, 0)
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

func (s *server) collectHealthInstances(ctx context.Context, e *apphealth.Evidence) {
	lister, ok := s.store.(interface {
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
		node, err := s.store.ComputeNodeByID(ctx, i.NodeID)
		if err == nil {
			e.Nodes[i.NodeID] = node
		}
	}
	if reader, ok := s.store.(state.InstanceReadinessBySourceReader); ok {
		e.Readiness, _ = reader.LatestInstanceReadinessBySource(ctx, ids)
	}
}
