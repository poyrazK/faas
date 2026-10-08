package main

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

const serviceMapPromQLTimeout = 5 * time.Second

// getServiceMap serves GET /v1/service-map?range= (ADR-732). It projects the
// unsampled service-proxy edge series onto the account's apps. The map is a
// dark preview: without FAAS_SERVICE_MAP_ENABLED=1 it answers 503.
func (s *server) getServiceMap(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.serviceMapEnabled {
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
	resp, err := s.serviceMapFor(r.Context(), acct, rng)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list apps"))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// serviceMapFor builds the map for an already-validated range. Prometheus
// failures become a degraded Source, never an error; only the app listing
// can fail. The API and the dashboard share it so both show the same edges.
func (s *server) serviceMapFor(ctx context.Context, acct state.Account, rng string) (api.ServiceMapResponse, error) {
	apps, err := s.store.ListApps(ctx, acct.ID)
	if err != nil {
		return api.ServiceMapResponse{}, err
	}
	resp := api.ServiceMapResponse{Range: rng, AsOf: time.Now().UTC().Format(time.RFC3339Nano)}
	if s.promqlClient == nil {
		resp.Source = appmetrics.SourceDegradedPrefix + "prometheus not configured"
		return resp, nil
	}
	owned := serviceMapOwnedApps(apps)
	samples, err := fetchServiceMapSamples(ctx, s.promqlClient, owned, rng)
	if err != nil {
		s.logServiceMapDegraded(err)
		resp.Source = appmetrics.SourceDegradedPrefix + appmetrics.TelemetryDegradedReason(err)
		return resp, nil
	}
	resp.Nodes, resp.Edges, resp.Truncated = buildServiceMap(samples, owned, api.ServiceMapMaxEdges)
	resp.Source = appmetrics.SourcePrometheus
	return resp, nil
}

func (s *server) logServiceMapDegraded(err error) {
	if s.log == nil {
		return
	}
	// CodeQL log-injection guard, as in writeMetricsDegraded: the range
	// parameter flows into the query text that produced the error.
	msg := strings.ReplaceAll(strings.ReplaceAll(err.Error(), "\r", ""), "\n", "")
	s.log.Warn("apid: service-map query failed", "err", msg)
}

// serviceMapOwnedApps keys the account's apps by the canonical UUID string the
// gateway writes into caller_app and target_app labels.
func serviceMapOwnedApps(apps []state.App) map[string]state.App {
	owned := make(map[string]state.App, len(apps))
	for _, app := range apps {
		owned[canonicalServiceMapAppID(app.ID)] = app
	}
	return owned
}

func canonicalServiceMapAppID(id string) string {
	if parsed, err := uuid.Parse(strings.TrimSpace(id)); err == nil {
		return parsed.String()
	}
	return strings.ToLower(strings.TrimSpace(id))
}

type serviceMapEdgeKey struct {
	caller string
	target string
}

type serviceMapSamples struct {
	calls   map[serviceMapEdgeKey]float64
	errors  map[serviceMapEdgeKey]float64
	buckets map[serviceMapEdgeKey]map[string]float64
}

// fetchServiceMapSamples runs two bounded PromQL queries. Both constrain the
// caller and the target to the account's closed app set before Prometheus
// evaluates them; returned labels are checked against the same set again.
func fetchServiceMapSamples(ctx context.Context, client *promql.Client, owned map[string]state.App, window string) (serviceMapSamples, error) {
	out := serviceMapSamples{
		calls:   map[serviceMapEdgeKey]float64{},
		errors:  map[serviceMapEdgeKey]float64{},
		buckets: map[serviceMapEdgeKey]map[string]float64{},
	}
	if len(owned) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(owned))
	for id := range owned {
		ids = append(ids, id)
	}
	selector := appmetrics.LabelIDMatcher("caller_app", ids) + "," + appmetrics.LabelIDMatcher("target_app", ids)
	client = client.WithTimeout(serviceMapPromQLTimeout)

	rows, err := client.QueryVector(ctx, fmt.Sprintf(
		`sum by (caller_app, target_app, outcome)(increase(gateway_service_dependency_edge_calls_total{%s}[%s]))`,
		selector, window))
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		key, ok := serviceMapKey(row.Labels, owned)
		if !ok {
			continue
		}
		out.calls[key] += row.Value
		if row.Labels["outcome"] == "error" {
			out.errors[key] += row.Value
		}
	}

	rows, err = client.QueryVector(ctx, fmt.Sprintf(
		`sum by (caller_app, target_app, le)(increase(gateway_service_dependency_duration_seconds_bucket{%s,outcome="success"}[%s]))`,
		selector, window))
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		key, ok := serviceMapKey(row.Labels, owned)
		if !ok {
			continue
		}
		if out.buckets[key] == nil {
			out.buckets[key] = map[string]float64{}
		}
		out.buckets[key][row.Labels["le"]] += row.Value
	}
	return out, nil
}

func serviceMapKey(labels map[string]string, owned map[string]state.App) (serviceMapEdgeKey, bool) {
	key := serviceMapEdgeKey{
		caller: canonicalServiceMapAppID(labels["caller_app"]),
		target: canonicalServiceMapAppID(labels["target_app"]),
	}
	_, callerOK := owned[key.caller]
	_, targetOK := owned[key.target]
	return key, callerOK && targetOK
}

// buildServiceMap ranks edges by call volume, applies the cap, and derives
// nodes from the edges that survive it.
func buildServiceMap(samples serviceMapSamples, owned map[string]state.App, maxEdges int) ([]api.ServiceMapNode, []api.ServiceMapEdge, bool) {
	edges := make([]api.ServiceMapEdge, 0, len(samples.calls))
	for key, calls := range samples.calls {
		rounded := int64(appmetrics.SafeRoundNonNeg(calls) + 0.5)
		if rounded == 0 {
			continue
		}
		caller, target := owned[key.caller], owned[key.target]
		buckets := samples.buckets[key]
		edges = append(edges, api.ServiceMapEdge{
			CallerAppID:   caller.ID,
			CallerAppSlug: caller.Slug,
			TargetAppID:   target.ID,
			TargetAppSlug: target.Slug,
			Calls:         rounded,
			Errors:        int64(appmetrics.SafeRoundNonNeg(samples.errors[key]) + 0.5),
			ErrorRatePct:  appmetrics.SafePercent(percentOf(samples.errors[key], calls)),
			LatencyP50MS:  appmetrics.SafeFloat(histogramQuantile(0.50, buckets) * 1000),
			LatencyP95MS:  appmetrics.SafeFloat(histogramQuantile(0.95, buckets) * 1000),
		})
	}
	sort.Slice(edges, func(i, j int) bool {
		if edges[i].Calls != edges[j].Calls {
			return edges[i].Calls > edges[j].Calls
		}
		if edges[i].CallerAppSlug != edges[j].CallerAppSlug {
			return edges[i].CallerAppSlug < edges[j].CallerAppSlug
		}
		return edges[i].TargetAppSlug < edges[j].TargetAppSlug
	})
	truncated := len(edges) > maxEdges
	if truncated {
		edges = edges[:maxEdges]
	}

	seen := map[string]api.ServiceMapNode{}
	for _, edge := range edges {
		seen[edge.CallerAppID] = api.ServiceMapNode{AppID: edge.CallerAppID, AppSlug: edge.CallerAppSlug}
		seen[edge.TargetAppID] = api.ServiceMapNode{AppID: edge.TargetAppID, AppSlug: edge.TargetAppSlug}
	}
	nodes := make([]api.ServiceMapNode, 0, len(seen))
	for _, node := range seen {
		nodes = append(nodes, node)
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].AppSlug < nodes[j].AppSlug })
	return nodes, edges, truncated
}
