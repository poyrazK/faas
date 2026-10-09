package profiling

import (
	"fmt"
	"math"
	"strconv"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
)

type routeReasonsKey struct{}

// Fixed counters only. No rejected label values enter storage or public views.
var attributionReasons = []string{"attributed", "unlabeled", "invalid_label", "route_not_admitted", "encoding_limit"}

func receivedRouteProfile(p *profile.Profile, identity sampleIdentity, reasons []string, reports ...*routeRequestMetadata) *profile.Profile {
	out := receivedProfile(p, identity)
	costs := map[string]float64{}
	for i, s := range p.Sample {
		reason := "unlabeled"
		if i < len(reasons) {
			reason = reasons[i]
		}
		costs[reason] += float64(s.Value[0]) / 1e9
	}
	name := out.Function[0].Name + ":attribution_v1"
	for _, reason := range attributionReasons {
		name += ":" + strconv.FormatFloat(costs[reason], 'g', -1, 64)
	}
	if len(reports) == 1 {
		name = appendRouteRequestMetadata(name, reports[0])
	}
	out.Function[0].Name = name
	return out
}

func parseAttributionCounters(parts []string, count int64, costs map[string]float64) error {
	if len(parts) == 5 {
		return nil
	} // Older capture, unknown discard reasons.
	if len(parts) == 13 {
		parts = parts[:11]
	}
	if len(parts) != 6+len(attributionReasons) || parts[5] != "attribution_v1" {
		return fmt.Errorf("invalid attribution diagnostics")
	}
	for i, reason := range attributionReasons {
		seconds, err := strconv.ParseFloat(parts[i+6], 64)
		value := seconds * float64(count)
		if err != nil || seconds < 0 || math.IsNaN(value) || math.IsInf(value, 0) || math.IsInf(costs[reason]+value, 0) {
			return fmt.Errorf("invalid attribution CPU counter")
		}
		costs[reason] += value
	}
	return nil
}

func attributionReasonRows(costs map[string]float64) []api.ProfileAttributionReason {
	rows := []api.ProfileAttributionReason{}
	for _, reason := range attributionReasons {
		if value, ok := costs[reason]; ok {
			rows = append(rows, api.ProfileAttributionReason{Reason: reason, CPUSeconds: value})
		}
	}
	return rows
}
