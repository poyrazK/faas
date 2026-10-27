// public_auth_basic_e2e_test.go — issue #1839: Basic auth on the live path.
//
// The customer sets public_auth mode=basic through apid, which seals the
// credential to the fleet recipient; gatewayd-internal opens it with the
// fleet identity it loads from FAAS_HOST_KEY_PATH's directory, as the
// production unit's LoadCredential does. Requests arrive through
// gatewayd-public. Twice this broke between the two processes while each
// side's unit tests passed: first the unsealer was never wired (500), then
// apid and the gateway disagreed on the seal namespace (exact credentials
// got 401, credential_unavailable).

package e2e_test

import (
	"encoding/base64"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

// basicAuthFleetEnv writes a fleet identity and its recipient where apid and
// gatewayd-internal read them in production, and returns the daemon env.
func basicAuthFleetEnv(t *testing.T) []string {
	t.Helper()
	fleet, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	host, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := secretbox.WriteHostKeyAtPath(filepath.Join(dir, "fleet.age"), fleet); err != nil {
		t.Fatal(err)
	}
	if err := secretbox.WriteHostKeyAtPath(filepath.Join(dir, "host.age"), host); err != nil {
		t.Fatal(err)
	}
	recipientPath := filepath.Join(dir, "fleet.age.pub")
	if err := secretbox.WriteRecipientFile(recipientPath, fleet); err != nil {
		t.Fatal(err)
	}
	return []string{
		"FAAS_FLEET_AGE_RECIPIENT_PATH=" + recipientPath,
		"FAAS_HOST_KEY_PATH=" + filepath.Join(dir, "host.age"),
	}
}

func setBasicAuth(t *testing.T, f *normalPathFixture, user, pass string) {
	t.Helper()
	body, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.app.Slug, api.UpdateAppRequest{
		PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeBasic, BasicUser: user, BasicPass: pass},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH public_auth basic: status=%d body=%s", status, body)
	}
}

func basicAuthHeader(user, pass string) map[string]string {
	return map[string]string{"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+pass))}
}

// waitForBasicAuthStatus polls the public edge until a request with headers
// answers want; app changes reach the gateway asynchronously.
func waitForBasicAuthStatus(t *testing.T, f *normalPathFixture, what string, headers map[string]string, want int) (http.Header, []byte) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		header, body, status := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil, headers)
		if status == want {
			return header, body
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s: status=%d body=%q, want %d", what, status, body, want)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestE2E_NormalPath_PublicBasicAuthOpensSealedCredentials(t *testing.T) {
	f := newNormalPathFixtureWithPlanAndEnv(t, "basic-auth", api.PlanPro, basicAuthFleetEnv(t)...)
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "v1")
	f.vmmd.SetVersion(instance.ID, "v1")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:v1\n", 10*time.Second)

	setBasicAuth(t, f, "editor", "first-pass")
	header, _ := waitForBasicAuthStatus(t, f, "anonymous after mode=basic", nil, http.StatusUnauthorized)
	if got := header.Get("WWW-Authenticate"); got != `Basic realm="apps"` {
		t.Errorf("anonymous WWW-Authenticate = %q, want a Basic challenge", got)
	}
	if _, body, status := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil, basicAuthHeader("editor", "wrong")); status != http.StatusUnauthorized {
		t.Fatalf("wrong password: status=%d body=%q, want 401", status, body)
	}
	if _, body := waitForBasicAuthStatus(t, f, "exact credentials", basicAuthHeader("editor", "first-pass"), http.StatusOK); string(body) != "normal-path:v1\n" {
		t.Fatalf("exact credentials body = %q, want the app's response", body)
	}

	// Rotation takes effect without restarting the gateway: the new password
	// opens the app and the old one stops working.
	setBasicAuth(t, f, "editor", "second-pass")
	waitForBasicAuthStatus(t, f, "rotated password", basicAuthHeader("editor", "second-pass"), http.StatusOK)
	if _, body, status := doReqHeaders(t, f.h, f.host, http.MethodGet, "/", nil, basicAuthHeader("editor", "first-pass")); status != http.StatusUnauthorized {
		t.Fatalf("old password after rotation: status=%d body=%q, want 401", status, body)
	}

	body, status := doReq(t, f.h, f.key, http.MethodPatch, "/v1/apps/"+f.app.Slug, api.UpdateAppRequest{
		PublicAuth: &api.PublicAuthBlock{Mode: api.AppPublicAuthModeOpen},
	})
	if status != http.StatusOK {
		t.Fatalf("PATCH public_auth open: status=%d body=%s", status, body)
	}
	waitForBasicAuthStatus(t, f, "anonymous after mode=open", nil, http.StatusOK)
}
