package routehealth

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestLatencyCompleteExamplesUseInterpolatedRoutePercentile(t *testing.T) {
	for _, test := range []struct {
		fastWeight, slowWeight int64
		p95                    float64
	}{{1, 1, 575}, {19, 1, 125}, {20, 1, 100}, {99, 1, 100}, {1, 99, 600}} {
		s := api.RouteHealthInvestigationSide{MatchingRequests: test.fastWeight + test.slowWeight, Examples: []api.RouteHealthInvestigationExample{{LatencyMS: 600, RepresentedRequests: test.slowWeight}, {LatencyMS: 100, RepresentedRequests: test.fastWeight}}}
		if !validLatencyExamples(s, &test.p95) {
			t.Fatalf("valid interpolated p95 %v rejected for %+v", test.p95, s)
		}
		wrong := test.p95 + 1
		if validLatencyExamples(s, &wrong) {
			t.Fatal("complete inventory failed to bind percentile")
		}
	}
}
