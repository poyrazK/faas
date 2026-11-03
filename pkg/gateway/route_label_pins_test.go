// adr: 20261028110044 — amendment 4: route evidence keeps its route labels.
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
)

type fakeRouteLabelPins map[string]bool

func (p fakeRouteLabelPins) RouteLabelPinned(_ context.Context, _ App, label string) bool {
	return p[label]
}

func routeLabelTestHandler(t *testing.T, metricsOptIn bool) (*Handler, string) {
	t.Helper()
	accountID, appID := uuid.NewString(), uuid.NewString()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)
	backend := &fakeBackend{
		app:  App{ID: appID, AccountID: accountID, Plan: api.PlanPro, RouteMetricsEnabled: metricsOptIn},
		host: "customer.example", upstream: upstream.Listener.Addr().String(),
	}
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString()})
	h := NewHandlerWith(backend, NewMetrics(), nil)
	h.requestTelemetry = makeTestRecorder()
	return h, appID
}

func serveRouteLabelRequest(t *testing.T, h *Handler, method, path string) string {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(method, "http://customer.example"+path, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("%s %s status = %d, want 200", method, path, w.Code)
	}
	rows := h.requestTelemetry.DrainBatch(4)
	if len(rows) != 1 {
		t.Fatalf("%s %s: telemetry rows = %d, want one", method, path, len(rows))
	}
	return rows[0].Route
}

// An app that opted out of per-route Prometheus series still labels its
// request telemetry, which route health and monitoring read.
func TestRequestTelemetryRouteIgnoresRouteMetricsOptOut(t *testing.T) {
	h, _ := routeLabelTestHandler(t, false)
	if route := serveRouteLabelRequest(t, h, http.MethodGet, "/profiles/238"); route != "GET /profiles/{id}" {
		t.Fatalf("telemetry route = %q, want the normalized route for an app without route metrics", route)
	}
}

// A route the guard or monitor selects keeps its label in a full set; an
// unselected route still overflows.
func TestHandlerPinsSelectedRouteLabels(t *testing.T) {
	h, appID := routeLabelTestHandler(t, true)
	h.WithRouteLabelPins(fakeRouteLabelPins{"POST /checkout": true})
	set := newRouteLabelSetWithCap(1)
	set.admit("GET /busy")
	h.routeSets.Store(appID, set)
	if route := serveRouteLabelRequest(t, h, http.MethodPost, "/checkout"); route != "POST /checkout" {
		t.Fatalf("pinned route telemetry = %q, want POST /checkout", route)
	}
	if route := serveRouteLabelRequest(t, h, http.MethodGet, "/other"); route != otherRouteLabel {
		t.Fatalf("unpinned route in a full set = %q, want %q", route, otherRouteLabel)
	}
}

func TestRouteLabelSetPinnedRoutes(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := newRouteLabelSetWithCap(2)
	s.now = func() time.Time { return now }
	s.admit("GET /a")
	s.admit("GET /b")
	if got := s.admit("GET /c"); got != otherRouteLabel {
		t.Fatalf("full set admitted %q", got)
	}
	if got := s.admitPinned("POST /checkout"); got != "POST /checkout" {
		t.Fatalf("pinned route in a full, busy set = %q", got)
	}
	if _, ok := s.lookup("GET /a"); ok {
		t.Fatal("the stalest unpinned label was not displaced")
	}
	now = now.Add(2 * time.Hour)
	s.admit("GET /d") // replaces the idle GET /b
	if got := s.admit("GET /e"); got != otherRouteLabel {
		t.Fatalf("set with a live and a pinned label admitted %q", got)
	}
	if _, ok := s.lookup("POST /checkout"); !ok {
		t.Fatal("an idle pinned label was replaced")
	}

	upgraded := newRouteLabelSetWithCap(1)
	upgraded.now = func() time.Time { return now }
	upgraded.admit("GET /x")
	upgraded.admitPinned("GET /x")
	now = now.Add(2 * time.Hour)
	if got := upgraded.admit("GET /y"); got != otherRouteLabel {
		t.Fatalf("an admitted label pinned later was still replaced by %q", got)
	}
}
