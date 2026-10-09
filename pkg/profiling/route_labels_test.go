package profiling

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestMergedRouteMarkerPosition(t *testing.T) {
	marker := frame{name: routeFramePrefix + "GET /hot/{id}", file: routeFrameFile}
	work := frame{name: "hotWork", file: "app.go", line: 42}
	for _, frames := range [][]frame{{marker, work}, {work, marker}, {work, marker, work}} {
		route, clean := sampleRoute(frames)
		if route != "GET /hot/{id}" || len(clean) != len(frames)-1 {
			t.Fatalf("route %q frames %+v", route, clean)
		}
		for _, f := range clean {
			if f != work {
				t.Fatalf("marker leaked: %+v", clean)
			}
		}
	}
	spoof := frame{name: marker.name, file: "app.go"}
	if route, _ := sampleRoute([]frame{spoof}); route != api.ProfileUnattributedRoute {
		t.Fatal("unreserved filename admitted")
	}
	if route, clean := sampleRoute([]frame{marker, work, marker}); route != "GET /hot/{id}" || len(clean) != 1 {
		t.Fatal("repeated markers", route, clean)
	}
	other := frame{name: routeFramePrefix + "GET /other", file: routeFrameFile}
	if route, clean := sampleRoute([]frame{marker, work, other}); route != api.ProfileUnattributedRoute || len(clean) != 1 {
		t.Fatal("conflicting markers", route, clean)
	}
}

func TestRouteLabelCoverageRequiresReconciledCounters(t *testing.T) {
	const route = "GET /hot/{id}"
	observed := int64(100)
	for _, tc := range []struct {
		name      string
		count     int64
		complete  bool
		available bool
	}{
		{"complete", 100, true, true},
		{"partial labeling", 50, true, true},
		{"overcount", 101, true, false},
		{"incomplete reports", 100, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := api.ProfileResponse{Coverage: &api.ProfileCoverage{Available: true, LabelCountsComplete: tc.complete, LabelCountProfiles: 2, LabelCounts: map[string]int64{routeRequestHash(route): tc.count}}}
			got := routeLabelCoverage(p, route, &observed)
			if got.Available != tc.available {
				t.Fatalf("coverage: %+v", got)
			}
			if got.Available && *got.Percent != float64(tc.count) {
				t.Fatalf("percentage: %+v", got)
			}
		})
	}
}

func TestRouteLabelComparisonRejectsLowOrChangedLabeling(t *testing.T) {
	const route = "GET /hot/{id}"
	observed := int64(100)
	makeProfile := func(count int64) api.ProfileResponse {
		return api.ProfileResponse{Coverage: &api.ProfileCoverage{Available: true, LabelCountsComplete: true, LabelCountProfiles: 1, LabelCounts: map[string]int64{routeRequestHash(route): count}}}
	}
	for _, tc := range []struct {
		count      int64
		consistent bool
	}{{100, true}, {90, true}, {80, false}, {50, false}} {
		got := compareRouteLabels(makeProfile(100), makeProfile(tc.count), route, &observed, &observed)
		if !got.Available || got.Consistent != tc.consistent {
			t.Fatalf("candidate count %d: %+v", tc.count, got)
		}
	}
}
