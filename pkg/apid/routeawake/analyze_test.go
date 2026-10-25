package routeawake_test

// adr: 1187, 1199, 20261022184757

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

// TestProbesAndPrewarmExplainAwakeTime credits minutes no request explains to
// the probe or prewarm that kept the app awake (ADR-1199), and a route's
// what-if keeps the minutes a prewarm would have covered anyway.
func TestProbesAndPrewarmExplainAwakeTime(t *testing.T) {
	s := newScenario(60).awakeRange(0, 60)
	for m := 30; m < 60; m++ {
		s.call("GET", "/health", m, 1, 1)
	}
	in := s.inputs()
	in.Sources = []routeawake.SourceMinute{{At: at(0), Source: api.RouteAwakeSourcePrewarm, Events: 1}}
	for m := 10; m < 20; m++ {
		in.Sources = append(in.Sources, routeawake.SourceMinute{At: at(m), Source: api.RouteAwakeSourceRouteProbe, Events: 10})
	}
	out := routeawake.Analyze(in)
	if len(out.Sources) != 2 {
		t.Fatalf("sources = %+v", out.Sources)
	}
	probe, prewarm := out.Sources[0], out.Sources[1]
	// Probes at 10..19 keep the app awake through minute 21; the prewarm at 0
	// through minute 2. Minutes 3..9 and 22..29 stay unexplained.
	if probe.Source != api.RouteAwakeSourceRouteProbe || probe.Events != 100 || probe.AwakeSeconds != 12*60 ||
		prewarm.Source != api.RouteAwakeSourcePrewarm || prewarm.Events != 1 || prewarm.AwakeSeconds != 3*60 {
		t.Fatalf("sources = %+v", out.Sources)
	}
	if out.UnattributedSeconds != 15*60 || find(t, out, "GET", "/health").AwakeSeconds != 30*60 {
		t.Fatalf("unattributed = %d routes = %+v", out.UnattributedSeconds, out.Routes)
	}

	s = newScenario(12).awakeRange(0, 12)
	for m := 0; m < 10; m++ {
		s.call("GET", "/feed", m, 1, 0)
	}
	in = s.inputs()
	in.Sources = []routeawake.SourceMinute{{At: at(5), Source: api.RouteAwakeSourcePrewarm, Events: 1}}
	if saved := find(t, routeawake.Analyze(in), "GET", "/feed").IfRemoved.AwakeSecondsSaved; saved != 9*60 {
		t.Fatalf("the prewarm keeps minutes 5..7 awake without the route: saved %d", saved)
	}
}

// adr: 20261022184757 — deliveries schedd made to the app explain minutes no
// request does, for as long as they run plus the idle timeout. Each cron job
// and each queue is its own source; events count the deliveries that started.
func TestBackgroundWorkExplainsAwakeTime(t *testing.T) {
	in := newScenario(60).awakeRange(0, 60).inputs()
	for m := 10; m < 20; m++ {
		started := int64(0)
		if m == 10 {
			started = 1
		}
		in.Sources = append(in.Sources, routeawake.SourceMinute{At: at(m), Source: api.RouteAwakeSourceQueue, Detail: "emails", Name: "emails", Events: started, Running: 1})
	}
	in.Sources = append(in.Sources,
		routeawake.SourceMinute{At: at(30), Source: api.RouteAwakeSourceQueue, Detail: "reports", Name: "reports", Events: 2, Running: 2},
		routeawake.SourceMinute{At: at(40), Source: api.RouteAwakeSourceCron, Detail: "cron-1", Name: "/sync (*/30 * * * *)", Events: 1, Running: 1},
		routeawake.SourceMinute{At: at(41), Source: api.RouteAwakeSourceCron, Detail: "cron-1", Name: "/sync (*/30 * * * *)", Running: 1},
	)
	out := routeawake.Analyze(in)
	got := map[string]api.RouteAwakeSource{}
	for _, src := range out.Sources {
		got[src.Source+"/"+src.Name] = src
	}
	// emails runs 10..19 and keeps the app awake through 21; reports runs in
	// minute 30 (through 32); the cron job runs 40..41 (through 43).
	emails, reports, cron := got["queue/emails"], got["queue/reports"], got["cron//sync (*/30 * * * *)"]
	if emails.Events != 1 || emails.AwakeSeconds != 12*60 || reports.Events != 2 || reports.AwakeSeconds != 3*60 ||
		cron.Events != 1 || cron.AwakeSeconds != 4*60 || cron.Pattern != api.RouteAwakePatternIrregular || cron.Suggestion != nil {
		t.Fatalf("sources = %+v", out.Sources)
	}
	if out.UnattributedSeconds != (60-12-3-4)*60 {
		t.Fatalf("unattributed = %d", out.UnattributedSeconds)
	}
}

// adr: 20261022184757 — a cron job that fires inside the idle timeout keeps
// the app from ever parking, and the report says to run it less often.
func TestFrequentCronJobSuggestsRunningLessOften(t *testing.T) {
	in := newScenario(180).awakeRange(0, 180).inputs()
	for m := 0; m < 180; m++ {
		in.Sources = append(in.Sources, routeawake.SourceMinute{At: at(m), Source: api.RouteAwakeSourceCron, Detail: "cron-1", Name: "/sync (* * * * *)", Events: 1, Running: 1})
	}
	out := routeawake.Analyze(in)
	if len(out.Sources) != 1 {
		t.Fatalf("sources = %+v", out.Sources)
	}
	cron := out.Sources[0]
	if cron.Pattern != api.RouteAwakePatternPeriodic || cron.IntervalSeconds != 60 || cron.Suggestion == nil ||
		cron.Suggestion.Kind != api.RouteAwakeSuggestRunLessOften || out.UnattributedSeconds != 0 {
		t.Fatalf("cron = %+v unattributed = %d", cron, out.UnattributedSeconds)
	}
}

// adr: 20261022184757 — a waitUntil task can hold the instance past the idle
// timeout, so a request keeps the app awake for the longer of the two. At
// today's limits (at most 60 s) the minute-resolution window already covers
// it; a longer tail widens the window.
func TestWaitUntilTimeoutWidensTheKeepWindow(t *testing.T) {
	s := newScenario(10).awakeRange(0, 6)
	s.call("POST", "/signup", 0, 1, 0)
	in := s.inputs()
	in.IdleTimeoutS = 30
	if out := routeawake.Analyze(in); out.UnattributedSeconds != 3*60 || out.WaitUntilTimeoutSeconds != 0 {
		t.Fatalf("idle only: unattributed = %d wait_until = %d", out.UnattributedSeconds, out.WaitUntilTimeoutSeconds)
	}
	in.TailTimeoutS = 180
	out := routeawake.Analyze(in)
	if out.UnattributedSeconds != 1*60 || out.WaitUntilTimeoutSeconds != 180 || find(t, out, "POST", "/signup").AwakeSeconds != 5*60 {
		t.Fatalf("with waitUntil: unattributed = %d wait_until = %d routes = %+v", out.UnattributedSeconds, out.WaitUntilTimeoutSeconds, out.Routes)
	}
}

// Production-us rc.294: `routes awake h14-py` read "0 wakes" above a GET /
// row with one wake. The header counted usage_minutes cold boots, which
// record only restore-to-cold-boot fallbacks (ADR-048), so snapshot wakes
// were missing (Hunt 14 H14-18).
func TestWakesCountRequestsThatWokeTheApp(t *testing.T) {
	in := newScenario(10).awakeRange(0, 10).inputs()
	in.Routes = append(in.Routes,
		routeawake.RouteMinute{At: at(1), Method: "GET", Path: "/", Requests: 1, ColdBoots: 1},
		routeawake.RouteMinute{At: at(6), Method: "POST", Path: "/hook", Requests: 2, ColdBoots: 1},
		routeawake.RouteMinute{At: at(20), Method: "GET", Path: "/late", Requests: 1, ColdBoots: 1})
	if out := routeawake.Analyze(in); out.Wakes != 2 {
		t.Fatalf("wakes = %d, want the 2 in-window request wakes", out.Wakes)
	}
	// Metered cold boots still count when no request telemetry explains them.
	in.Routes = nil
	in.Awake[0].ColdBoots = 3
	if out := routeawake.Analyze(in); out.Wakes != 3 {
		t.Fatalf("wakes = %d, want the 3 metered cold boots", out.Wakes)
	}
}
