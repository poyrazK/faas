package apidserver

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestRouteResourcesWindow(t *testing.T) {
	if d, err := routeResourcesWindow(""); err != nil || d != api.RouteResourceWindowDefault {
		t.Fatalf("default = %v %v", d, err)
	}
	if d, err := routeResourcesWindow("6h"); err != nil || d != 6*time.Hour {
		t.Fatalf("6h = %v %v", d, err)
	}
	for _, bad := range []string{"10s", "-1h", "7d", "337h", "soon"} {
		if _, err := routeResourcesWindow(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

type fakeRouteResourceStore struct {
	state.RouteResourceStore
	resources []routeresources.InstanceMinute
	truncated bool
	routeFrom time.Time
	routes    []routeresources.RouteMinute
}

func (f *fakeRouteResourceStore) InstanceResourceMinutes(context.Context, uuid.UUID, time.Time, time.Time, int) ([]routeresources.InstanceMinute, bool, error) {
	return f.resources, f.truncated, nil
}

func (f *fakeRouteResourceStore) InstanceRouteMinutes(_ context.Context, _ uuid.UUID, from, _ time.Time, _ int) ([]routeresources.RouteMinute, bool, error) {
	f.routeFrom = from
	return f.routes, false, nil
}

// A truncated resource read narrows the route read to the minutes it
// covers, so routes outside them cannot count as unsampled.
func TestReadRouteResourcesNarrowsTruncatedWindow(t *testing.T) {
	until := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	oldest := until.Add(-30 * time.Minute)
	store := &fakeRouteResourceStore{truncated: true, resources: []routeresources.InstanceMinute{
		{InstanceID: "i", Minute: until.Add(-time.Minute), MemStartMB: 100, MemPeakMB: 900, Samples: 6},
		{InstanceID: "i", Minute: oldest, MemStartMB: 100, MemPeakMB: 110, Samples: 6},
	}, routes: []routeresources.RouteMinute{
		{InstanceID: "i", Minute: until.Add(-time.Minute), Route: "GET /pdf", Method: "GET", Requests: 1},
	}}
	resp, err := readRouteResources(context.Background(), store, uuid.New(), until.Add(-24*time.Hour), until)
	if err != nil {
		t.Fatal(err)
	}
	if !store.routeFrom.Equal(oldest) || !resp.Truncated || resp.From != oldest.Format(time.RFC3339) {
		t.Fatalf("route read from %v, resp %+v", store.routeFrom, resp)
	}
	if len(resp.Routes) != 1 || resp.Routes[0].HighMemoryShare != 1 {
		t.Fatalf("routes = %+v", resp.Routes)
	}
}
