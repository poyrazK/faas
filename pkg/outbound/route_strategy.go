package outbound

import (
	"hash/fnv"
	"math/rand/v2"
	"net/http"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
)

// How a route call's first target was picked (ADR-1434), as
// outbound_ai_route_calls_total labels it.
const (
	routePickOrdered  = "ordered"
	routePickWeighted = "weighted"
	routePickBest     = "best"
	routePickExplore  = "explore"
	routePickCanary   = "canary"
)

// routeTracksStats reports a route whose settled attempts add to its
// per-minute target rows: one with a canary (ADR-1340) or one whose
// strategy judges targets by them (ADR-1434).
func routeTracksStats(route AIRoute) bool {
	return route.Canary != nil || route.Strategy == api.OutboundAIRouteStrategyLatency || route.Strategy == api.OutboundAIRouteStrategyCost
}

// routeStrategyOrder returns the 1-based positions in the order the route's
// strategy tries them, and how the first was picked. intN draws a random
// number below n.
func routeStrategyOrder(route AIRoute, r *http.Request, intN func(int) int) ([]int, string) {
	ordered := make([]int, len(route.Targets))
	for index := range ordered {
		ordered[index] = index + 1
	}
	switch route.Strategy {
	case api.OutboundAIRouteStrategyWeighted:
		first, ok := weightedRouteTarget(route, r, intN)
		if !ok {
			return ordered, routePickOrdered
		}
		return withFirst(ordered, first), routePickWeighted
	case api.OutboundAIRouteStrategyLatency, api.OutboundAIRouteStrategyCost:
		return judgedRouteOrder(route, intN)
	}
	return ordered, routePickOrdered
}

// weightedRouteTarget draws a target by weight. A call naming a session, or
// else an end user, always draws the same target, so a conversation stays
// on one model.
func weightedRouteTarget(route AIRoute, r *http.Request, intN func(int) int) (int, bool) {
	if len(route.Weights) != len(route.Targets) {
		return 0, false
	}
	total := 0
	for _, weight := range route.Weights {
		total += max(weight, 0)
	}
	if total == 0 {
		return 0, false
	}
	key := r.Header.Get(api.OutboundAISessionHeader)
	if key == "" {
		key = r.Header.Get(api.OutboundAIUserHeader)
	}
	var draw int
	if key == "" {
		draw = intN(total)
	} else {
		hash := fnv.New64a()
		_, _ = hash.Write([]byte(route.ID + "\x00weighted\x00" + key))
		draw = int(hash.Sum64() % uint64(total))
	}
	for index, weight := range route.Weights {
		if draw < max(weight, 0) {
			return index + 1, true
		}
		draw -= max(weight, 0)
	}
	return 0, false
}

// judgedRouteOrder orders a latency or cost route from its window counts:
// targets with enough attempts and in health, best first; then targets
// without enough attempts, in route order; then unhealthy targets, in route
// order. A share of calls tries a target without data first, at random, so
// every target keeps current counts.
func judgedRouteOrder(route AIRoute, intN func(int) int) ([]int, string) {
	type judged struct {
		position int
		metric   int64
		slow     bool
	}
	var best []judged
	var unknown, unhealthy []int
	for index := range route.Targets {
		position := index + 1
		var counts AIRouteTargetCounts
		if index < len(route.Stats) {
			counts = route.Stats[index]
		}
		metric, known := routeTargetMetric(route, counts)
		switch {
		case counts.Calls < api.OutboundAIRouteStatsMinCalls:
			unknown = append(unknown, position)
		case 100*counts.Errors > api.OutboundAIRouteUnhealthyErrorPercent*counts.Calls:
			unhealthy = append(unhealthy, position)
		case !known:
			unknown = append(unknown, position)
		default:
			slow := route.MaxTTFBMS != nil && counts.TTFBCalls > 0 && counts.TTFBMSTotal > int64(*route.MaxTTFBMS)*counts.TTFBCalls
			best = append(best, judged{position: position, metric: metric, slow: slow})
		}
	}
	sort.SliceStable(best, func(i, j int) bool {
		if best[i].slow != best[j].slow {
			return !best[i].slow
		}
		return best[i].metric < best[j].metric
	})
	order := make([]int, 0, len(route.Targets))
	for _, target := range best {
		order = append(order, target.position)
	}
	order = append(order, unknown...)
	order = append(order, unhealthy...)
	if len(unknown) > 0 && intN(100) < api.OutboundAIRouteExplorePercent {
		return withFirst(order, unknown[intN(len(unknown))]), routePickExplore
	}
	if len(best) == 0 {
		return order, routePickOrdered
	}
	return order, routePickBest
}

// routeTargetMetric is what a latency route ranks by, the mean time to
// first byte in milliseconds, or what a cost route ranks by, the mean cost
// per successful attempt in millicents.
func routeTargetMetric(route AIRoute, counts AIRouteTargetCounts) (int64, bool) {
	if route.Strategy == api.OutboundAIRouteStrategyCost {
		successes := counts.Calls - counts.Errors
		if successes <= 0 {
			return 0, false
		}
		return counts.CostMillicents / successes, true
	}
	if counts.TTFBCalls <= 0 {
		return 0, false
	}
	return counts.TTFBMSTotal / counts.TTFBCalls, true
}

// withFirst moves position to the front and keeps the others in order.
func withFirst(order []int, position int) []int {
	out := make([]int, 0, len(order))
	out = append(out, position)
	for _, other := range order {
		if other != position {
			out = append(out, other)
		}
	}
	return out
}

// planRouteSteps orders one call's targets: the canary first when chosen
// (ADR-1340), then the strategy's order (ADR-1434). It returns how the first
// target was picked.
func planRouteSteps(route AIRoute, r *http.Request, canaryFirst bool, intN func(int) int) ([]routeStep, string) {
	order, pick := routeStrategyOrder(route, r, intN)
	if canaryFirst {
		order, pick = withFirst(order, route.Canary.Position), routePickCanary
	}
	steps := make([]routeStep, 0, len(order))
	for _, position := range order {
		steps = append(steps, routeStep{position: position, target: route.Targets[position-1]})
	}
	return steps, pick
}

// routeIntN is the handler's random draw for routing; tests replace it.
func (h *Handler) routeIntN(n int) int {
	if h.routeRandom != nil {
		return h.routeRandom(n)
	}
	return rand.IntN(n) //nolint:gosec // traffic splitting, not security.
}
