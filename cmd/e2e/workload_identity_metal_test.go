//go:build metal

// workload_identity_metal_test.go — an app gets a workload identity token
// from guest-init's loopback endpoint (127.0.0.1:2773/oidc/token) on real
// Firecracker: guest-init forwards the request over vsock 1030, vmmd mints
// it for the instance the stream belongs to, and the token verifies against
// the signer's JWKS with this app's identity.
//
// Requires /dev/kvm, root, Firecracker on PATH and FAAS_TEST_KERNEL.
package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

func TestWorkloadIdentityMetal(t *testing.T) {
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL unset; skipping metal workload identity test")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("/dev/kvm not available: %v", err)
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "identity.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "", workloadidentity.DefaultTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	builderImg, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builderImg))
	deployBaseImg, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", helloBody)
	_ = registry.AddImage("onebox-faas/deploy-base", deployBaseImg)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.DeployWake, "FAAS_WORKLOAD_IDENTITY_KEY_PATH="+keyPath)
	apiKey := h.SeedAccount(context.Background(), api.PlanPro)
	img, _ := e2etest.IdentityProbeImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	if got := postOK(t, h, apiKey, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, apiKey, "hello")
	// vmmd keeps VMs running across a restart, so idle-park the app before
	// the harness stops or its microVM leaks on the host.
	t.Cleanup(func() {
		setAppIdleTimeout(t, h, apiKey, "hello", api.IdleTimeoutFloorSeconds)
		parkCtx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		if _, err := e2etest.WaitForAppParked(parkCtx, t, pool, appID, 60*time.Second); err != nil {
			t.Errorf("teardown: app not parked, its microVM will leak: %v", err)
		}
	})
	if body, code := doReq(t, h, apiKey, http.MethodPatch, "/v1/apps/hello", map[string]any{
		"require_authn": false, "public_auth": map[string]any{"mode": "open"},
	}); code != http.StatusOK {
		t.Fatalf("open public auth: %d %s", code, body)
	}
	raw, status := doReq(t, h, apiKey, http.MethodPost, "/v1/apps/hello/deployments", api.CreateDeploymentRequest{Image: ref})
	if status != http.StatusAccepted {
		t.Fatalf("create deployment: status=%d body=%s", status, raw)
	}
	var dep api.DeploymentResponse
	if err := json.Unmarshal(raw, &dep); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	defer h.DumpLogs(t)
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 90*time.Second); err != nil {
		t.Fatalf("deployment not live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}
	client := h.HTTPClient()
	if err := e2etest.WaitForHTTPReady(ctx, t, client, gatewayAppURL(h, "hello"), 10*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	if body, status := doGetWithHost(t, client, gatewayAppURL(h, "hello"), "hello.apps.test.example", 60*time.Second); status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
		t.Fatalf("wake = %d %q", status, body)
	}
	serving, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, 10*time.Second)
	if err != nil || len(serving) == 0 {
		t.Fatalf("no running instance: %v", err)
	}

	const audience = "sts.example.test"
	_, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/identity?audience="+audience, nil)
	if status != http.StatusOK {
		t.Fatalf("/identity = %d %s, want 200 with a token", status, body)
	}
	var token struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
	}
	if err := json.Unmarshal(body, &token); err != nil || token.AccessToken == "" {
		t.Fatalf("token response = %s (%v)", body, err)
	}
	claims, err := verifier.Verify(token.AccessToken, audience, time.Now())
	if err != nil {
		t.Fatalf("token does not verify: %v", err)
	}
	if claims.AppID != appID || claims.InstanceID != serving[0].ID {
		t.Fatalf("claims app=%s instance=%s, want app=%s instance=%s", claims.AppID, claims.InstanceID, appID, serving[0].ID)
	}
	if _, err := verifier.Verify(token.AccessToken, "other-audience", time.Now()); err == nil {
		t.Fatal("token verified for an audience it was not minted for")
	}
}
