package consumerbilling

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// UsageBreakdown summarizes durable, unpriced usage over the same window
// as the caller's usage response. An empty route is unattributed traffic.
func UsageBreakdown(ctx context.Context, store state.Store, accountID, appID, consumerID string, since, until time.Time,
	buckets []state.APIConsumerUsageBucket) ([]api.APIConsumerRouteUsage, []api.APIConsumerMeterUsage, error) {
	if !until.After(since) {
		return nil, nil, nil
	}
	requests := make(map[time.Time]int64, len(buckets))
	for _, row := range buckets {
		requests[row.WindowStart.UTC()] = row.BillableUnits
	}
	routeTotals := map[string]int64{}
	if routes, ok := store.(state.ConsumerRouteUsageStore); ok {
		rows, err := routes.ListAPIConsumerRouteUsage(ctx, accountID, appID, consumerID, since, until)
		if err != nil {
			return nil, nil, err
		}
		for _, row := range rows {
			minute := row.WindowStart.UTC()
			billable := min(requests[minute], row.BillableUnits)
			if billable > 0 {
				routeTotals[row.Route] += billable
				requests[minute] -= billable
			}
		}
	}
	for _, remaining := range requests {
		if remaining > 0 {
			routeTotals[""] += remaining
		}
	}
	var byRoute []api.APIConsumerRouteUsage
	for route, units := range routeTotals {
		byRoute = append(byRoute, api.APIConsumerRouteUsage{Route: route, BillableRequests: units})
	}
	slices.SortFunc(byRoute, func(a, b api.APIConsumerRouteUsage) int {
		return cmp.Compare(a.Route, b.Route)
	})

	var byMeter []api.APIConsumerMeterUsage
	if meters, ok := store.(state.ConsumerMeterUsageStore); ok {
		rows, err := meters.ListAPIConsumerMeterUsage(ctx, accountID, appID, consumerID, "", since, until)
		if err != nil {
			return nil, nil, err
		}
		totals := map[string]int64{}
		for _, row := range rows {
			totals[row.Meter] += row.Units
		}
		for meter, units := range totals {
			byMeter = append(byMeter, api.APIConsumerMeterUsage{Meter: meter, Units: units})
		}
		slices.SortFunc(byMeter, func(a, b api.APIConsumerMeterUsage) int {
			return cmp.Compare(a.Meter, b.Meter)
		})
	}
	return byRoute, byMeter, nil
}
