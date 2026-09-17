// adr: 093
package gateway

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/apihostingreceipt"
)

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
