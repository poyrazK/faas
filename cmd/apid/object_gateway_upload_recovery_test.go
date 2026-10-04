package main

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type acceptedGatewayObject struct {
	mu      sync.Mutex
	receipt string
	data    []byte
	puts    int
}

func (o *acceptedGatewayObject) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	o.mu.Lock()
	defer o.mu.Unlock()
	switch r.Method {
	case http.MethodPut:
		o.puts++
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		o.data = data
		o.receipt = r.Header.Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)
		if o.receipt == "" {
			o.receipt = r.URL.Query().Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)
		}
		if o.receipt == "" {
			t.Error("provider did not receive receipt")
		}
		// Commit the object, then lose the acknowledgment at the actual HTTP boundary.
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	case http.MethodHead:
		if o.data == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(o.data)))
		w.Header().Set("ETag", `"recovered"`)
		w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey, o.receipt)
	case http.MethodGet:
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<ListBucketResult><Name>physical</Name><IsTruncated>false</IsTruncated><KeyCount>0</KeyCount></ListBucketResult>`)
	default:
		t.Errorf("unexpected provider method %s", r.Method)
		w.WriteHeader(500)
	}
}

type gatewayRecoveryFixture struct {
	enabled    *atomic.Bool
	pool       *pgxpool.Pool
	st         *state.PgStore
	account    state.Account
	app        state.App
	bucket     state.ObjectBucket
	credential state.ObjectS3Credential
	registry   *objectstorage.Registry
	policy     api.ObjectStoragePolicy
	report     api.ObjectStorageUsageReport
	client     *awss3.Client
}

func newGatewayRecoveryFixture(t *testing.T, handler http.Handler, sourceBytes int64) gatewayRecoveryFixture {
	t.Helper()
	ctx := t.Context()
	pool := pgtest.OpenMigrated(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	st := state.NewPgStore(pool)
	acct, err := st.CreateAccount(ctx, "gateway-recovery@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "gateway-recovery", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(handler)
	t.Cleanup(upstream.Close)
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 3600}
	config := objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: upstream.URL, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true, AccessKeyEnv: "TEST_KEY", SecretKeyEnv: "TEST_SECRET"}}}
	registry, err := objectstorage.NewRegistry(config, func(string) string { return "upstream-secret" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Name: "assets", Scope: "default", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "physical", Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, acct.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	sourceKeys := int64(0)
	if sourceBytes > 0 {
		sourceKeys = 1
	}
	if err = st.ClaimObjectInventory(ctx, b.ID, "initial"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, b.ID, "initial", sourceBytes, sourceKeys); err != nil {
		t.Fatal(err)
	}
	report := api.ObjectStorageUsageReport{AccountID: acct.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}
	if err = st.RecordObjectUsageReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	const access = "GRGAAAAAAAAAAAAAAAAA"
	const secret = "0123456789012345678901234567890123456789"
	credential, err := st.CreateObjectS3Credential(ctx, state.ObjectS3Credential{ID: uuid.NewString(), AccountID: acct.ID, BucketID: b.ID, AccessKeyID: access, SecretSealed: []byte("sealed"), KID: "test", Label: "test", Permission: state.ObjectBucketPermissionReadWrite, Status: state.ObjectS3CredentialStatusActive}, 10)
	if err != nil {
		t.Fatal(err)
	}
	edge := httptest.NewUnstartedServer(nil)
	edgeURL := "http://" + edge.Listener.Addr().String()
	host, err := url.Parse(edgeURL)
	if err != nil {
		t.Fatal(err)
	}
	enabled := &atomic.Bool{}
	enabled.Store(true)
	h, err := s3gateway.New(s3gateway.Config{Registry: registry, Store: st, RequestMetrics: st, Enabled: enabled.Load, Host: host.Host, Region: "us-east-1", SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1, OpenSecret: func([]byte) (string, error) { return secret, nil }})
	if err != nil {
		t.Fatal(err)
	}
	edge.Config.Handler = h
	edge.Start()
	t.Cleanup(edge.Close)
	client := awss3.New(awss3.Options{Region: "us-east-1", BaseEndpoint: aws.String(edge.URL), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(access, secret, ""), RetryMaxAttempts: 1, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired})
	return gatewayRecoveryFixture{enabled: enabled, pool: pool, st: st, account: acct, app: app, bucket: b, credential: credential, registry: registry, policy: policy, report: report, client: client}
}

// adr: 535
func TestGatewayPUTLostAcknowledgmentRecoveryPG(t *testing.T) {
	object := &acceptedGatewayObject{}
	f := newGatewayRecoveryFixture(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { object.serve(t, w, r) }), 0)
	ctx := t.Context()
	pool, st, acct, app, b, credential, registry, policy, report, client := f.pool, f.st, f.account, f.app, f.bucket, f.credential, f.registry, f.policy, f.report, f.client
	var err error
	_, err = client.PutObject(ctx, &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("key"), Body: strings.NewReader("hello")})
	if err == nil {
		t.Fatal("lost acknowledgment was reported as success")
	}
	object.mu.Lock()
	id := object.receipt
	puts := object.puts
	object.mu.Unlock()
	if puts != 1 {
		t.Fatal("replayed provider request", puts)
	}
	receipt, err := st.GetObjectUploadReceipt(ctx, acct.ID, app.ID, "", credential.ID, id)
	if err != nil || receipt.Status != "pending" || receipt.Origin != "gateway" {
		t.Fatal(receipt, err)
	}
	// A new store/server has only durable state; no in-memory request ownership survives.
	recovered := state.NewPgStore(pool)
	e := setup(t, api.PlanHobby)
	e.s.store = recovered
	e.s.WithObjectStorage(registry)
	setS3Flag(t, e, false)
	report.ObservedAt = time.Now()
	report.CostMillicents = policy.MaxMonthlyCostMillicents
	if err = recovered.RecordObjectUsageReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	due := func(ctx context.Context, id string) {
		if _, e := pool.Exec(ctx, `UPDATE object_upload_completions SET recovery_retry_at=now()-interval '1 second' WHERE id=$1`, id); e != nil {
			t.Fatal(e)
		}
	}
	due(ctx, id)
	if err = e.s.reconcileObjectUploads(ctx, nil); err != nil {
		t.Fatal(err)
	}
	receipt, err = recovered.GetObjectUploadReceipt(ctx, acct.ID, app.ID, "", credential.ID, id)
	if err != nil || receipt.Status != "completed" || receipt.ETag != `"recovered"` {
		t.Fatal(receipt, err)
	}
	object.mu.Lock()
	puts = object.puts
	object.data = nil
	object.mu.Unlock()
	if puts != 1 {
		t.Fatal("recovery resent body", puts)
	}
	j, err := recovered.RequestObjectCapacityReconciliation(ctx, acct.ID, app.ID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = e.s.reconcileObjectCapacity(ctx, nil); err != nil {
		t.Fatal(err)
	}
	j, err = recovered.GetObjectCapacityReconciliation(ctx, acct.ID, b.ID, j.ID)
	if err != nil || j.State != "completed" || j.ReclaimedBytes != 5 {
		t.Fatal(j, err)
	}
	usage, err := recovered.ObjectUsage(ctx, acct.ID, time.Now())
	if err != nil || usage.Authorizations != 1 || usage.Reports[0].CostMillicents != policy.MaxMonthlyCostMillicents {
		t.Fatal("recovery changed monthly ledger", usage, err)
	}
	registry.Accounting.MaxMonthlyCostMillicents *= 2
	fresh, err := recovered.BeginTrackedGatewayUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, BucketID: b.ID, SubjectID: credential.ID, Key: "reuse", Bytes: 100, Status: "pending"}, registry.Accounting)
	if err != nil {
		t.Fatal("reclaimed capacity unavailable", err)
	}
	fresh, err = recovered.DispatchTrackedObjectUpload(ctx, acct.ID, b.ID, fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	// An overwrite preserves size/ETag but changes the receipt. It cannot prove this attempt.
	object.mu.Lock()
	object.data = make([]byte, 100)
	object.receipt = uuid.NewString()
	object.mu.Unlock()
	due(ctx, fresh.ID)
	if err = e.s.reconcileObjectUploads(ctx, nil); err != nil {
		t.Fatal(err)
	}
	pending, err := recovered.GetObjectUploadReceipt(ctx, acct.ID, app.ID, "", credential.ID, fresh.ID)
	if err != nil || pending.Status != "pending" {
		t.Fatal("overwrite falsely settled", pending, err)
	}
	if err = recovered.SettleObjectWrite(ctx, acct.ID, b.ID, fresh.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("legacy settlement bypass", err)
	}
}
