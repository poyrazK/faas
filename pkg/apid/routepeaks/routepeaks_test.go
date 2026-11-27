package routepeaks_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routepeaks"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWindow(t *testing.T) {
	if d, err := routepeaks.Window(""); err != nil || d != api.RouteResourceWindowDefault {
		t.Fatalf("default = %v %v", d, err)
	}
	if d, err := routepeaks.Window("6h"); err != nil || d != 6*time.Hour {
		t.Fatalf("6h = %v %v", d, err)
	}
	for _, bad := range []string{"10s", "-1h", "7d", "337h", "soon"} {
		if _, err := routepeaks.Window(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

// fakeStore returns fixed rows; it implements only the read half.
type fakeStore struct {
	state.RouteResourceStore
	resources       []routeresources.InstanceMinute
	resTruncated    bool
	routes          []routeresources.RouteMinute
	routesTruncated bool
	routeFrom       time.Time
}

func (f *fakeStore) InstanceResourceMinutes(context.Context, uuid.UUID, time.Time, time.Time, int) ([]routeresources.InstanceMinute, bool, error) {
	return f.resources, f.resTruncated, nil
}

func (f *fakeStore) InstanceRouteMinutes(_ context.Context, _ uuid.UUID, from, _ time.Time, _ int) ([]routeresources.RouteMinute, bool, error) {
	f.routeFrom = from
	return f.routes, f.routesTruncated, nil
}

var until = time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)

func at(i int) time.Time { return until.Add(-time.Duration(i) * time.Minute) }

func res(i, peak int) routeresources.InstanceMinute {
	return routeresources.InstanceMinute{InstanceID: "i", Minute: at(i), MemStartMB: 100, MemPeakMB: peak, Samples: 6}
}

func route(i int, name string) routeresources.RouteMinute {
	return routeresources.RouteMinute{InstanceID: "i", Minute: at(i), Route: name, Method: "GET", Requests: 1}
}

// A truncated resource read narrows the route read and drops its own,
// possibly partial, oldest minute.
func TestReadNarrowsToTruncatedResources(t *testing.T) {
	store := &fakeStore{resTruncated: true,
		resources: []routeresources.InstanceMinute{res(1, 900), res(2, 110), res(30, 110)},
		routes:    []routeresources.RouteMinute{route(1, "GET /pdf"), route(2, "GET /")},
	}
	resp, err := routepeaks.Read(context.Background(), store, uuid.New(), until.Add(-24*time.Hour), until)
	if err != nil {
		t.Fatal(err)
	}
	if !store.routeFrom.Equal(at(29)) || resp.From != at(29).Format(time.RFC3339) || !resp.Truncated {
		t.Fatalf("route read from %v, resp from %s truncated %v", store.routeFrom, resp.From, resp.Truncated)
	}
	if resp.ResourceMinutes != 2 || len(resp.Routes) != 2 || resp.Routes[0].HighMemoryShare != 1 {
		t.Fatalf("report = %+v", resp.Report)
	}
}

// A truncated route read must not leave older resource minutes looking
// idle: the window moves past the oldest route minute on both sides.
func TestReadNarrowsToTruncatedRoutes(t *testing.T) {
	store := &fakeStore{routesTruncated: true,
		resources: []routeresources.InstanceMinute{res(1, 900), res(2, 110), res(3, 110), res(10, 950)},
		routes:    []routeresources.RouteMinute{route(1, "GET /pdf"), route(2, "GET /"), route(3, "GET /")},
	}
	resp, err := routepeaks.Read(context.Background(), store, uuid.New(), until.Add(-24*time.Hour), until)
	if err != nil {
		t.Fatal(err)
	}
	if resp.From != at(2).Format(time.RFC3339) || !resp.Truncated {
		t.Fatalf("from %s truncated %v", resp.From, resp.Truncated)
	}
	if resp.ResourceMinutes != 2 || resp.UnattributedHighMemoryMinutes != 0 {
		t.Fatalf("older minutes kept: %+v", resp.Report)
	}
}

func TestFlagSubjectIgnoresOrganization(t *testing.T) {
	if got := routepeaks.FlagSubject("a", "pro"); got.OrgID != "" || got.AccountID != "a" || got.Plan != "pro" {
		t.Fatalf("subject = %+v", got)
	}
}
