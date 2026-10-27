package gateway

// adr: 20261026143210

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

// scannerHandler serves /checkout (200) and /broken (500) and answers every
// other path 404, as an app does for internet scanners.
func scannerHandler(t *testing.T) *Handler {
	t.Helper()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/checkout":
			w.WriteHeader(http.StatusOK)
		case "/broken":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)
	backend := &fakeBackend{
		app:  App{ID: uuid.NewString(), AccountID: uuid.NewString(), Plan: api.PlanPro, RouteMetricsEnabled: true},
		host: "customer.example", upstream: upstream.Listener.Addr().String(),
	}
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString()})
	h := NewHandlerWith(backend, NewMetrics(), nil)
	h.requestTelemetry = makeTestRecorder()
	h.WithRouteMetricsEnabled(true)
	return h
}

func serveRoute(t *testing.T, h *Handler, method, path string, want int) string {
	t.Helper()
	r := httptest.NewRequest(method, "http://customer.example"+path, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s status = %d, want %d", method, path, w.Code, want)
	}
	rows := h.requestTelemetry.DrainBatch(4)
	if len(rows) != 1 {
		t.Fatalf("%s %s telemetry rows = %d, want 1", method, path, len(rows))
	}
	return rows[0].Route
}

// Internet scanners probe every public host with paths the app does not
// serve. Those 404s must not use up the app's bounded route labels, or the
// app's real routes fold into __route_other__ for every routes feature that
// reads the label (telemetry, monitor, insights, route list).
func TestScannerNotFoundPathsDoNotExhaustRouteLabels(t *testing.T) {
	h := scannerHandler(t)
	for i := range routeLabelSetCap + 10 {
		if got := serveRoute(t, h, http.MethodGet, fmt.Sprintf("/scan-%d.php", i), http.StatusNotFound); got != otherRouteLabel {
			t.Fatalf("scanner path %d labelled %q, want %q", i, got, otherRouteLabel)
		}
	}
	// A made-up method on a real path never earns a label either.
	if got := serveRoute(t, h, "PROPFIND", "/checkout", http.StatusOK); got != otherRouteLabel {
		t.Fatalf("PROPFIND /checkout labelled %q, want %q", got, otherRouteLabel)
	}
	// The first request of a real route is labelled, and a route that only
	// fails still gets its label so its error budget can be watched.
	if got := serveRoute(t, h, http.MethodGet, "/checkout", http.StatusOK); got != "GET /checkout" {
		t.Fatalf("first GET /checkout labelled %q", got)
	}
	if got := serveRoute(t, h, http.MethodGet, "/broken", http.StatusInternalServerError); got != "GET /broken" {
		t.Fatalf("GET /broken labelled %q", got)
	}
	routes, overflowed := h.RoutesFor(h.backend.(*fakeBackend).app.ID)
	served := 0
	for _, route := range routes {
		if route != reservedRouteLabelEmpty && route != otherRouteLabel {
			served++
		}
	}
	if served != 2 || overflowed {
		t.Fatalf("admitted routes = %v (overflowed %v), want only the two the app serves", routes, overflowed)
	}
}

func TestStatusEstablishesRoute(t *testing.T) {
	for status, want := range map[int]bool{
		200: true, 204: true, 301: true, 304: true, 409: true, 422: true, 429: true, 500: true, 502: true, 503: true,
		400: false, 401: false, 403: false, 404: false, 405: false, 410: false, 414: false, 431: false, 501: false, 0: false, 101: false,
	} {
		if got := statusEstablishesRoute(status); got != want {
			t.Errorf("statusEstablishesRoute(%d) = %v, want %v", status, got, want)
		}
	}
}

func TestDeclaredRoutesAreLabelledBeforeTheResponse(t *testing.T) {
	s := newRouteLabelSet()
	if got := s.labelBeforeResponse("GET /orders/{id}", false); got != "" {
		t.Fatalf("undeclared route labelled %q before its response", got)
	}
	if got := s.labelBeforeResponse("GET /orders/{id}", true); got != "GET /orders/{id}" {
		t.Fatalf("declared route labelled %q", got)
	}
	if got := s.labelBeforeResponse("GET /orders/{id}", false); got != "GET /orders/{id}" {
		t.Fatalf("admitted route labelled %q", got)
	}
}

// labelClock is a settable clock for route label sets.
type labelClock struct{ t time.Time }

func (c *labelClock) now() time.Time { return c.t }

// A full set gives a new route the slot of its least recently seen label,
// only once that label has been idle for RouteLabelReuseAfterIdle, and never
// a declared route's (ADR-20261026143210 amendment 1).
func TestFullRouteLabelSetReplacesOnlyIdleLabels(t *testing.T) {
	clock := &labelClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	s := newRouteLabelSetWithCap(3)
	s.now = clock.now
	var evicted []string
	s.onEvict = func(route string) { evicted = append(evicted, route) }
	s.admitDeclared("GET /declared")
	s.admit("GET /a")
	s.admit("GET /b")

	clock.t = clock.t.Add(30 * time.Minute)
	if got := s.admit("GET /c"); got != otherRouteLabel {
		t.Fatalf("route admitted into a full set of live labels as %q", got)
	}
	s.lookup("GET /b") // b stays in use

	clock.t = clock.t.Add(api.RouteLabelReuseAfterIdle)
	if got := s.admit("GET /c"); got != "GET /c" {
		t.Fatalf("route after the idle window labelled %q, want its own label", got)
	}
	if got := s.admit("GET /d"); got != otherRouteLabel {
		t.Fatalf("second new route labelled %q: only GET /a was idle", got)
	}
	if len(evicted) != 1 || evicted[0] != "GET /a" {
		t.Fatalf("evicted = %v, want [GET /a]", evicted)
	}
	clock.t = clock.t.Add(10 * api.RouteLabelReuseAfterIdle)
	s.admit("GET /e")
	s.admit("GET /f")
	if got := s.admit("GET /g"); got != otherRouteLabel {
		t.Fatalf("declared route was replaced: GET /g labelled %q", got)
	}
	if _, ok := s.lookup("GET /declared"); !ok {
		t.Fatal("declared route lost its label")
	}
}

// An app that answers every path with 200 earns a label for every probed
// path. Its live routes keep their labels, a new real route is labelled
// once a probe label has been idle for the window, and the replaced probe's
// series are deleted.
func TestCatchAllAppLabelsNewRoutesAfterIdleProbes(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	backend := &fakeBackend{
		app:  App{ID: uuid.NewString(), AccountID: uuid.NewString(), Plan: api.PlanPro, RouteMetricsEnabled: true},
		host: "customer.example", upstream: upstream.Listener.Addr().String(),
	}
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString()})
	metrics := NewMetrics()
	h := NewHandlerWith(backend, metrics, nil)
	h.requestTelemetry = makeTestRecorder()
	h.WithRouteMetricsEnabled(true)
	clock := &labelClock{t: time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC)}
	h.RouteSetForTest(backend.app.ID).now = clock.now

	if got := serveRoute(t, h, http.MethodGet, "/health", http.StatusOK); got != "GET /health" {
		t.Fatalf("GET /health labelled %q", got)
	}
	for i := range routeLabelSetCap + 5 {
		serveRoute(t, h, http.MethodGet, fmt.Sprintf("/probe-%02d", i), http.StatusOK)
	}
	if got := serveRoute(t, h, http.MethodGet, "/checkout", http.StatusOK); got != otherRouteLabel {
		t.Fatalf("GET /checkout labelled %q in a set full of live probes", got)
	}
	if routeSeries(t, metrics, backend.app.ID, "GET /probe-00") == 0 {
		t.Fatal("GET /probe-00 has no series before it is replaced")
	}

	clock.t = clock.t.Add(30 * time.Minute)
	serveRoute(t, h, http.MethodGet, "/health", http.StatusOK)
	clock.t = clock.t.Add(api.RouteLabelReuseAfterIdle)
	if got := serveRoute(t, h, http.MethodGet, "/checkout", http.StatusOK); got != "GET /checkout" {
		t.Fatalf("GET /checkout labelled %q after the probes went idle", got)
	}
	if got := serveRoute(t, h, http.MethodGet, "/health", http.StatusOK); got != "GET /health" {
		t.Fatalf("live GET /health lost its label: %q", got)
	}
	if n := routeSeries(t, metrics, backend.app.ID, "GET /probe-00"); n != 0 {
		t.Fatalf("replaced GET /probe-00 still has %d series", n)
	}
}

// routeSeries counts app's per-route series carrying route.
func routeSeries(t *testing.T, m *Metrics, app, route string) int {
	t.Helper()
	families, err := m.Registry().Gather()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, f := range families {
		for _, metric := range f.GetMetric() {
			var appMatch, routeMatch bool
			for _, l := range metric.GetLabel() {
				appMatch = appMatch || l.GetName() == "app" && l.GetValue() == app
				routeMatch = routeMatch || l.GetName() == "route" && l.GetValue() == route
			}
			if appMatch && routeMatch {
				n++
			}
		}
	}
	return n
}
