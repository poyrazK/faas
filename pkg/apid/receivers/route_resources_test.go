package receivers

import (
	"testing"
	"time"

	"github.com/google/uuid"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
)

func testRouteResources(now time.Time) *RouteResources {
	return &RouteResources{now: func() time.Time { return now }}
}

func TestRouteResourceRowsValidate(t *testing.T) {
	now := time.Date(2026, 11, 21, 12, 0, 30, 0, time.UTC)
	rr := testRouteResources(now)
	minute := now.Truncate(time.Minute).Add(-time.Minute).Unix()
	good := &apidpb.InstanceRouteMinute{AccountId: uuid.NewString(), AppId: uuid.NewString(), InstanceId: "i-1",
		MinuteUnix: minute, Route: "GET /", Method: "GET", Requests: 1, BusyMs: 5}
	if _, ok := rr.routeRowFromProto(good); !ok {
		t.Fatal("good route row refused")
	}
	for name, mutate := range map[string]func(*apidpb.InstanceRouteMinute){
		"unaligned minute": func(p *apidpb.InstanceRouteMinute) { p.MinuteUnix++ },
		"future minute":    func(p *apidpb.InstanceRouteMinute) { p.MinuteUnix = now.Add(time.Hour).Truncate(time.Minute).Unix() },
		"expired minute": func(p *apidpb.InstanceRouteMinute) {
			p.MinuteUnix = now.Add(-15 * 24 * time.Hour).Truncate(time.Minute).Unix()
		},
		"no instance":   func(p *apidpb.InstanceRouteMinute) { p.InstanceId = "" },
		"no route":      func(p *apidpb.InstanceRouteMinute) { p.Route = "" },
		"bad app":       func(p *apidpb.InstanceRouteMinute) { p.AppId = "x" },
		"negative busy": func(p *apidpb.InstanceRouteMinute) { p.BusyMs = -1 },
		"invalid utf8":  func(p *apidpb.InstanceRouteMinute) { p.Route = "\xff" },
	} {
		p := &apidpb.InstanceRouteMinute{AccountId: good.AccountId, AppId: good.AppId, InstanceId: good.InstanceId,
			MinuteUnix: good.MinuteUnix, Route: good.Route, Method: good.Method, Requests: 1, BusyMs: 5}
		mutate(p)
		if _, ok := rr.routeRowFromProto(p); ok {
			t.Errorf("%s accepted", name)
		}
	}

	res := &apidpb.InstanceResourceMinute{AccountId: uuid.NewString(), AppId: uuid.NewString(), DeploymentId: uuid.NewString(),
		InstanceId: "i-1", MinuteUnix: minute, Samples: 60, MemStartMb: 100, MemPeakMb: 300, MemEndMb: 200}
	if row, ok := rr.resourceRowFromProto(res); !ok || row.MemPeakMB != 300 {
		t.Fatalf("good resource row: %+v %v", row, ok)
	}
	for name, mutate := range map[string]func(*apidpb.InstanceResourceMinute){
		"no samples":       func(p *apidpb.InstanceResourceMinute) { p.Samples = 0 },
		"peak below start": func(p *apidpb.InstanceResourceMinute) { p.MemPeakMb = 50 },
		"end above peak":   func(p *apidpb.InstanceResourceMinute) { p.MemEndMb = 400 },
		"no deployment":    func(p *apidpb.InstanceResourceMinute) { p.DeploymentId = "" },
		"negative cpu":     func(p *apidpb.InstanceResourceMinute) { p.CpuMs = -1 },
	} {
		p := &apidpb.InstanceResourceMinute{AccountId: res.AccountId, AppId: res.AppId, DeploymentId: res.DeploymentId,
			InstanceId: "i-1", MinuteUnix: minute, Samples: 60, MemStartMb: 100, MemPeakMb: 300, MemEndMb: 200}
		mutate(p)
		if _, ok := rr.resourceRowFromProto(p); ok {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestNewRouteResourcesNeedsStoreAndRelease(t *testing.T) {
	if NewRouteResources(nil, nil) != nil {
		t.Fatal("route resources without a store")
	}
}
