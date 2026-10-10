// adr: 945
package gateway

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/routeprobe"
)

type routeProbeBackend struct {
	*fakeBackend
	deploymentID, token string
}

func (b *routeProbeBackend) ValidateRouteProbe(appID, deploymentID, token string) bool {
	return appID == b.app.ID && deploymentID == b.deploymentID && token == b.token
}

func TestRouteProbeChallengeIsASeparateKind(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	b.AuthorizeRouteProbe("app-1", "dep-1", "probe-token", time.Now().Add(time.Minute))
	if !b.ValidateRouteProbe("app-1", "dep-1", "probe-token") {
		t.Fatal("matching probe challenge rejected")
	}
	if b.ValidateDeploymentSmoke("app-1", "dep-1", "probe-token") {
		t.Fatal("probe token authorized the smoke bypass")
	}
	b.AuthorizeDeploymentSmoke("app-1", "dep-1", "smoke-token", time.Now().Add(time.Minute))
	if b.ValidateRouteProbe("app-1", "dep-1", "smoke-token") {
		t.Fatal("smoke token authorized a route probe")
	}
	for _, m := range [][3]string{{"app-2", "dep-1", "probe-token"}, {"app-1", "dep-2", "probe-token"}, {"app-1", "dep-1", "forged"}} {
		if b.ValidateRouteProbe(m[0], m[1], m[2]) {
			t.Fatalf("mismatched probe accepted: %v", m)
		}
	}
	b.AuthorizeRouteProbe("app-1", "dep-old", "expired", time.Now().Add(-time.Second))
	if b.ValidateRouteProbe("app-1", "dep-old", "expired") {
		t.Fatal("expired probe accepted")
	}
}

type probeUpstream struct {
	mu      sync.Mutex
	hits    int
	headers http.Header
}

func newProbeUpstream(t *testing.T, p *probeUpstream, status int) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.mu.Lock()
		p.hits++
		p.headers = r.Header.Clone()
		p.mu.Unlock()
		w.WriteHeader(status)
	}))
	t.Cleanup(server.Close)
	return server
}

func routeProbeHandler(t *testing.T, app App, stableStatus, candidateStatus int) (*Handler, *probeUpstream, *probeUpstream) {
	t.Helper()
	stable, candidate := &probeUpstream{}, &probeUpstream{}
	stableServer := newProbeUpstream(t, stable, stableStatus)
	candidateServer := newProbeUpstream(t, candidate, candidateStatus)
	fake := &fakeBackend{app: app, host: "demo.apps.dom", upstream: stableServer.Listener.Addr().String()}
	fake.AddTarget(Target{NodeID: stableServer.Listener.Addr().String(), InstanceID: "stable-1", DeploymentID: "dep-stable"})
	fake.AddTarget(Target{NodeID: candidateServer.Listener.Addr().String(), InstanceID: "candidate-1", DeploymentID: "dep-candidate"})
	h := NewHandlerWith(&routeProbeBackend{fakeBackend: fake, deploymentID: "dep-candidate", token: "probe-token"}, NewMetrics(), nil)
	return h, stable, candidate
}

func probeRequest(token string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "http://demo.apps.dom/reports/7", nil)
	req.Host = "demo.apps.dom"
	req.Header.Set(routeprobe.DeploymentHeader, "dep-candidate")
	req.Header.Set(routeprobe.TokenHeader, token)
	return req
}

func TestRouteProbePinsDeploymentProvesResponseAndStripsHeaders(t *testing.T) {
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), Plan: api.PlanPro, MaxConcurrency: 2}
	h, stable, candidate := routeProbeHandler(t, app, http.StatusOK, http.StatusNoContent)
	h.requestTelemetry = makeTestRecorder()
	for i := 0; i < 3; i++ {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, probeRequest("probe-token"))
		if rec.Code != http.StatusNoContent {
			t.Fatalf("probe status = %d, want the candidate's 204; body=%s", rec.Code, rec.Body.String())
		}
		if got, want := rec.Header().Get(apihostingreceipt.ServedResponseHeader), apihostingreceipt.CandidateResponseProof("dep-candidate", "probe-token"); got != want {
			t.Fatalf("probe proof = %q, want %q", got, want)
		}
	}
	candidate.mu.Lock()
	defer candidate.mu.Unlock()
	if candidate.hits != 3 || stable.hits != 0 {
		t.Fatalf("hits candidate=%d stable=%d, want every probe pinned to the candidate", candidate.hits, stable.hits)
	}
	if candidate.headers.Get(routeprobe.TokenHeader) != "" || candidate.headers.Get(routeprobe.DeploymentHeader) != "" {
		t.Fatalf("probe headers reached the app: %v", candidate.headers)
	}
	if rows := h.requestTelemetry.DrainBatch(64); len(rows) != 0 {
		t.Fatalf("route probes wrote %d request telemetry rows", len(rows))
	}
}

func TestRouteProbeInvalidTokenIsRefusedWithoutTelemetry(t *testing.T) {
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), Plan: api.PlanPro, MaxConcurrency: 2}
	h, stable, candidate := routeProbeHandler(t, app, http.StatusOK, http.StatusNoContent)
	h.requestTelemetry = makeTestRecorder()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, probeRequest("forged"))
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get(apihostingreceipt.ServedResponseHeader) != "" {
		t.Fatalf("unauthorized probe = %d with proof %q, want a 503 refusal", rec.Code, rec.Header().Get(apihostingreceipt.ServedResponseHeader))
	}
	if candidate.hits != 0 || stable.hits != 0 {
		t.Fatalf("unauthorized probe reached the app: candidate=%d stable=%d", candidate.hits, stable.hits)
	}
	if rows := h.requestTelemetry.DrainBatch(64); len(rows) != 0 {
		t.Fatalf("unauthorized probe wrote %d telemetry rows", len(rows))
	}
}

func TestRouteProbeKeepsCustomerAuthGates(t *testing.T) {
	app := App{ID: uuid.NewString(), AccountID: uuid.NewString(), Plan: api.PlanPro, MaxConcurrency: 2, RequireAuthn: true}
	h, stable, candidate := routeProbeHandler(t, app, http.StatusOK, http.StatusNoContent)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, probeRequest("probe-token"))
	// Without a configured authenticator the gate fails closed (503); either
	// way the probe must be refused before reaching the app.
	if rec.Code < http.StatusBadRequest {
		t.Fatalf("probe bypassed require_authn: status %d", rec.Code)
	}
	if candidate.hits != 0 || stable.hits != 0 {
		t.Fatalf("unauthenticated probe reached the app: candidate=%d stable=%d", candidate.hits, stable.hits)
	}
}
