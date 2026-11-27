//go:build !no_pg

// adr: 20261121170000

package state_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
)

// A late slot for an instance-minute merges into it: the first start
// stands, peaks take the larger value, the end the newer one, CPU adds up.
// Route minutes add up, and reads stay inside the app and window.
func TestRouteResourceMinutesMergeAndRead(t *testing.T) {
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := state.NewPgStore(pool)
	app, other, account := uuid.New(), uuid.New(), uuid.New()
	minute := time.Now().UTC().Truncate(time.Minute).Add(-10 * time.Minute)
	res := func(app uuid.UUID, start, peak, end int, cpu int64) state.InstanceResourceMinuteRow {
		return state.InstanceResourceMinuteRow{AccountID: account, DeploymentID: uuid.New(), InstanceMinute: routeresources.InstanceMinute{
			AppID: app, InstanceID: "i-1", Minute: minute, Samples: 10, MemStartMB: start, MemPeakMB: peak, MemEndMB: end,
			MemLimitMB: 1024, CPUMS: cpu, CPUPeakMillicores: 300, CPULimitMillicores: 1000,
		}}
	}
	for _, batch := range [][]state.InstanceResourceMinuteRow{
		{res(app, 100, 400, 300, 50), res(other, 1, 2, 1, 1)},
		{res(app, 300, 350, 320, 25)},
	} {
		if err := store.AddInstanceResourceMinutes(ctx, batch); err != nil {
			t.Fatal(err)
		}
	}
	route := func(requests, busy int64, maxLatency int) state.InstanceRouteMinuteRow {
		return state.InstanceRouteMinuteRow{AccountID: account, RouteMinute: routeresources.RouteMinute{
			AppID: app, InstanceID: "i-1", Minute: minute, Route: "POST /export", Method: "POST",
			Requests: requests, BusyMS: busy, MaxLatencyMS: maxLatency,
		}}
	}
	for _, r := range []state.InstanceRouteMinuteRow{route(1, 30000, 30000), route(2, 10000, 9000)} {
		if err := store.AddInstanceRouteMinutes(ctx, []state.InstanceRouteMinuteRow{r}); err != nil {
			t.Fatal(err)
		}
	}

	got, truncated, err := store.InstanceResourceMinutes(ctx, app, minute, minute.Add(time.Minute), 10)
	if err != nil || truncated || len(got) != 1 {
		t.Fatalf("resources = %+v truncated=%v err=%v", got, truncated, err)
	}
	m := got[0]
	if m.MemStartMB != 100 || m.MemPeakMB != 400 || m.MemEndMB != 320 || m.CPUMS != 75 || m.Samples != 20 {
		t.Fatalf("merged minute = %+v", m)
	}
	routes, _, err := store.InstanceRouteMinutes(ctx, app, minute, minute.Add(time.Minute), 10)
	if err != nil || len(routes) != 1 || routes[0].Requests != 3 || routes[0].BusyMS != 40000 || routes[0].MaxLatencyMS != 30000 {
		t.Fatalf("routes = %+v err=%v", routes, err)
	}
	if got, _, _ := store.InstanceResourceMinutes(ctx, app, minute.Add(time.Minute), minute.Add(2*time.Minute), 10); len(got) != 0 {
		t.Fatalf("read outside the window: %+v", got)
	}
	if _, truncated, _ := store.InstanceResourceMinutes(ctx, app, minute, minute.Add(time.Minute), 0); !truncated {
		t.Fatal("limit 0 not reported as truncated")
	}
}
