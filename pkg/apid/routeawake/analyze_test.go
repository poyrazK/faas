package routeawake_test

// adr: 1187

import (
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/routeawake"
)

var from = time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC)

const (
	ramMB   = 256
	perMin  = 60 * (ramMB + api.PerVMOverheadMB) // MB-seconds of one fully awake minute
	idleSec = 60
)

func at(minute int) time.Time { return from.Add(time.Duration(minute) * time.Minute) }

// scenario builds inputs for a window of n minutes.
type scenario struct {
	n      int
	awake  map[int]bool
	routes []routeawake.RouteMinute
}

func newScenario(n int) *scenario { return &scenario{n: n, awake: map[int]bool{}} }

func (s *scenario) awakeRange(lo, hi int) *scenario {
	for i := lo; i < hi; i++ {
		s.awake[i] = true
	}
	return s
}

func (s *scenario) call(method, path string, minute int, requests, anonymous int64) *scenario {
	s.routes = append(s.routes, routeawake.RouteMinute{At: at(minute), Method: method, Path: path, Requests: requests, AnonymousRequests: anonymous})
	return s
}

func (s *scenario) inputs() routeawake.Inputs {
	in := routeawake.Inputs{App: "shop", From: from, Until: at(s.n), IdleTimeoutS: idleSec, RAMMB: ramMB, Routes: s.routes}
	for i := 0; i < s.n; i++ {
		if s.awake[i] {
			in.Awake = append(in.Awake, routeawake.Minute{At: at(i), MBSeconds: perMin})
		}
	}
	return in
}

func find(t *testing.T, out api.RouteAwakeResponse, method, path string) api.RouteAwakeRoute {
	t.Helper()
	for _, r := range out.Routes {
		if r.Method == method && r.Route == path {
			return r
		}
	}
	t.Fatalf("route %s %s missing from %+v", method, path, out.Routes)
	return api.RouteAwakeRoute{}
}

func TestUptimePingerKeepsAppAwake(t *testing.T) {
	s := newScenario(360).awakeRange(0, 360)
	for m := 0; m < 360; m++ {
		s.call("GET", "/health", m, 1, 1)
	}
	for _, burst := range []int{100, 200, 300} {
		s.call("POST", "/orders", burst, 5, 0)
	}
	out := routeawake.Analyze(s.inputs())
	if out.AwakeSeconds != 360*60 || out.AwakeShare != 1 || out.UnattributedSeconds != 0 || out.IdleTimeoutSeconds != idleSec {
		t.Fatalf("totals = %+v", out)
	}
	health := find(t, out, "GET", "/health")
	if health.Pattern != api.RouteAwakePatternPeriodic || health.IntervalSeconds != 60 {
		t.Fatalf("health pattern = %s every %ds", health.Pattern, health.IntervalSeconds)
	}
	if out.Routes[0].Route != "/health" || health.AwakeShare < 0.95 {
		t.Fatalf("health should own nearly all awake time: %+v", out.Routes)
	}
	// Without /health only the three bursts and their idle tails stay awake;
	// each burst now starts with a wake.
	if want := int64(360-3*3) * 60; health.IfRemoved.AwakeSecondsSaved != want || health.IfRemoved.WakesChange != 3 {
		t.Fatalf("if removed = %+v, want %ds saved and 3 more wakes", health.IfRemoved, want)
	}
	if health.Suggestion == nil || health.Suggestion.Kind != api.RouteAwakeSuggestEdgeCache {
		t.Fatalf("suggestion = %+v", health.Suggestion)
	}
	orders := find(t, out, "POST", "/orders")
	if orders.Pattern != api.RouteAwakePatternIrregular || orders.Suggestion != nil || orders.IfRemoved.AwakeSecondsSaved != 0 {
		t.Fatalf("orders = %+v", orders)
	}
}

func TestAuthenticatedPollerSuggestsCallingLessOften(t *testing.T) {
	s := newScenario(240).awakeRange(0, 240)
	for m := 0; m < 240; m++ {
		s.call("GET", "/api/status", m, 2, 0)
	}
	out := routeawake.Analyze(s.inputs())
	status := find(t, out, "GET", "/api/status")
	if status.IntervalSeconds != 30 || status.Suggestion == nil || status.Suggestion.Kind != api.RouteAwakeSuggestCallLessOften {
		t.Fatalf("status = %+v", status)
	}
}

func TestIrregularTrafficHasNoSuggestion(t *testing.T) {
	s := newScenario(600)
	minute := 0
	for i := 0; minute < 590; i++ {
		s.awakeRange(minute, minute+3)
		s.call("GET", "/", minute, int64(1+i%7), int64(1+i%7))
		minute += 3 + (i*7)%11
	}
	out := routeawake.Analyze(s.inputs())
	home := find(t, out, "GET", "/")
	if home.Pattern != api.RouteAwakePatternIrregular || home.Suggestion != nil {
		t.Fatalf("home = %+v", home)
	}
	if home.AwakeSeconds != out.AwakeSeconds {
		t.Fatalf("all awake time should be attributed to /: %d of %d", home.AwakeSeconds, out.AwakeSeconds)
	}
}

func TestAwakeTimeWithoutRequestsIsUnattributed(t *testing.T) {
	// Awake for ten minutes with no request (prewarm, probes, cron), then a
	// request whose idle tail covers two more minutes.
	s := newScenario(60).awakeRange(0, 10).awakeRange(30, 33).call("GET", "/", 30, 1, 1)
	out := routeawake.Analyze(s.inputs())
	if out.UnattributedSeconds != 10*60 || find(t, out, "GET", "/").AwakeSeconds != 3*60 {
		t.Fatalf("out = %+v", out)
	}
	if out.Wakes != 0 || out.AwakeShare != 13.0/60 {
		t.Fatalf("share = %v", out.AwakeShare)
	}
}

func TestSplitAndOtherRoutes(t *testing.T) {
	s := newScenario(10).awakeRange(0, 1).
		call("GET", "/a", 0, 3, 0).call("GET", "/b", 0, 1, 0).call("", "", 0, 4, 0)
	out := routeawake.Analyze(s.inputs())
	a, b := find(t, out, "GET", "/a"), find(t, out, "GET", "/b")
	// Each share is truncated to whole seconds, so the parts may lose a
	// second each; the MB-seconds behind them add up exactly.
	if sum := a.AwakeSeconds + b.AwakeSeconds + out.OtherRoutesSeconds; sum < 58 || sum > 60 || out.OtherRoutesSeconds != 30 || a.AwakeSeconds != 3*b.AwakeSeconds+1 {
		t.Fatalf("split a=%d b=%d other=%d", a.AwakeSeconds, b.AwakeSeconds, out.OtherRoutesSeconds)
	}
	if len(out.Routes) != 2 || out.RoutesLimit != api.RouteAwakeMaxRoutes {
		t.Fatalf("routes = %+v", out.Routes)
	}
}

func TestMinutesOutsideTheWindowAreIgnored(t *testing.T) {
	in := newScenario(10).awakeRange(0, 10).call("GET", "/", 0, 1, 1).inputs()
	in.Awake = append(in.Awake, routeawake.Minute{At: at(-5), MBSeconds: perMin, ColdBoots: 1}, routeawake.Minute{At: at(10), MBSeconds: perMin})
	in.Routes = append(in.Routes, routeawake.RouteMinute{At: at(20), Method: "GET", Path: "/late", Requests: 9})
	out := routeawake.Analyze(in)
	if out.AwakeSeconds != 600 || out.Wakes != 0 || len(out.Routes) != 1 {
		t.Fatalf("out = %+v", out)
	}
}
