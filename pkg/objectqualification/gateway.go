//go:build linux

package objectqualification

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	objectapi "github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type gatewayFixture struct {
	server   *httptest.Server
	client   *s3.Client
	store    *state.MemStore
	registry *objectstorage.Registry
	bucket   state.ObjectBucket
	subject  string
	clock    atomic.Int64
}

// Recovery only needs the explicitly implemented host methods. The embedded
// interface is never a state.Store and unsupported apid handlers are not wired.
type recoveryHost struct {
	objectapi.Host
	fixture *gatewayFixture
}

func (h recoveryHost) Store() state.Store                                         { return h.fixture.store }
func (h recoveryHost) ObjectStorage() *objectstorage.Registry                     { return h.fixture.registry }
func (h recoveryHost) S3RuntimeEnabled() bool                                     { return false }
func (h recoveryHost) Logger() *slog.Logger                                       { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }
func (h recoveryHost) EmitAudit(context.Context, string, *string, map[string]any) {}

func (g *gatewayFixture) service() objectapi.Service {
	return objectapi.Service{Host: recoveryHost{fixture: g}}
}

func (l *lab) newGateway(ctx context.Context) (*gatewayFixture, error) {
	g := &gatewayFixture{store: state.NewMemStore(), subject: uuid.NewString()}
	g.store.SetClockForTest(func() time.Time { return time.Now().Add(time.Duration(g.clock.Load())).UTC() })
	// These synthetic allowances admit only this isolated fixture's bounded
	// work. They are neither a production accounting configuration nor billing.
	budget := api.MaxObjectStoragePolicyValue
	policy := api.ObjectStoragePolicy{MaxAccountBytes: budget, MaxBucketBytes: budget, MaxAccountKeys: api.ObjectStorageInventoryMaxPages * api.MaxObjectS3ListItems, MaxMonthlyCostMillicents: budget, MaxMonthlyRequests: budget, MaxMonthlyEgressBytes: budget, MaxMonthlyAuthorizations: budget, MaxReportAgeSeconds: api.MaxObjectStorageReportAgeSeconds}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "qualification"}, Backends: []objectstorage.BackendConfig{{ID: "qualification", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "disposable-qualification", Endpoint: l.proxy.server.URL, AllowHTTP: true, PathStyle: true, OwnedNodeReceipts: true, OwnedNodeSettlements: l.options.RecoveryRetention, OwnedNodeUsageCounters: true, AccessKeyEnv: "QUALIFICATION_KEY", SecretKeyEnv: "QUALIFICATION_SECRET"}}}, func(name string) string {
		if name == "QUALIFICATION_KEY" {
			return l.key
		}
		if name == "QUALIFICATION_SECRET" {
			return l.secret
		}
		return ""
	}, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		return nil, err
	}
	g.registry = registry
	backend, err := registry.Default("us-east-1")
	if err != nil {
		return nil, err
	}
	account, err := g.store.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		return nil, err
	}
	app, err := g.store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "qualification", Status: state.AppActive})
	if err != nil {
		return nil, err
	}
	g.bucket, err = g.store.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", PhysicalName: "physical", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		return nil, err
	}
	if _, err = g.store.ClaimObjectBucket(ctx, account.ID, app.ID, g.bucket.ID, "create", "provisioning"); err != nil {
		return nil, err
	}
	if err = g.store.FinishObjectBucket(ctx, g.bucket.ID, "create", "ready"); err != nil {
		return nil, err
	}
	g.bucket.State = "ready"
	customerKey, customerSecret := "GREG"+uuid.NewString(), uuid.NewString()
	if _, err = g.store.CreateObjectS3Credential(ctx, state.ObjectS3Credential{ID: g.subject, AccountID: account.ID, BucketID: g.bucket.ID, AccessKeyID: customerKey, SecretSealed: []byte("qualification-fixture"), KID: "qualification", Label: "qualification", Permission: state.ObjectBucketPermissionReadWrite, Status: state.ObjectS3CredentialStatusActive}, api.DefaultObjectBucketsPerApp); err != nil {
		return nil, err
	}
	if err = g.store.ClaimObjectInventory(ctx, g.bucket.ID, "initial"); err != nil {
		return nil, err
	}
	if err = g.store.FinishObjectInventory(ctx, g.bucket.ID, "initial", 0, 0); err != nil {
		return nil, err
	}
	if err = g.store.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: account.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}); err != nil {
		return nil, err
	}
	spool := l.newPath("spool")
	if err = os.Mkdir(spool, 0o700); err != nil {
		return nil, err
	}
	g.server = httptest.NewUnstartedServer(nil)
	closeOnError := true
	defer func() {
		if closeOnError {
			g.server.Close()
		}
	}()
	handler, err := s3gateway.New(s3gateway.Config{Registry: registry, Store: g.store, RequestMetrics: g.store, Host: g.server.Listener.Addr().String(), Region: "us-east-1", SpoolDir: spool, MinSpoolFreeBytes: 1, MaxConcurrentPuts: l.options.Workers + 1, OpenSecret: func([]byte) (string, error) { return customerSecret, nil }, HTTPClient: &http.Client{Timeout: api.ObjectQualificationRequestTimeout}, Log: slog.New(slog.NewJSONHandler(io.Discard, nil))})
	if err != nil {
		return nil, err
	}
	g.server.Config.Handler = handler
	g.server.Start()
	g.client = s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String(g.server.URL), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(customerKey, customerSecret, ""), HTTPClient: &http.Client{Timeout: api.ObjectQualificationRequestTimeout, Transport: customerTransport{next: http.DefaultTransport, lab: l}}, RetryMaxAttempts: 1, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
	closeOnError = false
	return g, nil
}

func (g *gatewayFixture) receipt(ctx context.Context, id string) (state.ObjectUploadCompletion, error) {
	return g.store.GetObjectUploadReceipt(ctx, g.bucket.AccountID, g.bucket.AppID, "", g.subject, id)
}

func (g *gatewayFixture) recover(ctx context.Context) error {
	g.clock.Add(int64(api.ObjectUploadRecoveryLease + api.ObjectUploadRecoveryRetry))
	service := g.service()
	return service.ReconcileObjectUploads(ctx, nil)
}

func (g *gatewayFixture) inventory(ctx context.Context, bytes, keys int64) error {
	id := uuid.NewString()
	if err := g.store.ClaimObjectInventory(ctx, g.bucket.ID, id); err != nil {
		return err
	}
	service := g.service()
	if err := service.ScanObjectInventory(ctx, g.store, g.bucket, id); err != nil {
		return err
	}
	usage, err := g.store.ObjectUsage(ctx, g.bucket.AccountID, time.Now())
	if err != nil {
		return err
	}
	if len(usage.Buckets) != 1 || usage.Buckets[0].ObservedBytes != bytes || usage.Buckets[0].ObservedKeys != keys || usage.Buckets[0].MultipartBytes != 0 {
		return errors.New("reconciled capacity differs from the expected object model")
	}
	return nil
}
