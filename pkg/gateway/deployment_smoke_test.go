// adr: 093
package gateway

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
	"github.com/onebox-faas/faas/pkg/sched"
)

type deploymentSmokeRoutingBackend struct {
	*fakeBackend
	deploymentID string
	token        string
}

func (b *deploymentSmokeRoutingBackend) ValidateDeploymentSmoke(appID, deploymentID, token string) bool {
	return appID == b.app.ID && deploymentID == b.deploymentID && token == b.token
}

func TestDeploymentSmokeChallengeIsBoundAndExpires(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	b.AuthorizeDeploymentSmoke("app-1", "dep-1", "secret-token", time.Now().Add(time.Minute))
	if !b.ValidateDeploymentSmoke("app-1", "dep-1", "secret-token") {
		t.Fatal("matching challenge was rejected")
	}
	for _, mismatch := range [][3]string{{"app-2", "dep-1", "secret-token"}, {"app-1", "dep-2", "secret-token"}, {"app-1", "dep-1", "wrong"}} {
		if b.ValidateDeploymentSmoke(mismatch[0], mismatch[1], mismatch[2]) {
			t.Fatalf("mismatched challenge accepted: %v", mismatch)
		}
	}
	b.AuthorizeDeploymentSmoke("app-1", "dep-old", "expired", time.Now().Add(-time.Second))
	if b.ValidateDeploymentSmoke("app-1", "dep-old", "expired") {
		t.Fatal("expired challenge was accepted")
	}
}

func TestConcurrentDeploymentSmokeChallengesDoNotOverwrite(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	expiresAt := time.Now().Add(time.Minute)
	b.AuthorizeDeploymentSmoke("app-1", "dep-1", "token-a", expiresAt)
	b.AuthorizeDeploymentSmoke("app-1", "dep-1", "token-b", expiresAt)

	for _, token := range []string{"token-a", "token-b"} {
		if !b.ValidateDeploymentSmoke("app-1", "dep-1", token) {
			t.Fatalf("concurrent challenge %q was overwritten", token)
		}
	}
}

func TestAuthorizedDeploymentSmokeRequiresCachedChallenge(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	h := &Handler{backend: b}
	app := App{ID: "app-1"}
	req := httptest.NewRequest("GET", "http://demo/healthz", nil)
	req.Header.Set(apihostingreceipt.PlatformSmokeHeader, "1")
	req.Header.Set(apihostingreceipt.PlatformSmokeDeploymentHeader, "dep-1")
	req.Header.Set(apihostingreceipt.PlatformSmokeTokenHeader, "token-1")
	if h.authorizedDeploymentSmoke(req, app) {
		t.Fatal("uncached public marker bypassed edge health")
	}
	b.AuthorizeDeploymentSmoke(app.ID, "dep-1", "token-1", time.Now().Add(time.Minute))
	if !h.authorizedDeploymentSmoke(req, app) {
		t.Fatal("authorized platform smoke was rejected")
	}
}

func TestAuthorizedDeploymentSmokeBypassesCustomerAuthGates(t *testing.T) {
	b := NewPGBackend(nil, nil, nil)
	h := &Handler{backend: b}
	app := App{
		ID:           "app-1",
		RequireAuthn: true,
		PublicAuth:   PublicAuthConfig{Mode: publicAuthModeBearer},
	}
	b.AuthorizeDeploymentSmoke(app.ID, "dep-1", "token-1", time.Now().Add(time.Minute))

	req := httptest.NewRequest(http.MethodGet, "http://demo/healthz", nil)
	req.Header.Set(apihostingreceipt.PlatformSmokeHeader, "1")
	req.Header.Set(apihostingreceipt.PlatformSmokeDeploymentHeader, "dep-1")
	req.Header.Set(apihostingreceipt.PlatformSmokeTokenHeader, "token-1")

	for name, enforce := range map[string]func(http.ResponseWriter, *http.Request, *statusRecorder, App) bool{
		"require_authn": h.enforceRequireAuthn,
		"public_auth":   h.enforcePublicAuth,
	} {
		t.Run(name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			status := &statusRecorder{ResponseWriter: recorder}
			if !enforce(status, req, status, app) {
				t.Fatalf("authorized platform smoke was rejected with status %d", recorder.Code)
			}
		})
	}
}

func TestAuthorizedDeploymentSmokeWakesAndPinsCandidate(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(upstream.Close)

	fake := &fakeBackend{
		app: App{
			ID: "app-1", AccountID: "acct-1", Plan: api.PlanFree,
			MaxConcurrency: 1, HealthPath: "/healthz",
		},
		host:     "demo.apps.dom",
		upstream: upstream.Listener.Addr().String(),
	}
	fake.AddTarget(Target{
		NodeID: upstream.Listener.Addr().String(), InstanceID: "stable-1", DeploymentID: "dep-stable",
	})
	backend := &deploymentSmokeRoutingBackend{fakeBackend: fake, deploymentID: "dep-candidate", token: "secret"}
	h := NewHandlerWith(backend, NewMetrics(), nil)

	req := httptest.NewRequest(http.MethodGet, "http://demo.apps.dom/healthz", nil)
	req.Host = "demo.apps.dom"
	req.Header.Set(apihostingreceipt.PlatformSmokeHeader, "1")
	req.Header.Set(apihostingreceipt.PlatformSmokeDeploymentHeader, backend.deploymentID)
	req.Header.Set(apihostingreceipt.PlatformSmokeTokenHeader, backend.token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get(api.DeploymentIDHeader); got != backend.deploymentID {
		t.Fatalf("deployment header = %q, want %q", got, backend.deploymentID)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	if fake.lastAdmitDeployment != backend.deploymentID || fake.lastAdmitTrigger != sched.TriggerDeploymentSmoke {
		t.Fatalf("admit target=(%q, %q), want (%q, %q)", fake.lastAdmitDeployment, fake.lastAdmitTrigger, backend.deploymentID, sched.TriggerDeploymentSmoke)
	}
	if fake.lastAdmitMax != 2 {
		t.Fatalf("admit max = %d, want one stable plus one candidate", fake.lastAdmitMax)
	}
}

func TestDeploymentSmokeCandidatePickerDoesNotUseStableSibling(t *testing.T) {
	fake := &fakeBackend{app: App{ID: "app-1"}}
	fake.AddTarget(Target{InstanceID: "stable-1", DeploymentID: "dep-stable"})
	backend := &deploymentSmokeRoutingBackend{fakeBackend: fake, deploymentID: "dep-candidate", token: "secret"}
	h := &Handler{backend: backend}
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "http://demo/healthz", nil)
	req.Header.Set(apihostingreceipt.PlatformSmokeHeader, "1")
	req.Header.Set(apihostingreceipt.PlatformSmokeDeploymentHeader, backend.deploymentID)
	req.Header.Set(apihostingreceipt.PlatformSmokeTokenHeader, backend.token)

	deploymentID, ok := h.authorizedDeploymentSmokeTarget(req, fake.app)
	if !ok || deploymentID != backend.deploymentID {
		t.Fatalf("authorized target = (%q, %v)", deploymentID, ok)
	}
	if pick := backend.PickForDeployment(fake.app.ID, deploymentID); pick.OK {
		t.Fatalf("candidate picker fell back to stable target: %+v", pick)
	}
}
