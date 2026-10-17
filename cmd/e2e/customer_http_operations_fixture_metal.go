//go:build metal

// adr: 521
package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/webhookout"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

const customerHTTPCSV = "id,count\nalice,1\n"
const customerHTTPWebhookSecret = "native-customer-http-fixture-secret"

func customerHTTPDeclaration() api.OperationArtifactUploadRequest {
	return api.OperationArtifactUploadRequest{ReportID: "export-csv", Name: "export.csv", SizeBytes: int64(len(customerHTTPCSV)), SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(customerHTTPCSV)))}
}

// customerHTTPFixture qualifies ordinary HTTP Operations (ADR-521) through a
// real deployed guest. Customer intent is seeded through the API; admission,
// claim, dispatch, renewal and settlement are performed by the real daemons.
type customerHTTPFixture struct {
	h                              *e2etest.Harness
	store                          *state.PgStore
	owner, customer                *api.Client
	account                        state.Account
	app                            state.App
	tenantID, policyPath, imageRef string
	definition                     api.OperationDefinitionResponse
	deployment                     api.DeploymentResponse
	verifier                       *workloadidentity.Verifier
	relay                          *customerHTTPRelay
	webhookReady, webhookInvalid   atomic.Bool
	webhookReceipt                 atomic.Value
	mu                             sync.Mutex
	operationIDs                   map[string]bool
}

func newCustomerHTTPFixture(t *testing.T) *customerHTTPFixture {
	t.Helper()
	if _, err := os.Stat("/dev/kvm"); err != nil || os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("dedicated native KVM host and kernel required for Customer HTTP Operations")
	}
	if os.Geteuid() != 0 || os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("native Customer HTTP Operations require root and real PostgreSQL")
	}
	f := &customerHTTPFixture{policyPath: filepath.Join(t.TempDir(), "preview.json"), operationIDs: map[string]bool{}}
	mustCustomerHTTP(t, os.WriteFile(f.policyPath, []byte(`{"version":1,"enabled":false}`), 0600))
	keyPath, jwksPath := f.identityTrust(t)
	objectPath, backend := customerWorkflowAccounting(t)
	configPath := filepath.Join(t.TempDir(), "apid.toml")
	mustCustomerHTTP(t, os.WriteFile(configPath, []byte(fmt.Sprintf("operations_preview_policy_path = %q\noperations_workload_jwks_path = %q\n", f.policyPath, jwksPath)), 0600))
	ageKey, err := age.GenerateX25519Identity()
	mustCustomerHTTP(t, err)
	agePath := filepath.Join(t.TempDir(), "host.age")
	mustCustomerHTTP(t, secretbox.WriteHostKeyAtPath(agePath, ageKey))
	// imaged and apid load fleet.age beside host.age; the same key serves both.
	mustCustomerHTTP(t, secretbox.WriteHostKeyAtPath(filepath.Join(filepath.Dir(agePath), "fleet.age"), ageKey))
	// apid seals inbound webhook secrets to this recipient.
	recipientPath := filepath.Join(filepath.Dir(agePath), "host.age.pub")
	mustCustomerHTTP(t, os.WriteFile(recipientPath, []byte(ageKey.Recipient().String()), 0o444))
	receiver := f.webhookReceiver(t)
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	base, _ := e2etest.HelloImage("native-http-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("native-http-base", base))
	image, _ := e2etest.CustomerHTTPImage("native-http-app")
	f.imageRef = registry.AddImage("native-http-app", image)
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	pool := pgtest.OpenMigrated(t)
	// Without the template clone (FAAS_PGTEST_TEMPLATE_DATABASE) the pool is an
	// empty schema; the harness waits for migrations it does not run.
	mustCustomerHTTP(t, db.MigrateUp(t.Context(), pool))
	f.h = e2etest.Start(t, pool, e2etest.DeployWake|e2etest.GatewaydPublic|e2etest.Meterd,
		"FAAS_E2E_APID_CONFIG="+configPath, "FAAS_WORKLOAD_IDENTITY_KEY_PATH="+keyPath, "FAAS_OBJECT_STORAGE_CONFIG="+objectPath,
		"FAAS_WORKFLOW_S3_ACCESS=fixture-access", "FAAS_WORKFLOW_S3_SECRET=fixture-secret",
		"FAAS_HOST_AGE_IDENTITY_PATH="+agePath, "FAAS_HOST_AGE_RECIPIENT_PATH="+recipientPath, "FAAS_EGRESS_ALLOW_LOOPBACK=1")
	t.Cleanup(func() { f.cleanup(t) })
	f.store = state.NewPgStore(pool)
	f.owner = api.NewClient(f.h.APIDURL, f.h.SeedAccount(t.Context(), api.PlanPro, "customer-http"))
	acct, err := f.owner.Whoami(t.Context())
	mustCustomerHTTP(t, err)
	f.account.ID = acct.ID
	noAuth := false
	app, err := f.owner.CreateApp(t.Context(), api.CreateAppRequest{Slug: "native-http-ops", Type: "app", RequireAuthn: &noAuth})
	mustCustomerHTTP(t, err)
	f.app, err = f.store.AppByID(t.Context(), app.ID)
	mustCustomerHTTP(t, err)
	tenant, err := f.owner.CreatePlatformTenant(t.Context(), api.CreatePlatformTenantRequest{ExternalRef: "alice", Name: "Alice"})
	mustCustomerHTTP(t, err)
	f.tenantID = tenant.ID
	f.customer = f.customerToken(t, tenant.ID)
	surface, err := f.store.CreateTenantSurfaceIfUnderQuota(t.Context(), state.CreateTenantSurfaceParams{AccountID: f.account.ID, AppID: f.app.ID, Name: "alice", CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(api.PlanPro))
	mustCustomerHTTP(t, err)
	_, err = f.store.LinkPlatformTenantSurface(t.Context(), f.account.ID, tenant.ID, surface.ID)
	mustCustomerHTTP(t, err)
	mustCustomerHTTP(t, f.store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, state.SurfaceStatusActive))
	mustCustomerHTTP(t, seedOperationObjectAccounting(t, f.store, f.account.ID, f.app.ID, backend))
	runtime, err := f.store.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: "s3_enabled", Scope: state.RuntimeConfigScopeGlobal, DesiredValue: json.RawMessage(`true`), ApplyMode: state.RuntimeConfigApplyHot, ActorID: f.account.ID, Reason: "isolated native HTTP operation file fixture"})
	mustCustomerHTTP(t, err)
	mustCustomerHTTP(t, f.store.MarkRuntimeConfigApplied(t.Context(), runtime.Key, runtime.Scope, "", runtime.Version, json.RawMessage(`true`), ""))
	mustCustomerHTTP(t, f.h.RestartAPID())
	dep := f.deploy(t, "original")
	f.deployment = dep
	sealed, err := secretbox.SealBytes(ageKey.Recipient(), "APP_WEBHOOK", []byte(customerHTTPWebhookSecret), api.AppWebhookSecretMaxBytes)
	mustCustomerHTTP(t, err)
	hook, err := f.store.CreateAppWebhook(t.Context(), state.AppWebhook{Scope: state.AppWebhookScopeApp, AccountID: f.account.ID, AppID: f.app.ID, TargetURL: receiver.URL, SecretSealed: sealed, EventFilter: []string{string(state.AppWebhookEventOperationFinished)}, DeliveryFormat: state.AppWebhookDeliveryFormatCloudEvents, Enabled: true})
	mustCustomerHTTP(t, err)
	f.writePolicy(t, true)
	f.definition, err = f.owner.PutOperationDefinition(t.Context(), f.app.Slug, dep.ID, "export", api.OperationDefinitionSpec{Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, Recovery: api.OperationRecoveryReconcile, ProgressStages: []string{"generating", "uploading"}, CompletionWebhookID: hook.ID,
		InputSchema:  json.RawMessage(`{"type":"object","required":["mode"],"properties":{"mode":{"type":"string"}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","required":["rows","artifact_id","version"],"properties":{"rows":{"type":"integer","const":1},"artifact_id":{"type":"string"},"version":{"type":"string"}},"additionalProperties":false}`)})
	mustCustomerHTTP(t, err)
	if kind := operations.DefinitionExecutionKind(f.definition.Spec); kind != operations.ExecutionHTTP {
		t.Fatalf("fixture definition kind = %q, want ordinary HTTP", kind)
	}
	f.relay = newCustomerHTTPRelay(f)
	f.relay.watch(t)
	return f
}

func (f *customerHTTPFixture) cleanup(t *testing.T) {
	t.Helper()
	f.h.DumpLogs(t)
	if f.owner != nil && f.app.ID != "" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		if err := f.owner.DeleteApp(ctx, f.app.Slug); err != nil {
			t.Errorf("native fixture app cleanup: %v", err)
		}
		for ctx.Err() == nil {
			instances, err := f.store.ListInstancesForApp(ctx, f.app.ID)
			live := false
			for _, instance := range instances {
				switch state.State(instance.State) {
				case state.StateParked, state.StateStopped, state.StateFailed:
				default:
					live = true
				}
			}
			if err == nil && !live {
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if ctx.Err() != nil {
			t.Error("native HTTP operation app instances did not drain")
		}
		cancel()
	}
	if f.relay != nil {
		f.relay.close()
	}
	f.h.Stop()
	// Reap native producers before deleting receipts, including on early failure.
	if f.store != nil && f.account.ID != "" {
		f.cleanResultFiles(t)
	}
	leakcheck.AssertZero(t)
}

func (f *customerHTTPFixture) identityTrust(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	mustCustomerHTTP(t, err)
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, workloadidentity.DefaultKeyID, workloadidentity.DefaultTokenTTL)
	mustCustomerHTTP(t, err)
	f.verifier, err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	mustCustomerHTTP(t, err)
	keyPath := filepath.Join(t.TempDir(), "workload.key.pem")
	mustCustomerHTTP(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600))
	jwksPath := filepath.Join(t.TempDir(), "workload.jwks.json")
	body, err := json.Marshal(signer.JWKS())
	mustCustomerHTTP(t, err)
	mustCustomerHTTP(t, os.WriteFile(jwksPath, body, 0600))
	return keyPath, jwksPath
}

func (f *customerHTTPFixture) deploy(t *testing.T, version string) api.DeploymentResponse {
	t.Helper()
	dep, err := f.owner.Deploy(t.Context(), f.app.Slug, api.CreateDeploymentRequest{Image: f.imageRef, Overrides: &api.CreateDeploymentOverrides{Env: map[string]string{"FIXTURE_VERSION": version}, Healthcheck: &api.DeploymentHealthcheck{Path: "/healthz"}}})
	mustCustomerHTTP(t, err)
	// The per-deploy image scan alone takes over two minutes on a two-vCPU
	// acceptance host; three minutes was not enough headroom.
	_, err = e2etest.WaitForDeploymentLive(t.Context(), t, f.h.Pool, dep.ID, 8*time.Minute)
	mustCustomerHTTP(t, err)
	return dep
}

func (f *customerHTTPFixture) writePolicy(t *testing.T, enabled bool) {
	t.Helper()
	now := time.Now().UTC()
	policy := operations.PreviewPolicy{Version: 1, Enabled: enabled, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(time.Hour), Cohorts: []operations.PreviewCohort{{AccountID: f.account.ID, AppID: f.app.ID, Scope: state.DefaultEnvScope, PlatformTenantIDs: []string{f.tenantID}, ExecutionKinds: []string{operations.ExecutionHTTP}}}}
	body, err := json.Marshal(policy)
	mustCustomerHTTP(t, err)
	mustCustomerHTTP(t, os.WriteFile(f.policyPath+".tmp", body, 0600))
	mustCustomerHTTP(t, os.Rename(f.policyPath+".tmp", f.policyPath))
}

func (f *customerHTTPFixture) customerToken(t *testing.T, tenant string) *api.Client {
	t.Helper()
	token, err := f.owner.CreatePlatformTenantAccessToken(t.Context(), tenant, api.CreatePlatformTenantAccessTokenRequest{Name: "native-browser", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}})
	mustCustomerHTTP(t, err)
	return api.NewClient(f.h.APIDURL, token.Token)
}

func (f *customerHTTPFixture) webhookReceiver(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, api.OperationReportBodyMaxBytes))
		timestamp, parseErr := strconv.ParseInt(r.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
		if err == nil {
			err = parseErr
		}
		if err == nil {
			err = webhookout.NewSigner([]byte(customerHTTPWebhookSecret)).Verify(timestamp, r.Header.Get("X-Faas-Delivery-Id"), body, strings.TrimPrefix(r.Header.Get("X-Faas-Webhook-Signature"), "sha256="))
		}
		var event struct {
			Type string `json:"type"`
			Data struct {
				Operation api.OperationResponse `json:"operation"`
			} `json:"data"`
		}
		if err == nil {
			err = json.Unmarshal(body, &event)
		}
		if err != nil || event.Type != string(state.AppWebhookEventOperationFinished) || !event.Data.Operation.State.Terminal() {
			f.webhookInvalid.Store(true)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		f.webhookReceipt.Store(customerJobWebhookReceipt{DeliveryID: r.Header.Get("X-Faas-Delivery-Id"), Operation: event.Data.Operation})
		if !f.webhookReady.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server
}

// HTTP recovery starts a fresh generation with fresh file receipts, so the
// cleanup reads every retained, staging or abandoned copy from the ledger
// rather than only the current generation's published keys.
func (f *customerHTTPFixture) cleanResultFiles(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend, err := storage.BackendFromEnvContext(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	rows, err := f.h.Pool.Query(ctx, `select storage_key from customer_operation_result_blobs where account_id = $1`, f.account.ID)
	if err != nil {
		t.Error(err)
		return
	}
	keys, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Error(err)
		return
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "operation-results/"+f.account.ID+"/") {
			t.Error("foreign fixture file key")
			continue
		}
		if err := backend.Delete(ctx, key); err != nil && !errors.Is(err, storage.ErrNotFound) {
			t.Error(err)
		}
	}
}

func (f *customerHTTPFixture) assertStale(t *testing.T, id string, p customerHTTPProof) {
	t.Helper()
	if p.proof.Capability == "" || p.token == "" {
		t.Fatal("real guest claim proof was not observed")
	}
	client := api.NewClient(f.h.APIDURL, p.token)
	_, err := client.ReuseOperationUpload(t.Context(), id, p.proof, customerHTTPDeclaration())
	assertCustomerHTTPFence(t, err)
	_, err = client.UploadOperationArtifact(t.Context(), id, p.proof, customerHTTPDeclaration(), strings.NewReader(customerHTTPCSV))
	assertCustomerHTTPFence(t, err)
	_, err = client.ReportOperationProgress(t.Context(), id, p.proof, api.OperationReportRequest{ReportID: "stale", Stage: "uploading", Completed: 1, Total: 1})
	assertCustomerHTTPFence(t, err)
}

func assertCustomerHTTPFence(t *testing.T, err error) {
	t.Helper()
	var problem *api.APIError
	if !errors.As(err, &problem) || (problem.Problem.Status != http.StatusConflict && problem.Problem.Status != http.StatusNotFound) {
		t.Fatalf("old HTTP claim proof was not fenced: %v", err)
	}
}

// mustCustomerHTTP names its caller's line: fixture setup runs inside helpers, so
// t.Helper alone would attribute every setup failure to the test function.
func mustCustomerHTTP(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		_, file, line, _ := runtime.Caller(1)
		t.Fatalf("%s:%d: %v", filepath.Base(file), line, err)
	}
}

func customerHTTPEventually(t *testing.T, budget time.Duration, label string, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(budget)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", label)
}
