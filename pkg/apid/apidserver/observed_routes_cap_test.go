package apidserver

import (
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
)

func routeSample(node, route string) promql.VectorSample {
	return promql.VectorSample{Labels: map[string]string{"node_id": node, "route": route}, Value: 1}
}

// prod 2026-11-19: an app with 9 routes was told it had hit the 50-route cap
// because timed-out and OOM-killed requests land in __route_other__.
func TestObservedRoutesOtherBucketAloneIsNotTheCap(t *testing.T) {
	healthy := map[string]struct{}{"n1": {}}
	routes, capHit := mergeObservedRouteSamples([]promql.VectorSample{
		routeSample("n1", "GET /info"), routeSample("n1", "GET /sleep"), routeSample("n1", observedRoutesOther),
	}, healthy)
	if capHit {
		t.Fatal("cap_hit = true with 2 routes; __route_other__ alone is not the cap")
	}
	if len(routes) != 3 || routes[2] != observedRoutesOther {
		t.Fatalf("routes = %v; want the two routes and the other bucket", routes)
	}

	var samples []promql.VectorSample
	for i := 0; i < api.RouteMetricsPerAppCap; i++ {
		samples = append(samples, routeSample("n1", fmt.Sprintf("GET /r%02d", i)))
	}
	if _, capHit := mergeObservedRouteSamples(samples, healthy); !capHit {
		t.Fatal("cap_hit = false at the route cap")
	}
}
