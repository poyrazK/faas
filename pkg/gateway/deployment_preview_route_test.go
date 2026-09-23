package gateway

// adr: 122

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestDeploymentPreviewRouteWakesAndPinsExactRevision(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)
	stable := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(stable.Close)

	backend := &fakeBackend{
		app: App{
			ID: "app-1", AccountID: "acct-1", Plan: api.PlanFree,
			MaxConcurrency: 1, PinnedDeploymentID: "dep-candidate", PinnedDeploymentScope: "staging",
		},
		host:     "deploy-42-demo.gregale.dev",
		upstream: upstream.Listener.Addr().String(),
	}
	backend.AddTarget(Target{
		NodeID: stable.Listener.Addr().String(), InstanceID: "stable-1", DeploymentID: "dep-stable",
	})
	handler := NewHandlerWith(backend, NewMetrics(), nil)

	req := httptest.NewRequest(http.MethodGet, "http://deploy-42-demo.gregale.dev/", nil)
	req.Host = backend.host
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	backend.mu.Lock()
	defer backend.mu.Unlock()
	if backend.lastAdmitDeployment != "dep-candidate" || backend.lastAdmitScope != "staging" || backend.lastAdmitTrigger != sched.TriggerGateway {
		t.Fatalf("admit target=(%q, %q, %q), want (dep-candidate, staging, gateway)", backend.lastAdmitDeployment, backend.lastAdmitScope, backend.lastAdmitTrigger)
	}
	if backend.lastAdmitMax != 2 {
		t.Fatalf("admit max = %d, want steady cap plus rollout grant", backend.lastAdmitMax)
	}
}
