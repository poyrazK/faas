// adr: 20261024154324
package gateway

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
)

// emitRoute is Handler.observe's per-route emission for one request.
func emitRoute(m *Metrics, app, route string, status int, now time.Time) bool {
	code, class := strconv.Itoa(status), statusClassBucket(status)
	if !m.AdmitRouteSeries(app, "pro", route, code, class, now) {
		return false
	}
	m.ObserveRequestRoute(app, "pro", route, code)
	m.ObserveRequestDurationRoute(app, route, class, 10*time.Millisecond)
	if status >= 400 {
		m.RequestFailureRoute(app, "pro", route, code)
	}
	return true
}

// appSeries counts the per-route series the registry holds for app.
func appSeries(t *testing.T, m *Metrics, app string) int {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range families {
		switch f.GetName() {
		case "gateway_requests_by_route_total", "gateway_request_duration_by_route_seconds", "gateway_request_failures_by_route_total":
		default:
			continue
		}
		for _, metric := range f.GetMetric() {
			for _, l := range metric.GetLabel() {
				if l.GetName() == "app" && l.GetValue() == app {
					n++
				}
			}
		}
	}
	return n
}

func TestRouteSeriesBudgetCapsLivePairs(t *testing.T) {
	m := NewMetrics()
	m.SetRouteSeriesBudget(3, time.Hour)
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	steps := []struct {
		app, route string
		want       bool
	}{
		{"a", "GET /1", true},
		{"a", "GET /2", true},
		{"b", "GET /1", true},
		{"b", "GET /2", false}, // budget of 3 pairs is full
		{"a", "GET /1", true},  // a known pair needs no budget
	}
	for i, s := range steps {
		if got := emitRoute(m, s.app, s.route, 200, now); got != s.want {
			t.Fatalf("step %d: emit(%s %s) = %v, want %v", i, s.app, s.route, got, s.want)
		}
	}
	if got := m.routeSeries.live(); got != 3 {
		t.Fatalf("live pairs = %d, want 3", got)
	}
	if got := testutil.ToFloat64(m.routeSeriesRejected); got != 1 {
		t.Fatalf("rejected = %v, want 1", got)
	}
	if appSeries(t, m, "b") == 0 {
		t.Fatal("b's admitted route has no series")
	}
}

func TestRouteSeriesSweepDeletesIdleAppsExactly(t *testing.T) {
	m := NewMetrics()
	m.SetRouteSeriesBudget(3, time.Hour)
	t0 := time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)
	emitRoute(m, "idle", "GET /a", 200, t0)
	emitRoute(m, "idle", "GET /a", 503, t0)
	emitRoute(m, "idle", "POST /b", 404, t0)
	emitRoute(m, "busy", "GET /c", 200, t0)
	if emitRoute(m, "busy", "GET /d", 200, t0) {
		t.Fatal("budget should be full before the sweep")
	}

	emitRoute(m, "busy", "GET /c", 200, t0.Add(90*time.Minute))
	if n := m.SweepRouteSeries(t0.Add(2 * time.Hour)); n != 1 {
		t.Fatalf("swept %d apps, want 1", n)
	}
	if got := appSeries(t, m, "idle"); got != 0 {
		t.Fatalf("idle app keeps %d per-route series after eviction", got)
	}
	if appSeries(t, m, "busy") == 0 {
		t.Fatal("busy app lost its series")
	}
	if got := m.routeSeries.live(); got != 1 {
		t.Fatalf("live pairs after sweep = %d, want 1", got)
	}
	if got := testutil.ToFloat64(m.routeSeriesEvictions); got != 1 {
		t.Fatalf("evictions = %v, want 1", got)
	}
	// The freed budget admits new pairs, and a returning app re-creates its
	// series with the pre-instantiated classes.
	if !emitRoute(m, "busy", "GET /d", 200, t0.Add(2*time.Hour)) {
		t.Fatal("freed budget did not admit a new pair")
	}
	if !emitRoute(m, "idle", "GET /a", 200, t0.Add(2*time.Hour)) {
		t.Fatal("returning app was not re-admitted")
	}
	if got := appSeries(t, m, "idle"); got != 5 { // 4 pre-instantiated classes + 1 counter
		t.Fatalf("returning app has %d series, want 5", got)
	}
}

// TestRouteSeriesBudgetConcurrent races emissions against sweeps: the live
// count must never exceed the budget and must equal what the apps hold.
func TestRouteSeriesBudgetConcurrent(t *testing.T) {
	const budget = 40
	m := NewMetrics()
	m.SetRouteSeriesBudget(budget, time.Millisecond)
	start := time.Now()
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 500 {
				app := "app-" + strconv.Itoa((g+i)%12)
				emitRoute(m, app, "GET /"+strconv.Itoa(i%7), 200, start.Add(time.Duration(i)*time.Millisecond))
				if live := m.routeSeries.live(); live > budget {
					t.Errorf("live pairs %d exceed budget %d", live, budget)
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := range 200 {
			m.SweepRouteSeries(start.Add(time.Duration(i) * 3 * time.Millisecond))
		}
	}()
	wg.Wait()
	held := int64(0)
	m.routeSeries.apps.Range(func(_, v any) bool {
		e := v.(*routeSeriesApp)
		e.mu.Lock()
		held += int64(len(e.routes))
		e.mu.Unlock()
		return true
	})
	if live := m.routeSeries.live(); live != held {
		t.Fatalf("live pairs %d != pairs held by apps %d", live, held)
	}
}
