// adr: 20261111140000 — bound analytics methods while forwarding custom requests.
package gateway

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestCustomMethodsShareOneTelemetryMethodCategory(t *testing.T) {
	accountID, appID := uuid.NewString(), uuid.NewString()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	t.Cleanup(upstream.Close)
	backend := &fakeBackend{app: App{ID: appID, AccountID: accountID, Plan: api.PlanPro}, host: "analytics-methods.example", upstream: upstream.Listener.Addr().String()}
	backend.AddTarget(Target{NodeID: upstream.Listener.Addr().String(), InstanceID: uuid.NewString(), DeploymentID: uuid.NewString()})
	h := NewHandlerWith(backend, NewMetrics(), nil)
	h.requestTelemetry = NewRequestTelemetryRecorder(RequestTelemetryConfig{Enabled: true, RingSize: 256}, nil)
	for i := 0; i < 65; i++ {
		r := httptest.NewRequest(fmt.Sprintf("X%03d", i), "http://analytics-methods.example/", nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Fatalf("method %s returned %d: %s", r.Method, w.Code, w.Body.String())
		}
	}
	rows := h.requestTelemetry.DrainBatch(100)
	if len(rows) != 65 {
		t.Fatalf("recorded %d methods, want 65", len(rows))
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if r.Route != otherRouteLabel {
			t.Fatalf("expected folded route label, got %q", r.Route)
		}
		seen[r.Method] = true
	}
	if len(seen) != 1 || !seen["OTHER"] {
		t.Fatalf("got unbounded analytics methods: %v", seen)
	}
}
