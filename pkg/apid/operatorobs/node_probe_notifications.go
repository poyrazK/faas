package operatorobs

import (
	"context"
	"errors"
	"math"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

const NodeProbeNotificationsScope = "node_serving_probe_notification_submissions"

// NodeProbeNotificationSelection can only be constructed from a validated
// node name and an empty query. No arbitrary Prometheus selector is accepted.
type NodeProbeNotificationSelection struct{ node string }

func NodeProbeNotificationsSelection(node, rawQuery string, forceQuery bool) (NodeProbeNotificationSelection, error) {
	if !ServingProbeNodeValid(node) || strings.Contains(node, "..") || rawQuery != "" || forceQuery {
		return NodeProbeNotificationSelection{}, errors.New("invalid node probe notification selection")
	}
	return NodeProbeNotificationSelection{node: node}, nil
}

func queryServingEvidence(ctx context.Context, client *promql.Client, queries []string, now time.Time) []servingProbeQuery {
	out := make([]servingProbeQuery, len(queries))
	var wg sync.WaitGroup
	for i, query := range queries {
		wg.Go(func() { out[i].rows, out[i].err = client.QueryVectorStrictAt(ctx, query, now) })
	}
	wg.Wait()
	return out
}

// NodeProbeNotifications reads the node directly, never filtering a truncated
// fleet preview. Inventory and channel reads share one deadline and snapshot.
func NodeProbeNotifications(ctx context.Context, client *promql.Client, selected NodeProbeNotificationSelection, now time.Time) api.ObsNodeProbeNotifications {
	ctx, cancel := context.WithTimeout(ctx, api.ObsServingProbeBudget)
	defer cancel()
	out := api.ObsNodeProbeNotifications{GeneratedAt: now.UTC(), Scope: NodeProbeNotificationsScope, Node: selected.node, State: "unknown", Reason: "inventory_unavailable", MaxAgeSeconds: int(api.ObsHealthFreshnessMaxAge.Seconds()), Inventory: api.ObsServingProbeInventory{State: "unavailable", Reason: "prometheus_not_configured"}, Channels: []api.ObsServingProbeNotification{}}
	if client == nil || client.BaseURL() == "" {
		out.Reason = "prometheus_not_configured"
		return out
	}
	queries := servingHealthQueries()[:2]
	results := queryServingEvidence(ctx, client, queries, now)
	input, err := servingProbeSnapshot(results[0], results[1], false)
	if err != nil {
		out.Inventory.Reason = "query_failed"
		return out
	}
	intent := input.intent(now)
	out.Inventory = intent.status
	if intent.status.State != "healthy" || intent.status.Configured == nil {
		return out
	}
	if !*intent.status.Configured {
		out.Reason = "inventory_not_configured"
		return out
	}
	expected := false
	out.Expected = &expected
	for key, policy := range intent.pairs {
		if key.node == selected.node {
			age := policy.age
			expected, out.MaxResultAgeSeconds = true, &age
			break
		}
	}
	if !expected {
		out.State, out.Reason = "not_applicable", "node_not_expected"
		return out
	}
	results = queryServingEvidence(ctx, client, servingNotificationQueriesForNode(selected.node), now)
	frames := nodeNotificationFrames(results, selected.node)
	for _, channel := range ServingProbeNotificationChannels() {
		out.Channels = append(out.Channels, assessServingProbeNotification(selected.node, channel, *out.MaxResultAgeSeconds, frames, now))
	}
	slices.SortFunc(out.Channels, compareServingProbeNotifications)
	out.State, out.Reason = notificationSummaryState(notificationCounts(out.Channels), len(out.Channels))
	return out
}

func nodeNotificationFrames(results []servingProbeQuery, node string) []notificationSamples {
	for i := range results {
		invalid := len(results[i].rows) > 2*api.ObsServingProbeNotificationNodeChannelsMax
		for _, row := range results[i].rows {
			invalid = invalid || row.Labels["probe_node"] != node
		}
		if invalid {
			results[i] = servingProbeQuery{err: errors.New("invalid scoped notification evidence")}
		}
	}
	return servingNotificationFrames(results)
}

func nodeNotificationInventoryValid(inv api.ObsServingProbeInventory, now time.Time) bool {
	if !slices.Contains(ServingHealthReasons(), inv.Reason) || inv.ObservedAt != nil && (inv.ObservedAt.IsZero() || inv.ObservedAt.After(now.Add(api.ObsHealthClockSkewTolerance))) {
		return false
	}
	if inv.State != "healthy" {
		return slices.Contains([]string{"unavailable", "stale"}, inv.State) && inv.Reason != "" && inv.Configured == nil && inv.ExpectedPairs == nil && (inv.State != "stale" || inv.ObservedAt != nil)
	}
	return inv.Reason == "" && inv.Configured != nil && inv.ExpectedPairs != nil && *inv.ExpectedPairs >= 0 && *inv.ExpectedPairs <= api.ObsServingProbePairsMax && (*inv.Configured || *inv.ExpectedPairs == 0) && notificationTimeCurrent(inv.ObservedAt, now)
}

// NodeProbeNotificationsValid validates a complete scoped read before it can
// be printed in human or JSON output by the node inspector.
func NodeProbeNotificationsValid(out api.ObsNodeProbeNotifications) bool {
	if out.GeneratedAt.IsZero() || out.Scope != NodeProbeNotificationsScope || !ServingProbeNodeValid(out.Node) || strings.Contains(out.Node, "..") || out.MaxAgeSeconds != int(api.ObsHealthFreshnessMaxAge.Seconds()) || out.Channels == nil || !nodeNotificationInventoryValid(out.Inventory, out.GeneratedAt) {
		return false
	}
	inv := out.Inventory
	if inv.State != "healthy" || inv.Configured == nil || !*inv.Configured {
		want := "inventory_unavailable"
		if inv.State == "healthy" {
			want = "inventory_not_configured"
		}
		return out.State == "unknown" && (out.Reason == want || out.Reason == "prometheus_not_configured" && inv.Reason == out.Reason) && out.Expected == nil && out.MaxResultAgeSeconds == nil && len(out.Channels) == 0
	}
	if out.Expected == nil {
		return false
	}
	if !*out.Expected {
		return out.State == "not_applicable" && out.Reason == "node_not_expected" && out.MaxResultAgeSeconds == nil && len(out.Channels) == 0
	}
	if *inv.ExpectedPairs == 0 || out.MaxResultAgeSeconds == nil || *out.MaxResultAgeSeconds <= 0 || math.IsNaN(*out.MaxResultAgeSeconds) || math.IsInf(*out.MaxResultAgeSeconds, 0) || len(out.Channels) != api.ObsServingProbeNotificationNodeChannelsMax {
		return false
	}
	seen := map[string]bool{}
	for i, item := range out.Channels {
		if item.Node != out.Node || seen[item.Channel] || !servingProbeNotificationValid(item, out.GeneratedAt, *out.MaxResultAgeSeconds) || i > 0 && compareServingProbeNotifications(out.Channels[i-1], item) >= 0 {
			return false
		}
		seen[item.Channel] = true
	}
	state, reason := notificationSummaryState(notificationCounts(out.Channels), len(out.Channels))
	return out.State == state && out.Reason == reason
}
