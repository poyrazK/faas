package main

import (
	"math"
	"math/big"

	"github.com/onebox-faas/faas/pkg/api"
)

// allocateRequestShareMillicents splits one estimated amount across request
// counts. Largest-remainder rounding keeps the integer allocations equal to
// the original amount whenever at least one request is present.
func allocateRequestShareMillicents(requestCounts []int64, totalMillicents int64) ([]int64, int64, int64) {
	allocations := make([]int64, len(requestCounts))
	var totalRequests int64
	for _, requests := range requestCounts {
		if requests <= 0 {
			continue
		}
		if requests > math.MaxInt64-totalRequests {
			return allocations, 0, 0
		}
		totalRequests += requests
	}
	if totalRequests == 0 || totalMillicents <= 0 {
		return allocations, totalRequests, 0
	}

	type remainder struct {
		index int
		value *big.Int
	}
	remainders := make([]remainder, 0, len(requestCounts))
	var allocated int64
	for i, requests := range requestCounts {
		if requests <= 0 {
			continue
		}
		numerator := new(big.Int).Mul(big.NewInt(totalMillicents), big.NewInt(requests))
		quotient, rem := new(big.Int), new(big.Int)
		quotient.QuoRem(numerator, big.NewInt(totalRequests), rem)
		allocations[i] = quotient.Int64()
		allocated += allocations[i]
		remainders = append(remainders, remainder{index: i, value: rem})
	}

	remaining := totalMillicents - allocated
	for remaining > 0 && len(remainders) > 0 {
		best := 0
		for i := 1; i < len(remainders); i++ {
			if remainders[i].value.Cmp(remainders[best].value) > 0 {
				best = i
			}
		}
		allocations[remainders[best].index]++
		allocated++
		remaining--
		remainders = append(remainders[:best], remainders[best+1:]...)
	}
	return allocations, totalRequests, allocated
}

// allocateRequestAnalyticsRouteCost distributes an estimated compute value
// across the bounded route rows and the omitted-route bucket by observed
// request share. The route query adds an __other__ group when its top-N limit
// omits routes; that count is passed separately so the public route rows stay
// actual route/method pairs.
func allocateRequestAnalyticsRouteCost(routes []api.RequestAnalyticsRoute, otherRouteRequests, totalMillicents int64) (requestCount, allocatedMillicents, otherRouteMillicents int64) {
	counts := make([]int64, len(routes)+1)
	for i := range routes {
		counts[i] = routes[i].Requests
	}
	counts[len(routes)] = otherRouteRequests
	allocations, requestCount, allocatedMillicents := allocateRequestShareMillicents(counts, totalMillicents)
	for i := range routes {
		routes[i].EstimatedComputeCostMillicents = allocations[i]
		if requestCount > 0 && routes[i].Requests > 0 {
			routes[i].RequestSharePct = float64(routes[i].Requests) * 100 / float64(requestCount)
		} else {
			routes[i].RequestSharePct = 0
		}
	}
	return requestCount, allocatedMillicents, allocations[len(routes)]
}
