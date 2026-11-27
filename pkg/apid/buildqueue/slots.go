package buildqueue

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

// Slot sources for Input.SlotsState.
const (
	SlotsObserved      = "observed"
	SlotsUnavailable   = "unavailable"
	SlotsNotConfigured = "not_configured"
)

// SlotsQuery selects builderd's slot gauges (ADR-20261118154632 §8). Each
// builderd target carries the node label from metrics discovery.
const SlotsQuery = `{job="builderd",__name__=~"builderd_build_slots_(limit|reserved|in_use|active)"}`

// VectorQuerier is the slice of the Prometheus client the slot read uses.
type VectorQuerier interface {
	QueryVector(ctx context.Context, query string) ([]promql.VectorSample, error)
}

// ReadSlots reads builderd's slot gauges per node under the report's
// Prometheus budget. A missing client or a failed query leaves the report
// with the database's running counts only.
func ReadSlots(ctx context.Context, client *promql.Client) (map[string]api.ObsBuilderNodeSlots, string) {
	if client == nil || client.BaseURL() == "" {
		return nil, SlotsNotConfigured
	}
	return readSlots(ctx, client)
}

func readSlots(ctx context.Context, q VectorQuerier) (map[string]api.ObsBuilderNodeSlots, string) {
	ctx, cancel := context.WithTimeout(ctx, api.ObsBuildQueuePrometheusBudget)
	defer cancel()
	samples, err := q.QueryVector(ctx, SlotsQuery)
	if err != nil {
		return nil, SlotsUnavailable
	}
	return SlotsFromSamples(samples), SlotsObserved
}

// SlotsFromSamples folds the gauges into one row per node. Opportunistic is
// the reserved-slice builds beyond the reserved count: builderd labels a
// build only reserved or borrowed. A node reported by more than one target
// (two builderds, or a restarted one whose old series is still in the
// lookback) is marked Conflicting with no slot figures, not merged.
func SlotsFromSamples(samples []promql.VectorSample) map[string]api.ObsBuilderNodeSlots {
	byTarget := map[string]map[string]api.ObsBuilderNodeSlots{}
	for _, s := range samples {
		node := s.Labels["node"]
		if node == "" {
			continue
		}
		if byTarget[node] == nil {
			byTarget[node] = map[string]api.ObsBuilderNodeSlots{}
		}
		target := s.Labels["instance"]
		row, v := byTarget[node][target], int(s.Value)
		switch s.Labels["__name__"] {
		case "builderd_build_slots_limit":
			row.Limit = v
		case "builderd_build_slots_reserved":
			row.Reserved = v
		case "builderd_build_slots_in_use":
			row.InUse = v
		case "builderd_build_slots_active":
			switch s.Labels["kind"] {
			case "reserved":
				row.ActiveReserved = v
			case "borrowed":
				row.ActiveBorrowed = v
			}
		}
		byTarget[node][target] = row
	}
	out := make(map[string]api.ObsBuilderNodeSlots, len(byTarget))
	for node, targets := range byTarget {
		if len(targets) > 1 {
			out[node] = api.ObsBuilderNodeSlots{Conflicting: true}
			continue
		}
		for _, row := range targets {
			row.Opportunistic = max(0, row.ActiveReserved-row.Reserved)
			out[node] = row
		}
	}
	return out
}
