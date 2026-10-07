//go:build metal

// adr: 609
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
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/webhookout"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

const customerWorkflowCSV = "id,count\nalice,1\n"
const customerWorkflowWebhookSecret = "native-customer-workflow-fixture-secret"

func customerWorkflowDeclaration() api.OperationArtifactUploadRequest {
	return api.OperationArtifactUploadRequest{ReportID: "export-csv", Name: "export.csv", SizeBytes: int64(len(customerWorkflowCSV)), SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(customerWorkflowCSV)))}
}

type customerWorkflowFixture struct {
	h                              *e2etest.Harness
	store                          *state.PgStore
	owner, customer                *api.Client
	account                        state.Account
	app                            state.App
	tenantID, policyPath, imageRef string
	definition                     api.OperationDefinitionResponse
	workflow                       api.WorkflowSpec
	verifier                       *workloadidentity.Verifier
	relay                          *customerWorkflowRelay
	webhookReady, webhookInvalid   atomic.Bool
	webhookReceipt                 atomic.Value
	mu                             sync.Mutex
	operationIDs                   map[string]bool
}

func newCustomerWorkflowFixture(t *testing.T, finalTimeout time.Duration) *customerWorkflowFixture {
	t.Helper()
	if _, err := os.Stat("/dev/kvm"); err != nil || os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("dedicated native KVM host and kernel required for Customer Workflow Operations")
	}
	if os.Geteuid() != 0 || os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("native Customer Workflow Operations require root and real PostgreSQL")
	}
	f := &customerWorkflowFixture{policyPath: filepath.Join(t.TempDir(), "preview.json"), operationIDs: map[string]bool{}}
	mustCustomerWorkflow(t, os.WriteFile(f.policyPath, []byte(`{"version":1,"enabled":false}`), 0600))
	keyPath, jwksPath := f.identityTrust(t)
	objectPath, backend := customerWorkflowAccounting(t)
	configPath := filepath.Join(t.TempDir(), "apid.toml")
	mustCustomerWorkflow(t, os.WriteFile(configPath, []byte(fmt.Sprintf("operations_preview_policy_path = %q\noperations_workload_jwks_path = %q\n", f.policyPath, jwksPath)), 0600))
	ageKey, err := age.GenerateX25519Identity()
	mustCustomerWorkflow(t, err)
	agePath := filepath.Join(t.TempDir(), "host.age")
	mustCustomerWorkflow(t, secretbox.WriteHostKeyAtPath(agePath, ageKey))
	receiver := f.webhookReceiver(t)
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	base, _ := e2etest.HelloImage("native-workflow-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("native-workflow-base", base))
	image, _ := e2etest.CustomerWorkflowImage("native-workflow-app")
	f.imageRef = registry.AddImage("native-workflow-app", image)
	t.Setenv("FAAS_E2E_API_HOSTING_SMOKE", "1")
	pool := pgtest.OpenMigrated(t)
	f.h = e2etest.Start(t, pool, e2etest.DeployWake|e2etest.GatewaydPublic|e2etest.Meterd,
		"FAAS_WORKFLOWS_ENABLED=1", "FAAS_APID_CONFIG="+configPath,
		"FAAS_WORKLOAD_IDENTITY_KEY_PATH="+keyPath, "FAAS_OBJECT_STORAGE_CONFIG="+objectPath,
		"FAAS_WORKFLOW_S3_ACCESS=fixture-access", "FAAS_WORKFLOW_S3_SECRET=fixture-secret",
		"FAAS_HOST_AGE_IDENTITY_PATH="+agePath, "FAAS_EGRESS_ALLOW_LOOPBACK=1")
	t.Cleanup(func() { f.cleanup(t) })
	f.store = state.NewPgStore(pool)
	f.owner = api.NewClient(f.h.APIDURL, f.h.SeedAccount(t.Context(), api.PlanPro, "customer-workflow"))
	acct, err := f.owner.Whoami(t.Context())
	mustCustomerWorkflow(t, err)
	f.account.ID = acct.ID
	noAuth := false
	app, err := f.owner.CreateApp(t.Context(), api.CreateAppRequest{Slug: "native-workflow-ops", Type: "app", RequireAuthn: &noAuth})
	mustCustomerWorkflow(t, err)
	f.app, err = f.store.AppByID(t.Context(), app.ID)
	mustCustomerWorkflow(t, err)
	tenant, err := f.owner.CreatePlatformTenant(t.Context(), api.CreatePlatformTenantRequest{ExternalRef: "alice", Name: "Alice"})
	mustCustomerWorkflow(t, err)
	f.tenantID = tenant.ID
	f.customer = f.customerToken(t, tenant.ID)
	surface, err := f.store.CreateTenantSurfaceIfUnderQuota(t.Context(), state.CreateTenantSurfaceParams{AccountID: f.account.ID, AppID: f.app.ID, Name: "alice", CertKind: state.CertKindPerHostSAN}, api.MustLimitsFor(api.PlanPro))
	mustCustomerWorkflow(t, err)
	_, err = f.store.LinkPlatformTenantSurface(t.Context(), f.account.ID, tenant.ID, surface.ID)
	mustCustomerWorkflow(t, err)
	mustCustomerWorkflow(t, f.store.UpdateTenantSurfaceStatus(t.Context(), surface.ID, state.SurfaceStatusActive))
	mustCustomerWorkflow(t, f.store.RecordObjectUsageReport(t.Context(), api.ObjectStorageUsageReport{AccountID: f.account.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "native-workflow-fixture", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().Add(-time.Second)}))
	runtime, err := f.store.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: "s3_enabled", Scope: state.RuntimeConfigScopeGlobal, DesiredValue: json.RawMessage(`true`), ApplyMode: state.RuntimeConfigApplyHot, ActorID: f.account.ID, Reason: "isolated native workflow file fixture"})
	mustCustomerWorkflow(t, err)
	mustCustomerWorkflow(t, f.store.MarkRuntimeConfigApplied(t.Context(), runtime.Key, runtime.Scope, "", runtime.Version, json.RawMessage(`true`), ""))
	mustCustomerWorkflow(t, f.h.RestartAPID())
	if finalTimeout == 0 {
		finalTimeout = 2 * time.Minute
	}
	f.workflow = api.WorkflowSpec{Name: "export-chain", Steps: []api.WorkflowStepSpec{
		{Name: "collect", Path: "/collect", Timeout: 2 * time.Minute},
		{Name: "transform", Path: "/transform", DependsOn: []string{"collect"}, Input: json.RawMessage(`{"mode":"{{steps.collect.output.mode}}","rows":"{{steps.collect.output.rows}}"}`), Timeout: 2 * time.Minute},
		{Name: "finish", Path: "/finish", DependsOn: []string{"transform"}, Input: json.RawMessage(`{"mode":"{{steps.transform.output.mode}}","rows":"{{steps.transform.output.rows}}"}`), Timeout: finalTimeout},
	}}
	dep := f.deploy(t, "original")
	sealed, err := secretbox.SealBytes(ageKey.Recipient(), "APP_WEBHOOK", []byte(customerWorkflowWebhookSecret), api.AppWebhookSecretMaxBytes)
	mustCustomerWorkflow(t, err)
	hook, err := f.store.CreateAppWebhook(t.Context(), state.AppWebhook{Scope: state.AppWebhookScopeApp, AccountID: f.account.ID, AppID: f.app.ID, TargetURL: receiver.URL, SecretSealed: sealed, EventFilter: []string{string(state.AppWebhookEventOperationFinished)}, DeliveryFormat: state.AppWebhookDeliveryFormatCloudEvents, Enabled: true})
	mustCustomerWorkflow(t, err)
	f.writePolicy(t, true)
	f.definition, err = f.owner.PutOperationDefinition(t.Context(), f.app.Slug, dep.ID, "export", api.OperationDefinitionSpec{Name: "export", Workflow: f.workflow.Name, Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, Recovery: api.OperationRecoveryReconcile, ProgressStages: []string{"collect", "transform", "finish"}, CompletionWebhookID: hook.ID,
		InputSchema:  json.RawMessage(`{"type":"object","required":["mode"],"properties":{"mode":{"type":"string"}},"additionalProperties":false}`),
		OutputSchema: json.RawMessage(`{"type":"object","required":["rows","artifact_id","version"],"properties":{"rows":{"type":"integer","const":1},"artifact_id":{"type":"string"},"version":{"type":"string"}},"additionalProperties":false}`)})
	mustCustomerWorkflow(t, err)
	f.relay = newCustomerWorkflowRelay(f)
	f.relay.watch(t)
	return f
}

func (f *customerWorkflowFixture) cleanup(t *testing.T) {
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
			t.Error("native workflow app instances did not drain")
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

func (f *customerWorkflowFixture) identityTrust(t *testing.T) (string, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	mustCustomerWorkflow(t, err)
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, workloadidentity.DefaultKeyID, workloadidentity.DefaultTokenTTL)
	mustCustomerWorkflow(t, err)
	f.verifier, err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	mustCustomerWorkflow(t, err)
	keyPath := filepath.Join(t.TempDir(), "workload.key.pem")
	mustCustomerWorkflow(t, os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}), 0600))
	jwksPath := filepath.Join(t.TempDir(), "workload.jwks.json")
	body, err := json.Marshal(signer.JWKS())
	mustCustomerWorkflow(t, err)
	mustCustomerWorkflow(t, os.WriteFile(jwksPath, body, 0600))
	return keyPath, jwksPath
}

func customerWorkflowAccounting(t *testing.T) (string, objectstorage.Backend) {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 1000, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	config := objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "native-workflow"}, Backends: []objectstorage.BackendConfig{{ID: "native-workflow", Driver: "s3", Region: "us-east-1", Namespace: "native-fixture", Endpoint: server.URL, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true, AccessKeyEnv: "FAAS_WORKFLOW_S3_ACCESS", SecretKeyEnv: "FAAS_WORKFLOW_S3_SECRET", UsageReportsPath: filepath.Join(t.TempDir(), "usage.json")}}}
	path := filepath.Join(t.TempDir(), "objects.json")
	body, err := json.Marshal(config)
	mustCustomerWorkflow(t, err)
	mustCustomerWorkflow(t, os.WriteFile(path, body, 0600))
	registry, err := objectstorage.Load(func(key string) string {
		switch key {
		case "FAAS_OBJECT_STORAGE_CONFIG":
			return path
		case "FAAS_WORKFLOW_S3_ACCESS":
			return "fixture-access"
		case "FAAS_WORKFLOW_S3_SECRET":
			return "fixture-secret"
		}
		return ""
	})
	mustCustomerWorkflow(t, err)
	backend, err := registry.Default("us-east-1")
	mustCustomerWorkflow(t, err)
	return path, backend
}

func (f *customerWorkflowFixture) deploy(t *testing.T, version string) api.DeploymentResponse {
	t.Helper()
	dep, err := f.owner.Deploy(t.Context(), f.app.Slug, api.CreateDeploymentRequest{Image: f.imageRef, Workflows: []api.WorkflowSpec{f.workflow}, Overrides: &api.CreateDeploymentOverrides{Env: map[string]string{"FIXTURE_VERSION": version}, Healthcheck: &api.DeploymentHealthcheck{Path: "/healthz"}}})
	mustCustomerWorkflow(t, err)
	_, err = e2etest.WaitForDeploymentLive(t.Context(), t, f.h.Pool, dep.ID, 3*time.Minute)
	mustCustomerWorkflow(t, err)
	return dep
}

func (f *customerWorkflowFixture) writePolicy(t *testing.T, enabled bool) {
	t.Helper()
	now := time.Now().UTC()
	policy := operations.PreviewPolicy{Version: 1, Enabled: enabled, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(api.OperationPreviewWindowMax - time.Minute), Cohorts: []operations.PreviewCohort{{AccountID: f.account.ID, AppID: f.app.ID, Scope: state.DefaultEnvScope, PlatformTenantIDs: []string{f.tenantID}, ExecutionKinds: []string{operations.ExecutionWorkflow}}}}
	body, err := json.Marshal(policy)
	mustCustomerWorkflow(t, err)
	mustCustomerWorkflow(t, os.WriteFile(f.policyPath+".tmp", body, 0600))
	mustCustomerWorkflow(t, os.Rename(f.policyPath+".tmp", f.policyPath))
}

func (f *customerWorkflowFixture) customerToken(t *testing.T, tenant string) *api.Client {
	t.Helper()
	token, err := f.owner.CreatePlatformTenantAccessToken(t.Context(), tenant, api.CreatePlatformTenantAccessTokenRequest{Name: "native-browser", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}})
	mustCustomerWorkflow(t, err)
	return api.NewClient(f.h.APIDURL, token.Token)
}

func (f *customerWorkflowFixture) webhookReceiver(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, api.OperationReportBodyMaxBytes))
		timestamp, parseErr := strconv.ParseInt(r.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
		if err == nil {
			err = parseErr
		}
		if err == nil {
			err = webhookout.NewSigner([]byte(customerWorkflowWebhookSecret)).Verify(timestamp, r.Header.Get("X-Faas-Delivery-Id"), body, strings.TrimPrefix(r.Header.Get("X-Faas-Webhook-Signature"), "sha256="))
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

func (f *customerWorkflowFixture) cleanResultFiles(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	backend, err := storage.BackendFromEnvContext(ctx)
	if err != nil {
		t.Error(err)
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for id := range f.operationIDs {
		op, err := f.store.OperationByID(ctx, f.account.ID, f.tenantID, id)
		if err != nil {
			t.Error(err)
			continue
		}
		for _, key := range op.ArtifactStorageKeys {
			if !strings.HasPrefix(key, "operation-results/"+f.account.ID+"/") {
				t.Error("foreign fixture file key")
				continue
			}
			if err := backend.Delete(ctx, key); err != nil {
				t.Error(err)
			}
		}
	}
}

func mustCustomerWorkflow(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
func customerWorkflowEventually(t *testing.T, budget time.Duration, label string, ready func() bool) {
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

func (f *customerWorkflowFixture) assertStale(t *testing.T, id string, p customerWorkflowProof) {
	t.Helper()
	if p.proof.Capability == "" || p.token == "" {
		t.Fatal("real guest proof was not observed")
	}
	_, err := api.NewClient(f.h.APIDURL, p.token).ReuseWorkflowOperationUpload(t.Context(), id, p.proof, customerWorkflowDeclaration())
	assertCustomerWorkflowFence(t, err)
	_, err = api.NewClient(f.h.APIDURL, p.token).UploadWorkflowOperationArtifact(t.Context(), id, p.proof, customerWorkflowDeclaration(), strings.NewReader(customerWorkflowCSV))
	assertCustomerWorkflowFence(t, err)
}

func assertCustomerWorkflowFence(t *testing.T, err error) {
	t.Helper()
	var problem *api.APIError
	if !errors.As(err, &problem) || (problem.Problem.Status != http.StatusConflict && problem.Problem.Status != http.StatusNotFound) {
		t.Fatal("old native file proof was not fenced")
	}
}
