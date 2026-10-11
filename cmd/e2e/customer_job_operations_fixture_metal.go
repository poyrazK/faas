//go:build metal

package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/go-jose/go-jose/v4"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/operations"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

const customerJobCSV = "id,count\nalice,1\n"
const customerJobWebhookSecret = "native-customer-job-fixture-secret"

type customerJobFixture struct {
	h                           *MetalJobHarness
	store                       *state.PgStore
	owner, customer             *api.Client
	account                     state.Account
	app                         state.App
	tenant                      state.PlatformTenant
	definition                  api.OperationDefinitionResponse
	artifact                    api.OperationArtifactRequest
	policyPath                  string
	sourceMissing, webhookReady atomic.Bool
	sourceReads                 atomic.Int32
	webhookInvalid              atomic.Bool
	webhookReceipt              atomic.Value
	relays                      []*customerJobRelay
	operationIDs                []string
	artifactKeys                map[string]bool
}

func newCustomerJobFixture(t *testing.T) *customerJobFixture {
	t.Helper()
	if os.Getenv("FAAS_TEST_KERNEL") == "" {
		t.Skip("FAAS_TEST_KERNEL required for native Customer Job Operations")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skip("/dev/kvm required for native Customer Job Operations")
	}
	if os.Geteuid() != 0 {
		t.Fatal("native Customer Job Operations require root on the dedicated KVM host")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" {
		t.Skip("PostgreSQL and builder base fixture required")
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("native qualification cannot disable PostgreSQL")
	}
	f := &customerJobFixture{policyPath: filepath.Join(t.TempDir(), "preview.json"), artifactKeys: map[string]bool{}}
	mustCustomerJob(t, os.WriteFile(f.policyPath, []byte(`{"version":1,"enabled":false}`), 0600))
	cfgPath := filepath.Join(t.TempDir(), "apid.toml")
	jwksPath := customerJobTrust(t)
	mustCustomerJob(t, os.WriteFile(cfgPath, []byte(fmt.Sprintf("operations_preview_policy_path = %q\noperations_workload_jwks_path = %q\n", f.policyPath, jwksPath)), 0600))
	objectCfg, backend := f.objectSource(t)
	identity, err := age.GenerateX25519Identity()
	mustCustomerJob(t, err)
	identityPath := filepath.Join(t.TempDir(), "host.age")
	mustCustomerJob(t, secretbox.WriteHostKeyAtPath(identityPath, identity))
	recipientPath := filepath.Join(t.TempDir(), "host.age.pub")
	mustCustomerJob(t, os.WriteFile(recipientPath, []byte(identity.Recipient().String()), 0444))
	receiver := f.webhookReceiver(t)
	f.h = newMetalHarness(t, "FAAS_E2E_APID_CONFIG="+cfgPath, "FAAS_OBJECT_STORAGE_CONFIG="+objectCfg,
		"FAAS_EGRESS_ALLOW_LOOPBACK=1", // Existing host-only webhook test client; no guest egress grant.
		"FAAS_CUSTOMER_JOB_S3_ACCESS=fixture-access", "FAAS_CUSTOMER_JOB_S3_SECRET=fixture-secret",
		"FAAS_HOST_AGE_IDENTITY_PATH="+identityPath, "FAAS_HOST_AGE_RECIPIENT_PATH="+recipientPath)
	t.Cleanup(func() {
		f.h.DumpLogs(t)
		for _, relay := range f.relays {
			relay.close()
		}
		f.h.Close()
		f.cleanResultFiles(t)
		leakcheck.AssertZero(t)
	})
	f.store = state.NewPgStore(f.h.Pool)
	f.account = *f.h.seedAccount(t, api.PlanPro, "customer-job")
	f.h.defaultKey = f.h.accountKeys[f.account.ID]
	f.owner = api.NewClient(f.h.APIDURL, f.h.defaultKey)
	// Intent fixtures only. Scheduling, claiming, execution and completion are
	// exclusively performed by the real daemon owners.
	f.app, err = f.store.CreateApp(t.Context(), state.App{AccountID: f.account.ID, Slug: "native-job-ops", Type: state.AppTypeApp, Status: state.AppActive})
	mustCustomerJob(t, err)
	dep, err := f.store.CreateDeployment(t.Context(), state.Deployment{AppID: f.app.ID, Kind: state.DeploymentKindImage})
	mustCustomerJob(t, err)
	mustCustomerJob(t, f.store.MarkDeploymentLive(t.Context(), dep.ID))
	tenant, err := f.owner.CreatePlatformTenant(t.Context(), api.CreatePlatformTenantRequest{ExternalRef: "alice", Name: "Alice"})
	mustCustomerJob(t, err)
	f.tenant.ID = tenant.ID
	f.customer = f.customerToken(t, tenant.ID)
	f.writePolicy(t, true)
	f.h.MustSeedFakeImage(t, "customer-native-job:latest")
	job, err := f.owner.CreateJob(t.Context(), api.CreateJobRequest{Name: "native-customer-export", ImageRef: f.h.imageRef(t, "customer-native-job:latest"), Command: []string{"/job-fixture", "customer-operation"}, RAMMB: 512, TaskTimeoutSec: 180})
	mustCustomerJob(t, err)
	f.h.MustWaitJobImageReady(t, &job, 3*time.Minute)
	secret, err := secretbox.SealBytes(identity.Recipient(), "APP_WEBHOOK", []byte(customerJobWebhookSecret), api.AppWebhookSecretMaxBytes)
	mustCustomerJob(t, err)
	hook, err := f.store.CreateAppWebhook(t.Context(), state.AppWebhook{Scope: state.AppWebhookScopeApp, AccountID: f.account.ID, AppID: f.app.ID, TargetURL: receiver.URL, SecretSealed: secret, EventFilter: []string{string(state.AppWebhookEventOperationFinished)}, DeliveryFormat: state.AppWebhookDeliveryFormatCloudEvents, Enabled: true})
	mustCustomerJob(t, err)
	f.definition, err = f.owner.PutOperationDefinition(t.Context(), f.app.Slug, dep.ID, "export", api.OperationDefinitionSpec{
		Name: "export", Job: job.Name, Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant, Recovery: api.OperationRecoveryReconcile,
		ProgressStages: []string{"generating"}, CompletionWebhookID: hook.ID,
		InputSchema:  json.RawMessage(`{"type":"object","required":["mode","artifact"],"properties":{"mode":{"type":"string"},"artifact":{"type":"object"}}}`),
		OutputSchema: json.RawMessage(`{"type":"object","required":["file"],"properties":{"file":{"type":"string","const":"export.csv"}}}`),
	})
	mustCustomerJob(t, err)
	f.seedArtifact(t, backend, dep.Scope)
	runtime, err := f.store.UpsertRuntimeConfig(t.Context(), state.RuntimeConfigUpdate{Key: "s3_enabled", Scope: state.RuntimeConfigScopeGlobal, DesiredValue: json.RawMessage(`true`), ApplyMode: state.RuntimeConfigApplyHot, ActorID: f.account.ID, Reason: "isolated operation file fixture"})
	mustCustomerJob(t, err)
	mustCustomerJob(t, f.store.MarkRuntimeConfigApplied(t.Context(), runtime.Key, runtime.Scope, "", runtime.Version, json.RawMessage(`true`), ""))
	mustCustomerJob(t, f.h.RestartAPID())
	return f
}

func customerJobTrust(t *testing.T) string {
	t.Helper()
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	mustCustomerJob(t, err)
	body, err := json.Marshal(jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &private.PublicKey, KeyID: "native-fixture", Algorithm: "RS256", Use: "sig"}}})
	mustCustomerJob(t, err)
	path := filepath.Join(t.TempDir(), "public.jwks.json")
	mustCustomerJob(t, os.WriteFile(path, body, 0600))
	return path
}
func (f *customerJobFixture) writePolicy(t *testing.T, allowJob bool) {
	t.Helper()
	kinds := []string{operations.ExecutionHTTP}
	if allowJob {
		kinds = []string{operations.ExecutionJob}
	}
	now := time.Now().UTC()
	policy := operations.PreviewPolicy{Version: 1, Enabled: true, NotBefore: now.Add(-time.Minute), ExpiresAt: now.Add(api.OperationPreviewWindowMax - time.Minute), Cohorts: []operations.PreviewCohort{{AccountID: f.account.ID, AppID: f.app.ID, Scope: state.DefaultEnvScope, PlatformTenantIDs: []string{f.tenant.ID}, ExecutionKinds: kinds}}}
	body, err := json.Marshal(policy)
	mustCustomerJob(t, err)
	temporary := f.policyPath + ".tmp"
	mustCustomerJob(t, os.WriteFile(temporary, body, 0600))
	mustCustomerJob(t, os.Rename(temporary, f.policyPath))
}
func (f *customerJobFixture) customerToken(t *testing.T, id string) *api.Client {
	t.Helper()
	token, err := f.owner.CreatePlatformTenantAccessToken(t.Context(), id, api.CreatePlatformTenantAccessTokenRequest{Name: "native-browser", Scopes: []string{api.ScopePlatformTenantOperationsRead, api.ScopePlatformTenantOperationsManage}})
	mustCustomerJob(t, err)
	return api.NewClient(f.h.APIDURL, token.Token)
}
func (f *customerJobFixture) otherCustomer(t *testing.T) *api.Client {
	t.Helper()
	tenant, err := f.owner.CreatePlatformTenant(t.Context(), api.CreatePlatformTenantRequest{ExternalRef: "bob", Name: "Bob"})
	mustCustomerJob(t, err)
	return f.customerToken(t, tenant.ID)
}

func (f *customerJobFixture) objectSource(t *testing.T) (string, objectstorage.Backend) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/operation-exports/export.csv" || r.Header.Get("Authorization") == "" || f.sourceMissing.Load() {
			http.NotFound(w, r)
			return
		}
		f.sourceReads.Add(1)
		w.Header().Set("Content-Type", "text/csv")
		w.Header().Set("Content-Length", strconv.Itoa(len(customerJobCSV)))
		_, _ = io.WriteString(w, customerJobCSV)
	}))
	t.Cleanup(server.Close)
	// These are isolated operator accounting budgets, not customer plan quotas.
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 1000, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	config := objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "native-exports"}, Backends: []objectstorage.BackendConfig{{ID: "native-exports", Driver: "s3", Region: "us-east-1", Namespace: "native-fixture", Endpoint: server.URL, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true, AccessKeyEnv: "FAAS_CUSTOMER_JOB_S3_ACCESS", SecretKeyEnv: "FAAS_CUSTOMER_JOB_S3_SECRET", UsageReportsPath: filepath.Join(t.TempDir(), "usage.json")}}}
	path := filepath.Join(t.TempDir(), "objects.json")
	body, err := json.Marshal(config)
	mustCustomerJob(t, err)
	mustCustomerJob(t, os.WriteFile(path, body, 0600))
	// Load uses the production driver and fingerprint calculation.
	env := func(key string) string {
		switch key {
		case "FAAS_OBJECT_STORAGE_CONFIG":
			return path
		case "FAAS_CUSTOMER_JOB_S3_ACCESS":
			return "fixture-access"
		case "FAAS_CUSTOMER_JOB_S3_SECRET":
			return "fixture-secret"
		}
		return ""
	}
	registry, err := objectstorage.Load(env)
	mustCustomerJob(t, err)
	backend, err := registry.Default("us-east-1")
	mustCustomerJob(t, err)
	return path, backend
}
func (f *customerJobFixture) seedArtifact(t *testing.T, backend objectstorage.Backend, scope string) {
	t.Helper()
	bucket, err := f.store.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: f.account.ID, AppID: f.app.ID, Scope: scope, Name: "exports", Region: backend.Region, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "operation-exports"}, api.DefaultObjectBucketsPerApp)
	mustCustomerJob(t, err)
	_, err = f.store.ClaimObjectBucket(t.Context(), f.account.ID, f.app.ID, bucket.ID, "native-fixture", "provisioning")
	mustCustomerJob(t, err)
	mustCustomerJob(t, f.store.FinishObjectBucket(t.Context(), bucket.ID, "native-fixture", "ready"))
	mustCustomerJob(t, f.store.ClaimObjectInventory(t.Context(), bucket.ID, "native-fixture"))
	mustCustomerJob(t, f.store.FinishObjectInventory(t.Context(), bucket.ID, "native-fixture", int64(len(customerJobCSV)), 1))
	mustCustomerJob(t, f.store.RecordObjectUsageReport(t.Context(), api.ObjectStorageUsageReport{AccountID: f.account.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "native-fixture", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().Add(-time.Second)}))
	f.artifact = api.OperationArtifactRequest{ReportID: "export-file", Name: "export.csv", URI: fmt.Sprintf("obj://%s/%s/export.csv", f.app.ID, bucket.ID), SizeBytes: int64(len(customerJobCSV)), SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(customerJobCSV)))}
}

func (f *customerJobFixture) webhookReceiver(t *testing.T) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		timestamp, parseErr := strconv.ParseInt(r.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
		if err == nil {
			err = parseErr
		}
		if err == nil {
			err = webhookout.NewSigner([]byte(customerJobWebhookSecret)).Verify(timestamp, r.Header.Get("X-Faas-Delivery-Id"), body, strings.TrimPrefix(r.Header.Get("X-Faas-Webhook-Signature"), "sha256="))
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
		if err == nil && (event.Type != string(state.AppWebhookEventOperationFinished) || event.Data.Operation.ID == "" || !event.Data.Operation.State.Terminal()) {
			err = errors.New("invalid completed operation payload")
		}
		if err != nil {
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
func (f *customerJobFixture) waitDeliveryFailure(t *testing.T, id string) {
	t.Helper()
	customerJobEventually(t, time.Minute, "independent failed notification attempt", func() bool {
		d, err := f.owner.GetOperationDelivery(t.Context(), f.app.Slug, id)
		return err == nil && d.BusinessState == api.OperationSucceeded && d.LastResponseCode == http.StatusServiceUnavailable && d.Attempts >= 1 && d.State != "succeeded"
	})
	if f.webhookInvalid.Load() {
		t.Fatal("completion webhook signature invalid")
	}
}
func (f *customerJobFixture) waitDeliverySuccess(t *testing.T, id string) {
	t.Helper()
	customerJobEventually(t, 2*time.Minute, "retried completion delivery", func() bool {
		d, err := f.owner.GetOperationDelivery(t.Context(), f.app.Slug, id)
		return err == nil && d.BusinessState == api.OperationSucceeded && d.State == "succeeded" && d.Attempts >= 2
	})
	if f.webhookInvalid.Load() {
		t.Fatal("completion webhook signature invalid")
	}
	attempts, err := f.owner.GetOperationDeliveryAttempts(t.Context(), f.app.Slug, id, 10, "")
	mustCustomerJob(t, err)
	if len(attempts.Attempts) < 2 {
		t.Fatal("delivery attempt ledger missing retry")
	}
	delivery, err := f.owner.GetOperationDelivery(t.Context(), f.app.Slug, id)
	mustCustomerJob(t, err)
	receipt, ok := f.webhookReceipt.Load().(customerJobWebhookReceipt)
	if !ok || receipt.DeliveryID != delivery.DeliveryID || receipt.Operation.ID != id || receipt.Operation.Generation != 1 || receipt.Operation.State != api.OperationSucceeded || len(receipt.Operation.Artifacts) != 1 {
		t.Fatal("signed notification did not identify the completed native operation")
	}
}

type customerJobWebhookReceipt struct {
	DeliveryID string
	Operation  api.OperationResponse
}

func (f *customerJobFixture) waitNoInstances(t *testing.T) {
	t.Helper()
	customerJobEventually(t, time.Minute, "native job instance cleanup", func() bool {
		instances, err := f.store.ListJobInstances(context.Background())
		return err == nil && len(instances) == 0
	})
}
func (f *customerJobFixture) assertStaleProof(t *testing.T, id string, proof api.OperationJobRuntimeProof) {
	t.Helper()
	if proof.Capability == "" || proof.RunID == "" || proof.InstanceID == "" || proof.Generation < 1 || proof.Attempt != 1 {
		t.Fatal("guest never supplied a complete native proof")
	}
	_, err := api.NewClient(f.h.APIDURL, "").GetJobOperationExecutionControl(t.Context(), id, proof)
	var problem *api.APIError
	if !errors.As(err, &problem) || (problem.Problem.Status != http.StatusConflict && problem.Problem.Status != http.StatusNotFound) {
		t.Fatal("completed native task proof was not rejected with a stale-binding response")
	}
}
func customerJobEventually(t *testing.T, budget time.Duration, label string, ready func() bool) {
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
func mustCustomerJob(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

// Retain the node's artifact backend so cold boot can read its staged bases
// and scan sidecars. Cleanup deletes only keys bound to this fixture account.
func (f *customerJobFixture) rememberResultFiles(ctx context.Context, id string) error {
	op, err := f.store.OperationByID(ctx, f.account.ID, f.tenant.ID, id)
	if err != nil {
		return err
	}
	for _, key := range op.ArtifactStorageKeys {
		f.artifactKeys[key] = true
	}
	return nil
}
func (f *customerJobFixture) cleanResultFiles(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, id := range f.operationIDs {
		if err := f.rememberResultFiles(ctx, id); err != nil {
			t.Errorf("read fixture file cleanup metadata: %v", err)
		}
	}
	if len(f.artifactKeys) == 0 {
		return
	}
	backend, err := storage.BackendFromEnvContext(ctx)
	if err != nil {
		t.Errorf("fixture file cleanup backend: %v", err)
		return
	}
	account, err := uuid.Parse(f.account.ID)
	if err != nil {
		t.Error("invalid fixture account cleanup identity")
		return
	}
	for key := range f.artifactKeys {
		if !strings.HasPrefix(key, "operation-results/"+account.String()+"/") {
			t.Error("fixture cleanup refused a foreign result key")
			continue
		}
		if err := backend.Delete(ctx, key); err != nil {
			t.Errorf("delete fixture result file: %v", err)
		}
	}
}
