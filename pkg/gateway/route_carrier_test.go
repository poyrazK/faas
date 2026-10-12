// spec: §12
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// production-us hunt #9: the app errors recorder wraps the handler, so it
// never saw a route and fingerprinted every raw path as its own issue. The
// handler fills a planted carrier with the bounded route even when no other
// route consumer (metrics, telemetry, audit) is enabled.
func TestHandlerFillsTheObservedRouteCarrier(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)
	backend := &fakeBackend{
		app:  App{ID: uuid.NewString(), AccountID: uuid.NewString(), Plan: api.PlanPro},
		host: "customer.example", upstream: upstream.Listener.Addr().String(),
	}
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString()})
	h := NewHandlerWith(backend, NewMetrics(), nil)

	r, carrier := WithObservedRouteCarrier(httptest.NewRequest(http.MethodGet, "http://customer.example/orders/12345", nil))
	h.ServeHTTP(httptest.NewRecorder(), r)
	if got := carrier.Route(); got != "GET /orders/{id}" {
		t.Fatalf("carrier route = %q, want the identifier-inferred route", got)
	}
}

func TestObservedRouteLabelBoundsUnlabelledPaths(t *testing.T) {
	for path, want := range map[string]string{
		"/orders/12345": "GET /orders/{id}",
		"/healthz":      "GET /healthz",
		"/" + string(make([]byte, 300)): otherRouteLabel,
	} {
		if got := ObservedRouteLabel(http.MethodGet, path); got != want {
			t.Errorf("ObservedRouteLabel(GET, %.20q) = %q, want %q", path, got, want)
		}
	}
	if (*ObservedRouteCarrier)(nil).Route() != "" {
		t.Fatal("nil carrier reported a route")
	}
}
