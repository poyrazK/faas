package main

import (
	"math"
	"math/big"

	"github.com/onebox-faas/faas/pkg/api"
)

// allocateRequestAnalyticsRouteCost distributes an estimated compute value
// across the bounded route rows by observed request share. The route query
// adds an __other__ group whenever its top-N limit omits routes; that count is
// passed separately so the public route rows stay actual route/method pairs.
// Any integer-millicent rounding dust goes to the rows with the largest
// fractional remainders, keeping the total exactly reconciled.
func allocateRequestAnalyticsRouteCost(routes []api.RequestAnalyticsRoute, otherRouteRequests, totalMillicents int64) (requestCount, allocatedMillicents, otherRouteMillicents int64) {
	for i := range routes {
		routes[i].EstimatedComputeCostMillicents = 0
		routes[i].RequestSharePct = 0
		if routes[i].Requests > 0 {
			if routes[i].Requests > math.MaxInt64-requestCount {
				return 0, 0, 0
			}
			requestCount += routes[i].Requests
		}
	}
	if otherRouteRequests > 0 {
		if otherRouteRequests > math.MaxInt64-requestCount {
			return 0, 0, 0
		}
		requestCount += otherRouteRequests
	}
	if requestCount <= 0 {
		return requestCount, 0, 0
	}
	for i := range routes {
		if routes[i].Requests > 0 {
			routes[i].RequestSharePct = float64(routes[i].Requests) * 100 / float64(requestCount)
		}
	}
	if totalMillicents <= 0 {
		return requestCount, 0, 0
	}

	type remainder struct {
		index int // len(routes) denotes the Other routes bucket.
		value *big.Int
	}
	remainers := make([]remainder, 0, len(routes)+1)
	allocations := make([]int64, len(routes)+1)
	requestsByTarget := make([]int64, len(routes)+1)
	for i := range routes {
		if routes[i].Requests <= 0 {
			continue
		}
		requestsByTarget[i] = routes[i].Requests
	}
	requestsByTarget[len(routes)] = otherRouteRequests
	for i, requests := range requestsByTarget {
		if requests <= 0 {
			continue
		}
		numerator := new(big.Int).Mul(big.NewInt(totalMillicents), big.NewInt(requests))
		quotient, rem := new(big.Int), new(big.Int)
		quotient.QuoRem(numerator, big.NewInt(requestCount), rem)
		allocations[i] = quotient.Int64()
		allocatedMillicents += quotient.Int64()
		remainers = append(remainers, remainder{index: i, value: rem})
	}

	remaining := totalMillicents - allocatedMillicents
	for remaining > 0 && len(remainers) > 0 {
		best := 0
		for i := 1; i < len(remainers); i++ {
			if remainers[i].value.Cmp(remainers[best].value) > 0 {
				best = i
			}
		}
		allocations[remainers[best].index]++
		allocatedMillicents++
		remaining--
		remainers = append(remainers[:best], remainers[best+1:]...)
	}
	for i := range routes {
		routes[i].EstimatedComputeCostMillicents = allocations[i]
	}
	otherRouteMillicents = allocations[len(routes)]
	return requestCount, allocatedMillicents, otherRouteMillicents
}
