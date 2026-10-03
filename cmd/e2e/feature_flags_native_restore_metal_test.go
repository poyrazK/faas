//go:build metal

package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// TestFeatureFlagsNativeParkRestoreMetal proves that a real Node app restored
// from a Firecracker snapshot refreshes stale Flags before evaluating the next
// request. The local feature-bundle transport verifies the actual vmmd-signed
// workload JWT; the bundle itself is served locally because the guest cannot
// reach the harness's loopback apid listener. The non-metal Flags acceptance
// covers the real apid runtime route and PostgreSQL bundle lookup.
func TestFeatureFlagsNativeParkRestoreMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping native Flags acceptance")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}

	identityEnv, publicJWKS := nativeFlagsIdentityFixture(t)
	jwksPath := filepath.Join(t.TempDir(), "flags-workload.jwks.json")
	if err := os.WriteFile(jwksPath, publicJWKS, 0o600); err != nil {
		t.Fatalf("write workload JWKS: %v", err)
	}
	identityEnv = append(identityEnv,
		"FAAS_FLAGS_ENABLED=1",
		"FAAS_FLAGS_WORKLOAD_JWKS_PATH="+jwksPath,
		"FAAS_FLAGS_WORKLOAD_ISSUER="+workloadidentity.DefaultIssuer,
		"FAAS_REQUEST_TELEMETRY_ENABLED=true",
	)

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	base, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", base))
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "flags-native-restore")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBase)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All, identityEnv...)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	ctx := context.Background()
	key := h.SeedAccount(ctx, api.PlanPro, "flags-native-restore-"+randHexSuffix())
	accountID := accountIDFromKey(t, ctx, pool, key)
	store := state.NewPgStore(pool)

	projectSlug := "flags-native-restore-" + randHexSuffix()
	project, err := store.CreateProject(ctx, state.Project{AccountID: accountID, Slug: projectSlug, ScanSource: state.ProjectScanSourceSingle})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	environment, err := store.ProjectEnvironmentBySlug(ctx, accountID, project.ID, "production")
	if err != nil {
		t.Fatalf("load production environment: %v", err)
	}
	slug := "flags-native-restore-" + randHexSuffix()
	app, err := store.CreateApp(ctx, state.App{
		AccountID: accountID, ProjectID: project.ID, WorkloadName: slug, Slug: slug,
		Type: state.AppTypeApp, Runtime: "node22", Status: state.AppActive,
		PlatformTenantRequired: true,
	})
	if err != nil {
		t.Fatalf("create app: %v", err)
	}
	required := string(state.ConsumerAuthModeRequired)
	requireTenant := true
	if body, status := doReq(t, h, key, http.MethodPatch, "/v1/apps/"+slug, api.UpdateAppRequest{
		ConsumerAuthMode: &required, PlatformTenantRequired: &requireTenant,
	}); status != http.StatusOK {
		t.Fatalf("require customer identity: status=%d body=%s", status, body)
	}

	tenants := store
	tenant, _, err := tenants.CreatePlatformTenant(ctx, accountID, "native-restore-customer", "Native restore customer", 100)
	if err != nil {
		t.Fatalf("create platform tenant: %v", err)
	}
	consumer, err := store.CreateAPIConsumer(ctx, accountID, app.ID, "native-restore-customer", "Native restore customer")
	if err != nil {
		t.Fatalf("create API consumer: %v", err)
	}
	if _, err := tenants.LinkPlatformTenantConsumer(ctx, accountID, tenant.ID, consumer.ID); err != nil {
		t.Fatalf("link tenant to API consumer: %v", err)
	}
	keyBody, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/consumers/"+consumer.ID+"/keys", api.CreateConsumerKeyRequest{
		Name: "native-restore", Scopes: []string{"read"},
	})
	if status != http.StatusCreated {
		t.Fatalf("create consumer key: status=%d body=%s", status, keyBody)
	}
	var consumerKey api.ConsumerKeyResponse
	if err := json.Unmarshal(keyBody, &consumerKey); err != nil {
		t.Fatalf("decode consumer key: %v", err)
	}
	if consumerKey.Key == "" {
		t.Fatal("consumer key response omitted plaintext key")
	}

	flagsPath := "/v1/projects/" + projectSlug + "/environments/production/flags"
	initialConfig := flags.Config{Groups: map[string][]string{}, Flags: []flags.Flag{{
		Key: "export", Enabled: true, Default: false,
		Rules: []flags.Rule{{ID: "selected-customer", Customers: []string{tenant.ID}, Value: true}},
	}}}
	if body, status := doReq(t, h, key, http.MethodPut, flagsPath, map[string]any{"expected_version": 0, "config": initialConfig}); status != http.StatusOK {
		t.Fatalf("publish initial Flags bundle: status=%d body=%s", status, body)
	}
	flagStore := store
	scope := state.FeatureFlagScope{AccountID: accountID, ProjectID: project.ID, EnvironmentID: environment.ID}
	initial, err := flagStore.GetFeatureFlags(ctx, scope, 0)
	if err != nil || initial.Version != 1 {
		t.Fatalf("read initial Flags bundle: version=%d err=%v", initial.Version, err)
	}
	updatedConfig := flags.Config{Groups: map[string][]string{}, Flags: []flags.Flag{{
		Key: "export", Enabled: false, Default: false, Seed: initial.Flags[0].Seed, Rules: []flags.Rule{},
	}}}
	updatedBundle := flags.Bundle{EnvironmentID: environment.ID, Version: 2, Config: updatedConfig}

	buildBody, status := postMultipartDeployment(t, h, key, slug, nativeFlagsNodeFixture(t, initial.Bundle, updatedBundle, publicJWKS, accountID, app.ID), false, "")
	if status != http.StatusAccepted {
		t.Fatalf("deploy: status=%d body=%s", status, buildBody)
	}
	depID, _ := parseQueuedDeployment(t, buildBody)
	deployCtx, cancelDeploy := context.WithTimeout(ctx, sourceDeployCtxTimeout())
	defer cancelDeploy()
	if _, _, err := e2etest.WaitForSourceDeployment(deployCtx, t, pool, depID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("source deployment did not reach live: %v", err)
	}
	if _, err := e2etest.WaitForInstanceState(deployCtx, t, pool, app.ID, state.StateParked, 90*time.Second); err != nil {
		t.Fatalf("snapshot prime did not park: %v", err)
	}
	snapshotID := waitAfterRestoreSnapshot(t, store, depID, "")
	client := h.HTTPClient()
	url := gatewayAppURL(h, slug)
	if err := e2etest.WaitForHTTPReady(deployCtx, t, client, url, 5*time.Second); err != nil {
		t.Fatalf("gateway not ready: %v", err)
	}
	host := slug + ".apps.test.example"
	spoofedTenant := "00000000-0000-4000-8000-000000000000"

	initialResponse, initialWakeID := getNativeFlagsDecision(t, client, url, host, consumerKey.Key, "/decision", spoofedTenant)
	assertNativeFlagsDecision(t, initialResponse, true, 1, "selected-customer", "rule_match")
	if initialWakeID == "" {
		t.Fatal("initial request missing x-faas-wake-id")
	}
	assertNativeFlagsWake(t, pool, initialWakeID, "restore")

	previousSnapshot := snapshotID
	latest, err := store.LatestSnapshotForTier(ctx, depID, state.SnapshotTierInit)
	if err == nil {
		previousSnapshot = latest.ID
		if err := store.MarkSnapshotStale(ctx, latest.ID); err != nil {
			t.Fatalf("retire prior snapshot: %v", err)
		}
	} else if !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("read snapshot before second park: %v", err)
	}
	if body, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/park", nil); status != http.StatusAccepted {
		t.Fatalf("park app: status=%d body=%s", status, body)
	}
	snapshotID = waitAfterRestoreSnapshot(t, store, depID, previousSnapshot)
	if _, err := e2etest.WaitForInstanceState(deployCtx, t, pool, app.ID, state.StateParked, 30*time.Second); err != nil {
		t.Fatalf("app did not park: %v", err)
	}

	if body, status := doReq(t, h, key, http.MethodPut, flagsPath, map[string]any{"expected_version": 1, "config": updatedConfig}); status != http.StatusOK {
		t.Fatalf("publish Flags update while app is parked: status=%d body=%s", status, body)
	}
	updated, err := flagStore.GetFeatureFlags(ctx, scope, 0)
	if err != nil || updated.Version != 2 {
		t.Fatalf("read updated Flags bundle: version=%d err=%v", updated.Version, err)
	}
	// The Node SDK caps stale configuration at 60 seconds. Let the VM remain
	// parked beyond that bound so the next request must use the restored guest's
	// clock and workload-identity vsock path to fetch version 2.
	time.Sleep(65 * time.Second)

	resumed, wakeID := getNativeFlagsDecision(t, client, url, host, consumerKey.Key, "/__test/publish-v2", spoofedTenant)
	assertNativeFlagsDecision(t, resumed, false, 2, "", "disabled")
	if wakeID == "" {
		t.Fatal("post-restore request missing x-faas-wake-id")
	}
	assertNativeFlagsWake(t, pool, wakeID, "restore")
	if resumed.IdentityRequests != initialResponse.IdentityRequests+1 || resumed.BundleRequests != initialResponse.BundleRequests+1 {
		t.Fatalf("stale bundle did not trigger one new signed identity/bundle fetch: initial identity=%d bundle=%d, resumed identity=%d bundle=%d",
			initialResponse.IdentityRequests, initialResponse.BundleRequests, resumed.IdentityRequests, resumed.BundleRequests)
	}
	waitForNativeFlagsEvidence(t, pool, app.ID, tenant.ID)
}

type nativeFlagsDecisionResponse struct {
	Flag             string `json:"flag"`
	Value            bool   `json:"value"`
	ConfigVersion    int64  `json:"config_version"`
	RuleID           string `json:"rule_id"`
	Reason           string `json:"reason"`
	Source           string `json:"source"`
	IdentityRequests int    `json:"identity_requests"`
	BundleRequests   int    `json:"bundle_requests"`
	Evidence         string `json:"evidence"`
}

func getNativeFlagsDecision(t *testing.T, client *http.Client, url, host, consumerKey, path, spoofedTenant string) (nativeFlagsDecisionResponse, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url+path, nil)
	if err != nil {
		t.Fatalf("create app request: %v", err)
	}
	req.Host = host
	req.Header.Set("Authorization", "Bearer "+consumerKey)
	// A caller-controlled identity must be replaced by gateway's verified
	// customer context before the SDK evaluates the targeting rule.
	req.Header.Set(api.PlatformTenantIDHeader, spoofedTenant)
	ctx, cancel := context.WithTimeout(req.Context(), 45*time.Second)
	defer cancel()
	resp, err := client.Do(req.WithContext(ctx))
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s response: %v", path, err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status=%d body=%s", path, resp.StatusCode, body)
	}
	if got := resp.Header.Get(api.FlagEvidenceHeader); got != "" {
		t.Fatalf("gateway exposed reserved flag evidence header: %q", got)
	}
	var decision nativeFlagsDecisionResponse
	if err := json.Unmarshal(body, &decision); err != nil {
		t.Fatalf("decode %s decision: %v body=%s", path, err, body)
	}
	if decision.Flag != "export" || decision.Source != "configuration" || decision.Evidence == "" {
		t.Fatalf("incomplete Flags response: %+v", decision)
	}
	return decision, resp.Header.Get("x-faas-wake-id")
}

func assertNativeFlagsDecision(t *testing.T, got nativeFlagsDecisionResponse, value bool, version int64, rule, reason string) {
	t.Helper()
	if got.Value != value || got.ConfigVersion != version || got.RuleID != rule || got.Reason != reason {
		t.Fatalf("Flags decision = %+v, want value=%t version=%d rule=%q reason=%q", got, value, version, rule, reason)
	}
}

func assertNativeFlagsWake(t *testing.T, pool *pgxpool.Pool, wakeID, method string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	if _, err := e2etest.WaitForWakeMethod(ctx, t, pool, wakeID, method, 10*time.Second); err != nil {
		t.Fatalf("wake %s: %v", wakeID, err)
	}
}

func waitForNativeFlagsEvidence(t *testing.T, pool *pgxpool.Pool, appID, tenantID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		var gotTenant string
		var evidence []byte
		err := pool.QueryRow(ctx, `
			SELECT platform_tenant_id::text, flag_evidence
			  FROM request_telemetry
			 WHERE app_id = $1::uuid AND route = 'GET /__test/publish-v2'
			   AND flag_evidence @> '[{"flag":"export","config_version":2,"used":true}]'::jsonb
			 ORDER BY received_at DESC LIMIT 1`, appID).Scan(&gotTenant, &evidence)
		if err == nil {
			if gotTenant != tenantID {
				t.Fatalf("request telemetry tenant = %q, want verified tenant %q", gotTenant, tenantID)
			}
			var items []nativeFlagEvidence
			if err := json.Unmarshal(evidence, &items); err != nil {
				t.Fatalf("decode persisted flag evidence: %v", err)
			}
			if len(items) != 1 || items[0].ConfigVersion != 2 || !items[0].Used || items[0].Value || items[0].Reason != "disabled" {
				t.Fatalf("persisted flag evidence = %+v", items)
			}
			return
		}
		if !errors.Is(err, pgx.ErrNoRows) && ctx.Err() == nil {
			t.Fatalf("query persisted Flags request evidence: %v", err)
		}
		select {
		case <-ctx.Done():
			t.Fatalf("version-2 request evidence not persisted: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

type nativeFlagEvidence struct {
	Value         bool   `json:"value"`
	ConfigVersion int64  `json:"config_version"`
	Reason        string `json:"reason"`
	Used          bool   `json:"used"`
}

func nativeFlagsIdentityFixture(t *testing.T) ([]string, []byte) {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate workload identity key: %v", err)
	}
	const keyID = "flags-native-restore"
	signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, keyID, workloadidentity.DefaultTokenTTL)
	if err != nil {
		t.Fatalf("configure workload identity signer: %v", err)
	}
	privatePath := filepath.Join(t.TempDir(), "flags-workload-identity.pem")
	privatePEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)})
	if err := os.WriteFile(privatePath, privatePEM, 0o600); err != nil {
		t.Fatalf("write workload identity key: %v", err)
	}
	publicJWKS, err := json.Marshal(signer.JWKS())
	if err != nil {
		t.Fatalf("encode workload JWKS: %v", err)
	}
	return []string{
		"FAAS_WORKLOAD_IDENTITY_KEY_PATH=" + privatePath,
		"FAAS_WORKLOAD_IDENTITY_ISSUER=" + workloadidentity.DefaultIssuer,
		"FAAS_WORKLOAD_IDENTITY_KEY_ID=" + keyID,
	}, publicJWKS
}

func nativeFlagsNodeFixture(t *testing.T, initial, updated flags.Bundle, publicJWKS []byte, accountID, appID string) []byte {
	t.Helper()
	sdkPath := filepath.Join("..", "..", "sdk", "node", "dist", "flags.js")
	sdk, err := os.ReadFile(sdkPath)
	if err != nil {
		t.Fatalf("read built Node Flags SDK at %s (run npm run build in sdk/node): %v", sdkPath, err)
	}
	initialJSON, err := json.Marshal(initial)
	if err != nil {
		t.Fatalf("encode initial Flags bundle: %v", err)
	}
	updatedJSON, err := json.Marshal(updated)
	if err != nil {
		t.Fatalf("encode updated Flags bundle: %v", err)
	}
	jwksJSON, err := json.Marshal(json.RawMessage(publicJWKS))
	if err != nil {
		t.Fatalf("encode workload JWKS for fixture: %v", err)
	}
	const packageJSON = `{"name":"gregale-flags-native-restore","version":"1.0.0","private":true,"type":"module","engines":{"node":"22"},"scripts":{"start":"node index.js"},"dependencies":{}}`
	indexJS := `import { createPublicKey, verify } from 'node:crypto';
import http from 'node:http';
import { GregaleFlags, GREGALE_FLAG_EVIDENCE_HEADER } from './vendor/flags.js';

const accountID = ` + mustJSONLiteral(t, accountID) + `;
const appID = ` + mustJSONLiteral(t, appID) + `;
const jwks = ` + string(jwksJSON) + `;
const publicKey = createPublicKey({ key: jwks.keys[0], format: 'jwk' });
const bundleV1 = ` + string(initialJSON) + `;
const bundleV2 = ` + string(updatedJSON) + `;
let activeBundle = bundleV1;
let identityRequests = 0;
let bundleRequests = 0;
const identityURL = new URL(process.env.FAAS_WORKLOAD_IDENTITY_ENDPOINT);
const nativeFetch = globalThis.fetch.bind(globalThis);
const seenTokens = new Set();

function checkedClaims(token) {
  const pieces = token.split('.');
  if (pieces.length !== 3) throw new Error('malformed JWT');
  const header = JSON.parse(Buffer.from(pieces[0], 'base64url').toString('utf8'));
  const claims = JSON.parse(Buffer.from(pieces[1], 'base64url').toString('utf8'));
  const signed = Buffer.from(pieces[0] + '.' + pieces[1]);
  if (header.alg !== 'RS256' || header.kid !== 'flags-native-restore' ||
      !verify('RSA-SHA256', signed, publicKey, Buffer.from(pieces[2], 'base64url'))) throw new Error('invalid workload JWT signature');
  const now = Math.floor(Date.now() / 1000);
  const aud = Array.isArray(claims.aud) ? claims.aud : [claims.aud];
  if (claims.iss !== 'https://identity.gregale.dev' || claims.sub !== 'app:' + appID ||
      claims.account_id !== accountID || claims.app_id !== appID ||
      !/^[0-9a-f-]{36}$/.test(claims.instance_id || '') || !aud.includes('gregale:flags') ||
      !claims.jti || seenTokens.has(claims.jti) || !Number.isSafeInteger(claims.iat) ||
      !Number.isSafeInteger(claims.exp) || claims.iat > now + 5 || claims.exp <= now ||
      claims.exp - claims.iat > 300) throw new Error('invalid workload JWT claims');
  seenTokens.add(claims.jti);
  return claims;
}

const flags = new GregaleFlags({
  apiURL: 'https://flags.fixture.test',
  fetch: async (input, init) => {
    const url = new URL(String(input));
    if (url.href === 'https://flags.fixture.test/v1/runtime/flags') {
      const bearer = new Headers(init?.headers).get('authorization') || '';
      if (!bearer.startsWith('Bearer ')) throw new Error('missing workload token');
      checkedClaims(bearer.slice('Bearer '.length));
      bundleRequests++;
      return Response.json(activeBundle, { headers: { 'cache-control': 'private, no-store' } });
    }
    if (url.origin === identityURL.origin && url.pathname === identityURL.pathname) identityRequests++;
    return nativeFetch(input, init);
  }
});
await flags.start();
flags.close();

const server = http.createServer(async (req, res) => {
  if (req.url === '/healthz') { res.writeHead(200); res.end('ready'); return; }
  if (req.url === '/__test/publish-v2') activeBundle = bundleV2;
  if (req.url !== '/decision' && req.url !== '/__test/publish-v2') { res.writeHead(404); res.end(); return; }
  try {
    await flags.runRequest(req.headers, () => {
      const decision = flags.boolean('export', false);
      flags.used('export');
      res.setHeader(GREGALE_FLAG_EVIDENCE_HEADER, flags.responseEvidence());
      res.setHeader('content-type', 'application/json');
      res.end(JSON.stringify({ ...decision, identity_requests: identityRequests, bundle_requests: bundleRequests, evidence: flags.responseEvidence() }));
    });
  } catch (error) {
    console.error('Flags request failed:', error);
    res.writeHead(500); res.end('flags failed');
  }
});
server.listen(Number(process.env.PORT || 8080), '0.0.0.0');`

	return buildTarGz(t, map[string]string{
		"package.json":     packageJSON,
		"index.js":         indexJS,
		"vendor/flags.js":  string(sdk),
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
}

func mustJSONLiteral(t *testing.T, value any) string {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode JS fixture value: %v", err)
	}
	return string(raw)
}
