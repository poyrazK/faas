package profiling

import (
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// A common mix is evaluated only when every observed request route has CPU
// attribution on both sides and collection/request thresholds are satisfied.
func routeAdjustment(a, b api.ProfileResponse) *api.ProfileRouteAdjustment {
	out := &api.ProfileRouteAdjustment{Reason: "Route-adjusted CPU is unavailable: both windows need complete matching route attribution and request telemetry.", Weights: []api.ProfileRouteWeight{}}
	if a.Query.Route != "" || b.Query.Route != "" {
		out.Reason = "A common-mix comparison requires profiles for all routes."
		return out
	}
	o := api.DefaultProfileRegressionOptions()
	if !a.RouteRequestsComplete || !b.RouteRequestsComplete || !sufficientCoverage(a, o) || !sufficientCoverage(b, o) || len(a.Routes) == 0 || len(a.Routes) != len(b.Routes) {
		return out
	}
	left, right := map[string]api.ProfileRouteCPU{}, map[string]api.ProfileRouteCPU{}
	var totalA, totalB float64
	for i, rows := range [][]api.ProfileRouteCPU{a.Routes, b.Routes} {
		for _, r := range rows {
			if r.Route == api.ProfileUnattributedRoute || r.Requests == nil || *r.Requests < o.MinimumRequests || r.CPUSecondsPerRequest == nil || !finiteCPU(*r.CPUSecondsPerRequest) {
				out.Reason = "Unattributed CPU, sparse route traffic or missing request telemetry prevents request-mix adjustment."
				return out
			}
			if i == 0 {
				left[r.Route] = r
				totalA += float64(*r.Requests)
			} else {
				right[r.Route] = r
				totalB += float64(*r.Requests)
			}
		}
	}
	if totalA <= 0 || totalB <= 0 {
		return out
	}
	var adjustedA, adjustedB float64
	keys := make([]string, 0, len(left))
	for key := range left {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, route := range keys {
		if _, ok := right[route]; !ok {
			return out
		}
	}
	for _, route := range keys {
		x, y := left[route], right[route]
		if y.Requests == nil {
			return out
		}
		weight := (float64(*x.Requests)/totalA + float64(*y.Requests)/totalB) / 2
		adjustedA += weight * *x.CPUSecondsPerRequest
		adjustedB += weight * *y.CPUSecondsPerRequest
		out.Weights = append(out.Weights, api.ProfileRouteWeight{Route: route, Weight: weight})
	}
	delta := adjustedB - adjustedA
	out.Available = true
	out.BaselineCPUSecondsPerRequest = &adjustedA
	out.CandidateCPUSecondsPerRequest = &adjustedB
	out.DeltaCPUSecondsPerRequest = &delta
	out.Reason = "Both windows use the same route weights: the average of stable and canary request shares. This compares sampled route-associated CPU, not individual request execution or deployment causality."
	return out
}
