package routestatus

// adr: 1152

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestBuildMarksRoutesWithAdvice(t *testing.T) {
	s := Build(Inputs{
		App: "demo",
		Usage: &api.RouteCustomerUsageResponse{Routes: []api.RouteCustomerUsage{
			usage("GET", "/catalog", 900, 40),
			usage("POST", "/checkout", 300, 30),
		}},
		Advice: &api.RouteAdviceResponse{Suggestions: []api.RouteAdviceSuggestion{
			{ID: "a1", Kind: api.RouteAdviceKindCache, Method: "GET", Route: "/catalog", Title: "Cache GET /catalog"},
			{ID: "a2", Kind: api.RouteAdviceKindWakeAhead, Title: "Turn on service wake-ahead for demo"},
		}},
	})
	if s.Summary.Advice != 2 || len(s.Advice) != 2 || s.Advice[1].Method != "" {
		t.Fatalf("advice = %+v summary = %+v", s.Advice, s.Summary)
	}
	for _, r := range s.Routes {
		if got := hasGap(r, GapAdviceAvailable); got != (r.Path == "/catalog") {
			t.Fatalf("%s %s advice gap = %v", r.Method, r.Path, got)
		}
	}
}
