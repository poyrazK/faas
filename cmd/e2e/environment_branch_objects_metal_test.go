//go:build metal

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestEnvironmentBranchContainedObjectsMetal proves ADR-20261031193635 slice
// 6c on a real guest: a contained branch reads and writes its overlay bucket
// through the bridge object relay, and a parent key is refused at the relay.
func TestEnvironmentBranchContainedObjectsMetal(t *testing.T) {
	if !metalAvailable(t) {
		return
	}
	if os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("FAAS_BUILDER_BASE_PATH unset; skipping environment branch acceptance")
	}
	t.Setenv(api.EnvironmentBranchContainmentEnv, "1")
	t.Setenv(api.EnvironmentBranchContainedObjectsEnv, "1")
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	store := state.NewPgStore(pool)
	objects := startBranchObjectGateway(t, store)

	registry := e2etest.NewFakeRegistry()
	t.Cleanup(func() { registry.Close() })
	base, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", base))
	deployBase, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "environment-branch-objects")
	_ = registry.AddImage("onebox-faas/deploy-base", deployBase)
	e2etest.OverrideDeployBase(t, registry.Host()+"/onebox-faas/deploy-base:latest")

	h := e2etest.Start(t, pool, e2etest.All, objects.env...)
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
	})
	label := "objects" + randHexSuffix()
	key := h.SeedAccount(ctx, api.PlanPro, label)
	account, err := store.AccountByEmail(ctx, "e2e+pro+"+label+"@test.example")
	if err != nil {
		t.Fatalf("load seeded account: %v", err)
	}
	releaseEnvironmentBranches(t, store)
	releaseRuntimeFlag(t, store, "s3_enabled")

	slug := "objects-web-" + randHexSuffix()
	falsy := false
	if status := postOK(t, h, key, "/v1/apps", api.CreateAppRequest{Slug: slug, Type: "app", RequireAuthn: &falsy}); status != http.StatusCreated {
		t.Fatalf("create app: status=%d", status)
	}
	appID := mustGetAppID(t, h, key, slug)
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: "objects-" + randHexSuffix(),
		ProductionBranch: "main", ScanSource: state.ProjectScanSourceConvention})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	// Fixture setup: attach the API-created app to the project as a workload.
	if _, err := pool.Exec(ctx, `update apps set project_id = $1, workload_name = 'web' where id = $2`, project.ID, appID); err != nil {
		t.Fatalf("attach app to project: %v", err)
	}
	raw, status := doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/buckets", map[string]any{"name": "assets", "scope": "production"})
	var bucket api.ObjectBucket
	if status != http.StatusCreated || json.Unmarshal(raw, &bucket) != nil {
		t.Fatalf("create bucket: status=%d body=%s", status, raw)
	}
	raw, status = doReq(t, h, key, http.MethodPost, "/v1/apps/"+slug+"/buckets/"+bucket.ID+"/compute-bindings",
		api.CreateObjectStorageComputeBindingRequest{Permission: api.ObjectBucketPermissionReadWrite})
	var binding api.ObjectStorageComputeBinding
	if status != http.StatusCreated || json.Unmarshal(raw, &binding) != nil {
		t.Fatalf("bind bucket: status=%d body=%s", status, raw)
	}
	keys := object.ObjectStorageBindingSecretKeys(binding.Prefix)

	buildBody, status := postMultipartDeployment(t, h, key, slug, environmentBranchObjectsFixture(t), false, "")
	if status != http.StatusAccepted {
		t.Fatalf("deploy: status=%d body=%s", status, buildBody)
	}
	parentID, _ := parseQueuedDeployment(t, buildBody)
	deployCtx, cancel := context.WithTimeout(t.Context(), sourceDeployCtxTimeout())
	defer cancel()
	if _, _, err := e2etest.WaitForSourceDeployment(deployCtx, t, pool, parentID, e2etest.DefaultBuildStallWindow, e2etest.DefaultBuildCeiling); err != nil {
		t.Fatalf("parent deployment did not reach live: %v", err)
	}

	raw, status = doReq(t, h, key, http.MethodPost, "/v1/projects/"+project.Slug+"/environments/production/branches",
		api.CreateProjectEnvironmentBranchRequest{Slug: "pr-1", Egress: api.EnvironmentBranchEgressContained},
		map[string]string{"Idempotency-Key": "objects-" + randHexSuffix()})
	if status != http.StatusCreated {
		t.Fatalf("create contained branch: status=%d body=%s", status, raw)
	}
	var created api.ProjectEnvironmentResponse
	if err := json.Unmarshal(raw, &created); err != nil || created.Branch == nil || len(created.Branch.Workloads) != 1 {
		t.Fatalf("contained branch response: %v (%s)", err, raw)
	}
	waitDeploymentLive(t, pool, created.Branch.Workloads[0].DeploymentID)
	branch := waitBranchDataReady(t, store, account.ID, project.ID, "pr-1")
	owned, err := store.ListProjectEnvironmentBranchBuckets(ctx, branch.EnvironmentID)
	if err != nil || len(owned) != 1 || owned[0].CopyBucketID == "" {
		t.Fatalf("branch buckets = %+v, %v", owned, err)
	}
	copyBucket, err := store.GetObjectBucket(ctx, account.ID, appID, owned[0].CopyBucketID)
	if err != nil {
		t.Fatal(err)
	}

	secret := func(scope, name string) string {
		t.Helper()
		row, err := store.GetAppSecretInScope(ctx, account.ID, appID, scope, name)
		if err != nil {
			t.Fatalf("%s secret %s: %v", scope, name, err)
		}
		envelope, _, err := secretbox.OpenMultiBound([]*age.X25519Identity{objects.identity}, row.Ciphertext)
		if err != nil {
			t.Fatalf("open %s secret %s: %v", scope, name, err)
		}
		return envelope[name]
	}
	branchCredential := aws.Credentials{AccessKeyID: secret("pr-1", keys.AccessKeyID), SecretAccessKey: secret("pr-1", keys.SecretAccessKey)}
	parentCredential := aws.Credentials{AccessKeyID: secret("production", keys.AccessKeyID), SecretAccessKey: secret("production", keys.SecretAccessKey)}
	branchBucket, parentBucket := secret("pr-1", keys.Bucket), secret("production", keys.Bucket)

	client := h.HTTPClient()
	host := environmentBranchHost(t, store, account.ID, project.ID, "pr-1", appID)
	guest := func(path string) string {
		t.Helper()
		body, _, status := branchGet(t, client, gatewayAppURL(h, slug)+strings.TrimPrefix(path, "/"), host, key)
		if status != http.StatusOK {
			t.Fatalf("guest %s: status=%d body=%q", path, status, body)
		}
		return strings.TrimSpace(string(body))
	}
	if got := guest("/env?k=" + url.QueryEscape(keys.Endpoint)); got != api.EnvironmentBranchObjectRelayEndpoint() {
		t.Fatalf("branch endpoint = %q, want the relay %q", got, api.EnvironmentBranchObjectRelayEndpoint())
	}
	fetch := func(method, target, body string) string {
		t.Helper()
		q := url.Values{"method": {method}, "url": {target}, "body": {body}}
		return guest("/fetch?" + q.Encode())
	}
	objectKey := "relay/" + randHexSuffix() + ".txt"
	if got := fetch(http.MethodPut, presignRelayURL(t, branchCredential, http.MethodPut, branchBucket, objectKey), "through-the-relay"); !strings.HasPrefix(got, "status=200") {
		t.Fatalf("branch PUT through the relay = %q", got)
	}
	if got := fetch(http.MethodGet, presignRelayURL(t, branchCredential, http.MethodGet, branchBucket, objectKey), ""); got != "status=200 body=through-the-relay" {
		t.Fatalf("branch GET through the relay = %q", got)
	}
	if body, ok := objects.stub.object(copyBucket.PhysicalName, objectKey); !ok || string(body) != "through-the-relay" {
		t.Fatalf("the object did not land in the branch's own bucket: %q %v", body, ok)
	}
	if got := fetch(http.MethodGet, presignRelayURL(t, parentCredential, http.MethodGet, parentBucket, objectKey), ""); !strings.HasPrefix(got, "status=403") || !strings.Contains(got, "InvalidAccessKeyId") {
		t.Fatalf("parent key through the relay = %q, want 403 InvalidAccessKeyId", got)
	}
	if objects.relayed.saw(parentCredential.AccessKeyID) {
		t.Fatal("the relay forwarded a parent key to the gateway")
	}
	if !objects.relayed.saw(branchCredential.AccessKeyID) {
		t.Fatal("the branch's requests did not come through the relay port")
	}
}

// releaseRuntimeFlag turns a hot runtime flag on and waits for apid.
func releaseRuntimeFlag(t *testing.T, store *state.PgStore, key string) {
	t.Helper()
	row, err := store.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: key, Scope: state.RuntimeConfigScopeGlobal,
		DesiredValue: json.RawMessage(`true`), ApplyMode: state.RuntimeConfigApplyHot, ActorID: uuid.NewString(), Reason: "environment branch objects metal fixture"})
	if err != nil {
		t.Fatalf("release %s: %v", key, err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		receipts, err := store.ListRuntimeConfigApplyReceipts(t.Context(), []string{row.Key})
		if err == nil {
			for _, r := range receipts {
				if r.Daemon == "apid" && r.Status == state.RuntimeConfigReceiptApplied && r.Version >= row.Version {
					return
				}
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("apid did not apply %s", key)
}

type branchObjectGateway struct {
	env      []string
	identity *age.X25519Identity
	stub     *objectStorageS3Stub
	relayed  *relayedKeys
}

// startBranchObjectGateway runs s3-gatewayd's handler on the public TLS
// endpoint and on the loopback port the relay forwards to, with the host keys
// apid seals to and vmmd opens with.
func startBranchObjectGateway(t *testing.T, store *state.PgStore) branchObjectGateway {
	t.Helper()
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	keysDir := t.TempDir()
	for _, name := range []string{"host.age", "fleet.age"} {
		if err := secretbox.WriteHostKeyAtPath(filepath.Join(keysDir, name), identity); err != nil {
			t.Fatal(err)
		}
	}
	recipient := filepath.Join(keysDir, "host.age.pub")
	if err := secretbox.WriteRecipientFile(recipient, identity); err != nil {
		t.Fatal(err)
	}
	stub := newObjectStorageS3Stub(t)
	relayed := &relayedKeys{}
	mux := http.NewServeMux()
	public := httptest.NewTLSServer(mux)
	t.Cleanup(public.Close)
	relayListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	relayServer := &http.Server{Handler: relayed.wrap(mux), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = relayServer.Serve(relayListener) }()
	t.Cleanup(func() { _ = relayServer.Close() })

	config := objectstorage.Config{
		Accounting: &api.ObjectStoragePolicy{
			MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1000,
			MaxMonthlyCostMillicents: 1 << 30, MaxMonthlyRequests: 1000,
			MaxMonthlyEgressBytes: 1 << 30, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600,
		},
		DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": objectStorageE2EBackendID},
		MaxBucketsPerApp: 10, MaxUploadBytes: api.MaxObjectUploadBytes,
		PublicEndpoint: public.URL, PublicRegion: "us-east-1",
		Backends: []objectstorage.BackendConfig{{
			ID: objectStorageE2EBackendID, Driver: "s3", Region: "us-east-1", Namespace: "e2e-s3",
			Endpoint: stub.server.URL, S3Region: "us-east-1", PathStyle: true,
			AccessKeyEnv: "FAAS_E2E_S3_ACCESS_KEY", SecretKeyEnv: "FAAS_E2E_S3_SECRET_KEY", AllowHTTP: true,
			UsageReportsPath: filepath.Join(keysDir, "usage.json"),
		}},
	}
	registry, err := objectstorage.NewRegistry(config, func(name string) string {
		return map[string]string{"FAAS_E2E_S3_ACCESS_KEY": "e2e-access", "FAAS_E2E_S3_SECRET_KEY": "e2e-secret"}[name]
	}, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatalf("object storage registry: %v", err)
	}
	relayHost := api.EnvironmentBranchObjectRelayHost
	handler, err := s3gateway.New(s3gateway.Config{Registry: registry, Store: store, RequestMetrics: store, SpoolDir: t.TempDir(),
		RelayHosts: []string{relayHost, net.JoinHostPort(relayHost, strconv.Itoa(api.EnvironmentBranchObjectRelayPort))},
		OpenSecret: func(blob []byte) (string, error) {
			namespace, value, err := secretbox.OpenBytes(identity, blob)
			if err != nil || namespace != s3gateway.CredentialSecretNamespace {
				return "", fmt.Errorf("invalid fixture credential")
			}
			return string(value), nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	mux.Handle("/", handler)
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(keysDir, "object-storage.json")
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return branchObjectGateway{identity: identity, stub: stub, relayed: relayed, env: []string{
		"FAAS_OBJECT_STORAGE_CONFIG=" + configPath,
		"FAAS_E2E_S3_ACCESS_KEY=e2e-access", "FAAS_E2E_S3_SECRET_KEY=e2e-secret",
		"FAAS_HOST_KEY_PATH=" + filepath.Join(keysDir, "host.age"),
		"FAAS_HOST_AGE_IDENTITY_PATH=" + filepath.Join(keysDir, "host.age"),
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipient, "FAAS_FLEET_AGE_RECIPIENT_PATH=" + recipient,
		"FAAS_GATEWAYD_OBJECT_RELAY_UPSTREAM=" + relayListener.Addr().String(),
	}}
}

// relayedKeys records which access keys reached the gateway's relay port.
type relayedKeys struct {
	mu   sync.Mutex
	keys map[string]bool
}

func (r *relayedKeys) wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		accessKey, _, _ := strings.Cut(req.URL.Query().Get("X-Amz-Credential"), "/")
		r.mu.Lock()
		if r.keys == nil {
			r.keys = make(map[string]bool)
		}
		r.keys[accessKey] = true
		r.mu.Unlock()
		next.ServeHTTP(w, req)
	})
}

func (r *relayedKeys) saw(accessKey string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.keys[accessKey]
}

func (s *objectStorageS3Stub) object(bucket, key string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	o, ok := s.buckets[bucket][key]
	return o.body, ok
}

func presignRelayURL(t *testing.T, credential aws.Credentials, method, bucket, key string) string {
	t.Helper()
	host := net.JoinHostPort(api.EnvironmentBranchObjectRelayHost, strconv.Itoa(api.EnvironmentBranchObjectRelayPort))
	req, err := http.NewRequest(method, "http://"+host+"/"+bucket+"/"+key+"?X-Amz-Expires=600", nil)
	if err != nil {
		t.Fatal(err)
	}
	signed, _, err := awsv4.NewSigner().PresignHTTP(context.Background(), credential, req, "UNSIGNED-PAYLOAD", "s3", "us-east-1", time.Now().UTC())
	if err != nil {
		t.Fatal(err)
	}
	return signed
}

func waitBranchDataReady(t *testing.T, store *state.PgStore, accountID, projectID, slug string) state.ProjectEnvironmentBranch {
	t.Helper()
	deadline := time.Now().Add(5 * time.Minute)
	for {
		branch, err := store.ProjectEnvironmentBranchBySlug(context.Background(), accountID, projectID, slug)
		if err == nil && branch.DataState == state.EnvironmentBranchDataReady {
			return branch
		}
		if time.Now().After(deadline) {
			t.Fatalf("branch %s data never became ready: %+v, %v", slug, branch, err)
		}
		time.Sleep(2 * time.Second)
	}
}

// The app reports one environment variable and runs one HTTP request it is
// handed, so the test signs and the guest only carries the request.
func environmentBranchObjectsFixture(t *testing.T) []byte {
	t.Helper()
	const packageJSON = `{"name":"environment-branch-objects-metal","version":"1.0.0","private":true,"engines":{"node":"22"},"scripts":{"start":"node index.js"},"dependencies":{}}`
	const indexJS = `const http = require('http');
http.createServer((req, res) => {
  const u = new URL(req.url, 'http://local');
  if (u.pathname === '/healthz') { res.writeHead(200); res.end('ready'); return; }
  if (u.pathname === '/env') { res.writeHead(200); res.end(process.env[u.searchParams.get('k')] || ''); return; }
  if (u.pathname === '/fetch') {
    const body = u.searchParams.get('body') || '';
    const headers = body ? { 'content-length': Buffer.byteLength(body), 'content-type': 'application/octet-stream' } : {};
    const out = http.request(new URL(u.searchParams.get('url')), { method: u.searchParams.get('method') || 'GET', headers }, (up) => {
      let data = '';
      up.on('data', (c) => { data += c; });
      up.on('end', () => { res.writeHead(200); res.end('status=' + up.statusCode + ' body=' + data); });
    });
    out.setTimeout(10000, () => out.destroy(new Error('timeout')));
    out.on('error', (e) => { res.writeHead(200); res.end('error=' + (e.code || e.message)); });
    out.end(body);
    return;
  }
  res.writeHead(200); res.end('ok');
}).listen(Number(process.env.PORT || 8080), '0.0.0.0');`
	return buildTarGz(t, map[string]string{
		"package.json":     packageJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	})
}
