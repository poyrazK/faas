//go:build metal

// workload_identity_metal_test.go — an app gets a workload identity token
// from guest-init's loopback endpoint (127.0.0.1:2773/oidc/token) on real
// Firecracker: guest-init forwards the request over vsock 1030, vmmd mints
// it for the instance the stream belongs to, and the token verifies against
// the signer's JWKS with this app's identity. vmmd and apid are configured
// the way the ADR-1141 drop-ins configure them (key path + thumbprint key id;
// cluster JWKS), and a gregale:queues token leases the app's pull queue
// through apid (ADR-1140), recording the serving instance on the lease.
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

	"github.com/jackc/pgx/v5/pgxpool"
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
	signer, identityEnv := workloadIdentityTrust(t)
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

	h := e2etest.Start(t, pool, e2etest.DeployWake, identityEnv...)
	apiKey := h.SeedAccount(context.Background(), api.PlanPro)
	img, _ := e2etest.IdentityProbeImageAboveBase("library/hello", helloBody)
	ref := registry.AddImage("library/hello", img)
	falsy := false
	if got := postOK(t, h, apiKey, "/v1/apps", api.CreateAppRequest{Slug: "hello", Type: "app", RequireAuthn: &falsy}); got != http.StatusCreated {
		t.Fatalf("create app: status=%d", got)
	}
	appID := mustGetAppID(t, h, apiKey, "hello")
	// A pull binding for the lease check. The fixture must stay an HTTP app
	// to serve /identity through the gateway, and the binding API refuses a
	// worker-class binding on an HTTP app, so the row is written through the
	// store: the lease path only needs an enabled pull binding on the app.
	store := state.NewPgStore(pool)
	app, err := store.AppByID(context.Background(), appID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateQueueBinding(context.Background(), state.QueueBinding{AccountID: app.AccountID, AppID: appID,
		Name: "jobs", QueueName: "jobs", Mode: "pull", WorkloadClass: state.WorkloadClassWorker, Enabled: true, MaxConcurrency: 2}); err != nil {
		t.Fatalf("create pull binding: %v", err)
	}
	// vmmd keeps VMs running across a restart, so idle-park the app before
	// the harness stops or its microVM leaks on the host.
	t.Cleanup(func() {
		setAppIdleTimeout(t, h, apiKey, "hello", api.IdleTimeoutFloorSeconds)
		parkCtx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
		defer cancel()
		if _, err := e2etest.WaitForAppParked(parkCtx, t, pool, appID, 4*time.Minute); err != nil {
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
	// Imaging (layer build, secret and CVE scans, base patch) takes minutes on a
	// loaded nested-virt host; the deadlines bound a hang, not a fast path.
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	defer h.DumpLogs(t)
	if _, err := e2etest.WaitForDeploymentLive(ctx, t, pool, dep.ID, 12*time.Minute); err != nil {
		t.Fatalf("deployment not live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateParked, 4*time.Minute); err != nil {
		t.Fatalf("no parked instance: %v", err)
	}
	client := h.HTTPClient()
	if err := e2etest.WaitForHTTPReady(ctx, t, client, gatewayAppURL(h, "hello"), 3*time.Minute); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	if body, status := doGetWithHost(t, client, gatewayAppURL(h, "hello"), "hello.apps.test.example", 3*time.Minute); status != http.StatusOK || strings.TrimSpace(string(body)) != helloBody {
		t.Fatalf("wake = %d %q", status, body)
	}
	serving, err := e2etest.WaitForInstanceState(ctx, t, pool, appID, state.StateRunning, time.Minute)
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
	assertWorkloadTokenLeasesQueue(t, h, pool, apiKey, appID, serving[0].ID)
}

// workloadIdentityTrust writes a signing key, its thumbprint key id and the
// JWKS the way gregalectl workload-identity init does (ADR-1141), and returns
// the signer with the daemon environment that points vmmd and apid at them.
func workloadIdentityTrust(t *testing.T) (*workloadidentity.Signer, []string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(t.TempDir(), "identity.pem")
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	if err := os.WriteFile(keyPath, pemBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	jwk, err := workloadidentity.PublicJWK(&key.PublicKey)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, jwk.KeyID, workloadidentity.DefaultTokenTTL)
	if err != nil {
		t.Fatal(err)
	}
	jwks, err := json.Marshal(signer.JWKS())
	if err != nil {
		t.Fatal(err)
	}
	jwksPath := filepath.Join(t.TempDir(), "workload-identity.jwks.json")
	if err := os.WriteFile(jwksPath, jwks, 0o444); err != nil {
		t.Fatal(err)
	}
	return signer, []string{"FAAS_WORKLOAD_IDENTITY_KEY_PATH=" + keyPath,
		"FAAS_WORKLOAD_IDENTITY_KEY_ID=" + jwk.KeyID, "FAAS_WORKLOAD_JWKS_PATH=" + jwksPath}
}

// assertWorkloadTokenLeasesQueue fetches a gregale:queues token inside the
// guest and leases the app's pull queue with it through apid's cluster
// workload trust (ADR-1140, ADR-1141).
func assertWorkloadTokenLeasesQueue(t *testing.T, h *e2etest.Harness, pool *pgxpool.Pool, apiKey, appID, instanceID string) {
	t.Helper()
	_, body, status := doReqHeaders(t, h, "hello.apps.test.example", http.MethodGet, "/identity?audience="+workloadidentity.QueuesAudience, nil)
	if status != http.StatusOK {
		t.Fatalf("/identity queues = %d %s", status, body)
	}
	var token struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(body, &token); err != nil || token.AccessToken == "" {
		t.Fatalf("queues token response = %s (%v)", body, err)
	}
	sent, code := doReq(t, h, apiKey, http.MethodPost, "/v1/apps/hello/queues/send", api.QueueSendRequest{
		QueueName: "jobs", Payload: json.RawMessage(`{"job":"resize"}`),
	})
	if code != http.StatusCreated {
		t.Fatalf("queue send: %d %s", code, sent)
	}
	var queued api.QueueSendResponse
	if err := json.Unmarshal(sent, &queued); err != nil || queued.ID == "" {
		t.Fatalf("queue send response %s (%v)", sent, err)
	}
	leased, code := doReq(t, h, token.AccessToken, http.MethodPost, "/v1/apps/hello/queues/jobs/leases", api.QueueLeaseRequest{
		MaxMessages: 10, WaitSeconds: 5,
	})
	if code != http.StatusOK {
		t.Fatalf("lease with the workload token: %d %s", code, leased)
	}
	var lease api.QueueLeaseResponse
	if err := json.Unmarshal(leased, &lease); err != nil || len(lease.Messages) != 1 || lease.Messages[0].ID != queued.ID {
		t.Fatalf("lease response %s (%v), want message %s", leased, err, queued.ID)
	}
	inv, err := state.NewPgStore(pool).InvocationByID(context.Background(), queued.ID)
	if err != nil || inv.State != state.InvocationDispatching || inv.InstanceID != instanceID || inv.AppID != appID {
		t.Fatalf("leased row %+v (%v), want dispatching on instance %s", inv, err, instanceID)
	}
	acked, code := doReq(t, h, token.AccessToken, http.MethodPost, "/v1/apps/hello/queues/jobs/leases:ack", api.QueueLeaseReceiptsRequest{
		Receipts: []string{lease.Messages[0].Receipt},
	})
	var outcome api.QueueLeaseReceiptsResponse
	if code != http.StatusOK || json.Unmarshal(acked, &outcome) != nil || len(outcome.Succeeded) != 1 {
		t.Fatalf("ack: %d %s", code, acked)
	}
	// A JWT-shaped bearer that vmmd did not sign is refused.
	if _, code := doReq(t, h, "eyJhbGciOiJSUzI1NiJ9.e30.c2ln", http.MethodPost, "/v1/apps/hello/queues/jobs/leases", api.QueueLeaseRequest{}); code != http.StatusUnauthorized {
		t.Fatalf("forged workload token: status %d, want 401", code)
	}
}
