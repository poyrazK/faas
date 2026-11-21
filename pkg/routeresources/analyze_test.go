package routeresources

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

var (
	testApp = uuid.MustParse("11111111-1111-1111-1111-111111111111")
	t0      = time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
)

func minute(i int) time.Time { return t0.Add(time.Duration(i) * time.Minute) }

// A 256 MB app whose /export route climbs to 1.5 GB a few times an hour:
// the report must put /export first with every attributed peak, and show
// the rest of the app peaking near its baseline without it.
func TestAnalyzeFindsHeavyRoute(t *testing.T) {
	var res []InstanceMinute
	var routes []RouteMinute
	for i := 0; i < 60; i++ {
		m := InstanceMinute{AppID: testApp, InstanceID: "i-1", Minute: minute(i),
			MemStartMB: 240, MemPeakMB: 250 + i%5, MemEndMB: 240, MemLimitMB: 2048,
			CPUPeakMillicores: 200, CPULimitMillicores: 1000, Samples: 60}
		routes = append(routes, RouteMinute{AppID: testApp, InstanceID: "i-1", Minute: minute(i),
			Route: "GET /v1/items", Method: "GET", Requests: 100, BusyMS: 3000})
		if i%10 == 3 {
			m.MemPeakMB = 1500
			m.CPUPeakMillicores = 950
			routes = append(routes, RouteMinute{AppID: testApp, InstanceID: "i-1", Minute: minute(i),
				Route: "POST /export", Method: "POST", Requests: 1, BusyMS: 40000})
		}
		res = append(res, m)
	}
	// One minute of route traffic with no samples is coverage, not data.
	routes = append(routes, RouteMinute{AppID: testApp, InstanceID: "i-2", Minute: minute(5), Route: "GET /v1/items", Method: "GET", Requests: 1})

	rep := Analyze(res, routes, Options{})
	if rep.ResourceMinutes != 60 || rep.ActiveMinutes != 60 || rep.RouteMinutesWithoutResources != 1 {
		t.Fatalf("coverage = %d/%d/%d", rep.ResourceMinutes, rep.ActiveMinutes, rep.RouteMinutesWithoutResources)
	}
	if rep.HighMemoryMinutes != 6 || rep.HighCPUMinutes != 6 || rep.MemPeakMaxMB != 1500 {
		t.Fatalf("peaks: mem %d cpu %d max %d", rep.HighMemoryMinutes, rep.HighCPUMinutes, rep.MemPeakMaxMB)
	}
	if len(rep.Routes) != 2 {
		t.Fatalf("routes = %+v", rep.Routes)
	}
	export := rep.Routes[0]
	if export.Route != "POST /export" || export.HighMemoryShare != 1 || export.HighCPUShare != 1 {
		t.Fatalf("export = %+v", export)
	}
	if export.MemPeakP95WithMB != 1500 || export.MemPeakP95WithoutMB > 260 {
		t.Fatalf("export with/without = %d/%d", export.MemPeakP95WithMB, export.MemPeakP95WithoutMB)
	}
	if export.PresenceShare != 0.1 || export.SoleHighMemoryMinutes != 0 {
		t.Fatalf("export presence = %v sole = %d", export.PresenceShare, export.SoleHighMemoryMinutes)
	}
	items := rep.Routes[1]
	// /v1/items runs in every minute, so it shares every peak too; what
	// separates it is that it is just as present when nothing peaks.
	if items.HighMemoryShare != 1 || items.PresenceShare != 1 || items.MemPeakP95WithoutMB != 0 {
		t.Fatalf("items = %+v", items)
	}
}

func TestAnalyzeSoleAndUnattributedPeaks(t *testing.T) {
	res := []InstanceMinute{
		{InstanceID: "i-1", Minute: minute(0), MemStartMB: 100, MemPeakMB: 900, Samples: 60},
		{InstanceID: "i-1", Minute: minute(1), MemStartMB: 100, MemPeakMB: 110, Samples: 60},
		{InstanceID: "i-1", Minute: minute(2), MemStartMB: 100, MemPeakMB: 800, Samples: 60}, // no request: a cron
	}
	routes := []RouteMinute{
		{InstanceID: "i-1", Minute: minute(0), Route: "GET /pdf", Method: "GET", Requests: 1},
		{InstanceID: "i-1", Minute: minute(1), Route: "GET /", Method: "GET", Requests: 5},
	}
	rep := Analyze(res, routes, Options{})
	if rep.HighMemoryMinutes != 2 || rep.UnattributedHighMemoryMinutes != 1 {
		t.Fatalf("high %d unattributed %d", rep.HighMemoryMinutes, rep.UnattributedHighMemoryMinutes)
	}
	pdf := rep.Routes[0]
	if pdf.Route != "GET /pdf" || pdf.HighMemoryShare != 1 || pdf.SoleHighMemoryMinutes != 1 {
		t.Fatalf("pdf = %+v", pdf)
	}
}

func TestAnalyzeRiseThresholdAndLimits(t *testing.T) {
	// A plateau (memory already high at the minute's start) is not a peak.
	res := []InstanceMinute{{InstanceID: "i", Minute: minute(0), MemStartMB: 1000, MemPeakMB: 1005, Samples: 6}}
	routes := []RouteMinute{{InstanceID: "i", Minute: minute(0), Route: "GET /", Method: "GET", Requests: 1}}
	if rep := Analyze(res, routes, Options{}); rep.HighMemoryMinutes != 0 || rep.HighMemoryRiseMB != defaultMinRiseMB {
		t.Fatalf("plateau counted: %+v", rep)
	}
	if rep := Analyze(nil, routes, Options{}); rep.RouteMinutesWithoutResources != 1 || len(rep.Routes) != 0 {
		t.Fatalf("no resources: %+v", rep)
	}
	var many []RouteMinute
	for i := 0; i < 5; i++ {
		many = append(many, RouteMinute{InstanceID: "i", Minute: minute(0), Route: string(rune('a' + i)), Method: "GET", Requests: 1})
	}
	if rep := Analyze(res, many, Options{MaxRoutes: 2}); len(rep.Routes) != 2 || rep.RoutesOmitted != 3 {
		t.Fatalf("max routes: %d omitted %d", len(rep.Routes), rep.RoutesOmitted)
	}
}

func TestMergeMinuteKeepsFirstStart(t *testing.T) {
	a := InstanceMinute{MemStartMB: 100, MemPeakMB: 300, MemEndMB: 200, CPUMS: 10, Samples: 30}
	b := InstanceMinute{MemStartMB: 200, MemPeakMB: 250, MemEndMB: 150, CPUMS: 5, Samples: 30}
	got := mergeMinute(a, b)
	if got.MemStartMB != 100 || got.MemPeakMB != 300 || got.MemEndMB != 150 || got.CPUMS != 15 || got.Samples != 60 {
		t.Fatalf("merge = %+v", got)
	}
}
