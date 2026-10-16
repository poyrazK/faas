// Package servicemap is apid's account service map (ADR-1131): which of an
// account's apps call which, with call volume, error rate and success latency
// per edge, projected from the unsampled service-proxy edge series that
// gatewayd-internal already records (ADR-288). It lives outside apidserver per
// ADR-982; apidserver only wires the routes and implements Host.
package servicemap

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

// Host is what the service map needs from apid. Accessors are read at call
// time so tests and late wiring (WithStatusCache) take effect.
type Host interface {
	// Apps lists every app the account owns.
	Apps(ctx context.Context, accountID string) ([]state.App, error)
	// PromQL is nil when apid runs without a Prometheus URL.
	PromQL() *promql.Client
	Logger() *slog.Logger
	// WakeAheadEnabled reports an app's ADR-977 opt-in.
	WakeAheadEnabled(ctx context.Context, appID string) (bool, error)
}

// Service serves the map. Enabled is the FAAS_SERVICE_MAP_ENABLED operator
// gate; while it is off the API answers 503 and the dashboard says so.
type Service struct {
	Host    Host
	Enabled bool
}

// Get serves GET /v1/service-map?range=.
func (s *Service) Get(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.Enabled {
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, "service_map_unavailable",
			"Service map unavailable", "the service map preview is not enabled"))
		return
	}
	if !acct.Plan.PerAppMetricsAllowed() {
		api.WriteProblem(w, api.ErrPlanPerAppMetricsNotAllowed(acct.Plan))
		return
	}
	rng := r.URL.Query().Get("range")
	if rng == "" {
		rng = api.ServiceMapDefaultRange
	}
	if !appmetrics.IsValidRange(rng) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "invalid range",
			fmt.Sprintf("range must be one of: %s", strings.Join(appmetrics.Ranges(), ", "))))
		return
	}
	resp, err := s.For(r.Context(), acct, rng)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list apps"))
		return
	}
	httpkit.WriteJSON(w, http.StatusOK, resp)
}

// For builds the map for an already-validated range. Prometheus failures
// become a degraded Source, never an error; only the app listing can fail.
// The API and the dashboard share it so both show the same edges.
func (s *Service) For(ctx context.Context, acct state.Account, rng string) (api.ServiceMapResponse, error) {
	apps, err := s.Host.Apps(ctx, acct.ID)
	if err != nil {
		return api.ServiceMapResponse{}, err
	}
	resp := api.ServiceMapResponse{Range: rng, AsOf: time.Now().UTC().Format(time.RFC3339Nano)}
	client := s.Host.PromQL()
	if client == nil {
		resp.Source = appmetrics.SourceDegradedPrefix + "prometheus not configured"
		return resp, nil
	}
	owned := ownedApps(apps)
	samples, err := fetchSamples(ctx, client, owned, rng)
	if err != nil {
		s.logDegraded(err)
		resp.Source = appmetrics.SourceDegradedPrefix + appmetrics.TelemetryDegradedReason(err)
		return resp, nil
	}
	resp.Nodes, resp.Edges, resp.Truncated = build(samples, owned, api.ServiceMapMaxEdges)
	s.markWakeAhead(ctx, resp.Nodes, resp.Edges)
	resp.Source = appmetrics.SourcePrometheus
	return resp, nil
}

// markWakeAhead fills each node's opt-in and each edge's suggestion. A failed
// lookup leaves that app unmarked and unsuggested rather than failing the map.
func (s *Service) markWakeAhead(ctx context.Context, nodes []api.ServiceMapNode, edges []api.ServiceMapEdge) {
	known := make(map[string]bool, len(nodes))
	for i := range nodes {
		enabled, err := s.Host.WakeAheadEnabled(ctx, nodes[i].AppID)
		if err != nil {
			if log := s.Host.Logger(); log != nil {
				log.Warn("apid: service-map wake-ahead lookup failed", "app_id", nodes[i].AppID, "err", err)
			}
			continue
		}
		nodes[i].WakeAheadEnabled = enabled
		known[nodes[i].AppID] = true
	}
	optedIn := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		optedIn[node.AppID] = node.WakeAheadEnabled
	}
	for i := range edges {
		if known[edges[i].CallerAppID] {
			edges[i].SuggestWakeAhead = suggestWakeAhead(edges[i], optedIn[edges[i].CallerAppID])
		}
	}
}

// DashboardData is the /dashboard/service-map view model. An invalid ?range=
// falls back to the default instead of failing the page.
func (s *Service) DashboardData(r *http.Request, acct state.Account) (dashboard.ServiceMapData, error) {
	data := dashboard.ServiceMapData{
		Enabled:          s.Enabled,
		PlanAllowed:      acct.Plan.PerAppMetricsAllowed(),
		Range:            api.ServiceMapDefaultRange,
		Ranges:           appmetrics.Ranges(),
		HighErrorRatePct: api.ServiceMapHighErrorRatePct,
	}
	if !data.Enabled || !data.PlanAllowed {
		return data, nil
	}
	if rng := r.URL.Query().Get("range"); appmetrics.IsValidRange(rng) {
		data.Range = rng
	}
	m, err := s.For(r.Context(), acct, data.Range)
	if err != nil {
		return data, err
	}
	if reason, degraded := strings.CutPrefix(m.Source, appmetrics.SourceDegradedPrefix); degraded {
		data.Degraded = reason
	}
	data.Map = &m
	return data, nil
}

func (s *Service) logDegraded(err error) {
	log := s.Host.Logger()
	if log == nil {
		return
	}
	// Log-injection guard, as in the per-app metrics handler: the range
	// parameter flows into the query text that produced the error.
	msg := strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\r", ""), "\n", "")
	log.Warn("apid: service-map query failed", "err", msg)
}
