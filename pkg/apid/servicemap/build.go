package servicemap

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

const promQLTimeout = 5 * time.Second

// ownedApps keys the account's apps by the canonical UUID string the gateway
// writes into caller_app and target_app labels.
func ownedApps(apps []state.App) map[string]state.App {
	owned := make(map[string]state.App, len(apps))
	for _, app := range apps {
		owned[CanonicalAppID(app.ID)] = app
	}
	return owned
}

// CanonicalAppID is the form the gateway labels edges with (uuid.UUID.String,
// dashed); the store hands out 32-hex IDs.
func CanonicalAppID(id string) string {
	if parsed, err := uuid.Parse(strings.TrimSpace(id)); err == nil {
		return parsed.String()
	}
	return strings.ToLower(strings.TrimSpace(id))
}

type edgeKey struct {
	caller string
	target string
}

type samples struct {
	calls   map[edgeKey]float64
	errors  map[edgeKey]float64
	wakes   map[edgeKey]float64
	ahead   map[edgeKey]map[string]float64 // wake-ahead outcome -> count
	buckets map[edgeKey]map[string]float64
	// Private TCP sessions (ADR-576) on the same caller -> target pairs.
	tcpSessions map[edgeKey]float64
	tcpErrors   map[edgeKey]float64
	tcpWakes    map[edgeKey]float64
}

// fetchSamples runs six bounded PromQL queries. Each constrains the caller and
// the target to the account's closed app set before Prometheus evaluates it;
// returned labels are checked against the same set again.
func fetchSamples(ctx context.Context, client *promql.Client, owned map[string]state.App, window string) (samples, error) {
	out := samples{
		calls:   map[edgeKey]float64{},
		errors:  map[edgeKey]float64{},
		wakes:   map[edgeKey]float64{},
		ahead:   map[edgeKey]map[string]float64{},
		buckets: map[edgeKey]map[string]float64{},

		tcpSessions: map[edgeKey]float64{},
		tcpErrors:   map[edgeKey]float64{},
		tcpWakes:    map[edgeKey]float64{},
	}
	if len(owned) == 0 {
		return out, nil
	}
	ids := make([]string, 0, len(owned))
	for id := range owned {
		ids = append(ids, id)
	}
	selector := appmetrics.LabelIDMatcher("caller_app", ids) + "," + appmetrics.LabelIDMatcher("target_app", ids)
	client = client.WithTimeout(promQLTimeout)

	rows, err := client.QueryVector(ctx, fmt.Sprintf(
		`sum by (caller_app, target_app, outcome)(increase(gateway_service_dependency_edge_calls_total{%s}[%s]))`,
		selector, window))
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		key, ok := ownedKey(row.Labels, owned)
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
		key, ok := ownedKey(row.Labels, owned)
		if !ok {
			continue
		}
		if out.buckets[key] == nil {
			out.buckets[key] = map[string]float64{}
		}
		out.buckets[key][row.Labels["le"]] += row.Value
	}

	// Gateways that predate the wake counter emit no series; the edges then
	// report zero cold calls rather than failing the map.
	rows, err = client.QueryVector(ctx, fmt.Sprintf(
		`sum by (caller_app, target_app)(increase(gateway_service_dependency_edge_wakes_total{%s}[%s]))`,
		selector, window))
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		if key, ok := ownedKey(row.Labels, owned); ok {
			out.wakes[key] += row.Value
		}
	}

	rows, err = client.QueryVector(ctx, fmt.Sprintf(
		`sum by (caller_app, target_app, outcome)(increase(gateway_service_wake_ahead_edge_total{%s}[%s]))`,
		selector, window))
	if err != nil {
		return out, err
	}
	for _, row := range rows {
		key, ok := ownedKey(row.Labels, owned)
		if !ok {
			continue
		}
		if out.ahead[key] == nil {
			out.ahead[key] = map[string]float64{}
		}
		out.ahead[key][row.Labels["outcome"]] += row.Value
	}
	return out, fetchTCPSamples(ctx, client, selector, window, owned, &out)
}

// fetchTCPSamples adds private TCP sessions; like the wake counter, gateways
// that predate it emit no series and the edges report zero sessions.
func fetchTCPSamples(ctx context.Context, client *promql.Client, selector, window string, owned map[string]state.App, out *samples) error {
	rows, err := client.QueryVector(ctx, fmt.Sprintf(
		`sum by (caller_app, target_app, outcome)(increase(gateway_service_tcp_edge_sessions_total{%s}[%s]))`,
		selector, window))
	if err != nil {
		return err
	}
	for _, row := range rows {
		key, ok := ownedKey(row.Labels, owned)
		if !ok {
			continue
		}
		out.tcpSessions[key] += row.Value
		if row.Labels["outcome"] == "error" {
			out.tcpErrors[key] += row.Value
		}
	}
	rows, err = client.QueryVector(ctx, fmt.Sprintf(
		`sum by (caller_app, target_app)(increase(gateway_service_tcp_edge_wakes_total{%s}[%s]))`,
		selector, window))
	if err != nil {
		return err
	}
	for _, row := range rows {
		if key, ok := ownedKey(row.Labels, owned); ok {
			out.tcpWakes[key] += row.Value
		}
	}
	return nil
}

func ownedKey(labels map[string]string, owned map[string]state.App) (edgeKey, bool) {
	key := edgeKey{
		caller: CanonicalAppID(labels["caller_app"]),
		target: CanonicalAppID(labels["target_app"]),
	}
	_, callerOK := owned[key.caller]
	_, targetOK := owned[key.target]
	return key, callerOK && targetOK
}

// build ranks edges by HTTP calls plus TCP sessions, applies the cap, and
// derives nodes from the edges that survive it. A pair with only TCP traffic
// is an edge too.
func build(in samples, owned map[string]state.App, maxEdges int) ([]api.ServiceMapNode, []api.ServiceMapEdge, bool) {
	keys := make(map[edgeKey]struct{}, len(in.calls)+len(in.tcpSessions))
	for key := range in.calls {
		keys[key] = struct{}{}
	}
	for key := range in.tcpSessions {
		keys[key] = struct{}{}
	}
	edges := make([]api.ServiceMapEdge, 0, len(keys))
	for key := range keys {
		calls := in.calls[key]
		rounded := count(calls)
		sessions := count(in.tcpSessions[key])
		if rounded == 0 && sessions == 0 {
			continue
		}
		caller, target := owned[key.caller], owned[key.target]
		buckets := in.buckets[key]
		// Each pair of counters is scraped independently, so a part can
		// briefly exceed its whole.
		cold := min(count(in.wakes[key]), rounded)
		edges = append(edges, api.ServiceMapEdge{
			CallerAppID:      caller.ID,
			CallerAppSlug:    caller.Slug,
			TargetAppID:      target.ID,
			TargetAppSlug:    target.Slug,
			Calls:            rounded,
			Errors:           count(in.errors[key]),
			ErrorRatePct:     appmetrics.SafePercent(percentOf(in.errors[key], calls)),
			LatencyP50MS:     appmetrics.SafeFloat(appmetrics.HistogramQuantile(0.50, buckets) * 1000),
			LatencyP95MS:     appmetrics.SafeFloat(appmetrics.HistogramQuantile(0.95, buckets) * 1000),
			ColdCalls:        cold,
			ColdCallPct:      appmetrics.SafePercent(percentOf(float64(cold), float64(rounded))),
			WakeAheadStarted: count(in.ahead[key]["started"]),
			WakeAheadUsed:    count(in.ahead[key]["used"]),
			TCPSessions:      sessions,
			TCPErrors:        min(count(in.tcpErrors[key]), sessions),
			TCPColdSessions:  min(count(in.tcpWakes[key]), sessions),
		})
	}
	sort.Slice(edges, func(i, j int) bool {
		if vi, vj := edges[i].Calls+edges[i].TCPSessions, edges[j].Calls+edges[j].TCPSessions; vi != vj {
			return vi > vj
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

// suggestWakeAhead applies the ADR-977 evidence bar to one edge: enough cold
// calls to have taught wake-ahead the edge, and most of the edge's calls cold.
// HTTP calls and private TCP sessions both teach wake-ahead (ADR-1134), so
// they count together.
func suggestWakeAhead(edge api.ServiceMapEdge, callerOptedIn bool) bool {
	cold := edge.ColdCalls + edge.TCPColdSessions
	total := edge.Calls + edge.TCPSessions
	return !callerOptedIn &&
		cold >= api.ServiceWakeAheadMinWakes &&
		float64(cold) >= api.ServiceWakeAheadMinColdShare*float64(total)
}

// count rounds an increase() sample to a whole, non-negative count.
func count(v float64) int64 {
	return int64(appmetrics.SafeRoundNonNeg(v) + 0.5)
}

func percentOf(numerator, denominator float64) float64 {
	if numerator <= 0 || denominator <= 0 {
		return 0
	}
	return numerator / denominator * 100
}
