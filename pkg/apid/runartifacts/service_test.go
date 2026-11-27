package runartifacts_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/object"
	"github.com/onebox-faas/faas/pkg/apid/runartifacts"
	"github.com/onebox-faas/faas/pkg/apid/runfiles"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type target struct {
	objectstorage.Provider
	mu               sync.Mutex
	writes           int
	key, receipt     string
	body             []byte
	outcome          error
	started, release chan struct{}
}

func (p *target) WriteTrackedObject(ctx context.Context, _, key, receipt string, body io.Reader, size int64, _ objectstorage.ObjectMetadata) (objectstorage.UploadResult, error) {
	if p.started != nil {
		close(p.started)
		select {
		case <-p.release:
		case <-ctx.Done():
			return objectstorage.UploadResult{}, ctx.Err()
		}
	}
	data, err := io.ReadAll(io.LimitReader(body, size+1))
	if err != nil || int64(len(data)) != size {
		return objectstorage.UploadResult{}, objectstorage.ErrWriteRejected
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.writes++
	p.key, p.receipt, p.body = key, receipt, data
	return objectstorage.UploadResult{ETag: "verified-etag"}, p.outcome
}
func (p *target) ConfirmTrackedObject(_ context.Context, _, key, receipt string, size int64) (objectstorage.UploadResult, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.key != key || p.receipt != receipt || int64(len(p.body)) != size {
		return objectstorage.UploadResult{}, objectstorage.ErrUnavailable
	}
	return objectstorage.UploadResult{ETag: "verified-etag"}, nil
}

type fixture struct {
	svc      runartifacts.Service
	access   runartifacts.Access
	req      api.DeliverRunArtifactRequest
	store    *state.MemStore
	provider *target
	account  state.Account
	app      state.App
	bytes    []byte
	opens    atomic.Int32
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctx := t.Context()
	st := state.NewMemStore()
	account, err := st.CreateAccount(ctx, "run-delivery@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "reports", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	p := &target{}
	policy := &api.ObjectStoragePolicy{MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1000, MaxMonthlyCostMillicents: 1 << 30, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1 << 30, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: "https://storage.invalid", S3Region: "us-east-1"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"s3": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) { return p, nil }})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "reports", Scope: "default", Region: "us-east-1", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "private-reports"}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, account.ID, app.ID, bucket.ID, "provision", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, bucket.ID, "provision", "ready"); err != nil {
		t.Fatal(err)
	}
	if _, err = st.UpsertObjectUploadRoute(ctx, state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, BucketID: bucket.ID, Name: "reports", KeyPrefix: "customer-reports", MaxBytes: 8 << 20, AllowedContentTypes: []string{"text/html"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err = st.ClaimObjectInventory(ctx, bucket.ID, "inventory"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(ctx, bucket.ID, "inventory", 0, 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err = st.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: account.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(now), ObservedAt: now}); err != nil {
		t.Fatal(err)
	}
	data := []byte("<!doctype html><h1>Customer report</h1>")
	digest := sha256.Sum256(data)
	return &fixture{svc: runartifacts.Service{Store: st, Registry: registry, Enabled: true, Slots: make(chan struct{}, api.RunArtifactDeliveryConcurrency), TempDir: t.TempDir()}, store: st, provider: p, account: account, app: app, bytes: data,
		access: runartifacts.Access{AccountID: account.ID, AppID: app.ID, AppSlug: app.Slug, AuthorizeBucket: func(context.Context, state.ObjectBucket) error { return nil }},
		req:    api.DeliverRunArtifactRequest{IdempotencyKey: "completion-one-report", ExecutionID: uuid.NewString(), ArtifactName: "report.html", SizeBytes: int64(len(data)), SHA256: "sha256:" + hex.EncodeToString(digest[:]), ContentType: "text/html"}}
}

func (f *fixture) open(context.Context, api.DeliverRunArtifactRequest) (io.ReadCloser, error) {
	f.opens.Add(1)
	return io.NopCloser(bytes.NewReader(f.bytes)), nil
}

func TestRunArtifactDeliveryReplaySurvivesSourceExpiryAndWriterRollback(t *testing.T) {
	f := newFixture(t)
	first, err := f.svc.Deliver(t.Context(), f.access, "reports", f.req, f.open)
	if err != nil || first.Status != "completed" || first.Object == nil {
		t.Fatal(first, err)
	}
	if !bytes.Equal(f.provider.body, f.bytes) {
		t.Fatal("wrong delivered bytes")
	}
	entries, err := os.ReadDir(f.svc.TempDir)
	if err != nil || len(entries) != 0 {
		t.Fatal("plaintext staging remained", entries, err)
	}
	f.svc.Enabled = false
	replay, err := f.svc.Deliver(t.Context(), f.access, "reports", f.req, func(context.Context, api.DeliverRunArtifactRequest) (io.ReadCloser, error) {
		t.Fatal("expired source was reopened")
		return nil, nil
	})
	if err != nil || replay.ID != first.ID || f.provider.writes != 1 || f.opens.Load() != 1 {
		t.Fatal(replay, err, f.provider.writes)
	}
	got, _, err := f.svc.Get(t.Context(), f.access, "reports", first.ID)
	if err != nil || got.Object.Key != first.Object.Key {
		t.Fatal(got, err)
	}
	changed := f.req
	changed.SHA256 = "sha256:" + string(bytes.Repeat([]byte("a"), 64))
	if _, err = f.svc.Deliver(t.Context(), f.access, "reports", changed, f.open); runartifacts.Problem(err).Status != 409 {
		t.Fatal("changed request replayed", err)
	}
	f.access.AuthorizeBucket = func(context.Context, state.ObjectBucket) error { return state.ErrNotFound }
	if _, _, err = f.svc.Get(t.Context(), f.access, "reports", first.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("revoked destination remained accessible", err)
	}
}

func TestRunArtifactIntegrityFailureNeverDispatchesOrReservesDestination(t *testing.T) {
	for _, corruption := range []string{"short", "long", "checksum"} {
		t.Run(corruption, func(t *testing.T) {
			f := newFixture(t)
			switch corruption {
			case "short":
				f.bytes = f.bytes[:len(f.bytes)-1]
			case "long":
				f.bytes = append(f.bytes, 'x')
			case "checksum":
				f.bytes[0] = 'x'
			}
			if _, err := f.svc.Deliver(t.Context(), f.access, "reports", f.req, f.open); err == nil || runartifacts.Problem(err).Status != 422 {
				t.Fatal(err)
			}
			if f.provider.writes != 0 {
				t.Fatal("corrupt bytes reached provider")
			}
			rows, err := f.store.ListObjectWriteReceipts(t.Context(), f.account.ID, f.app.ID, mustBucket(t, f), "all", 10, "")
			if err != nil || len(rows.Items) != 0 {
				t.Fatal("corrupt source reserved a write", rows, err)
			}
		})
	}
}

func mustBucket(t *testing.T, f *fixture) string {
	t.Helper()
	route, err := f.store.GetObjectUploadRoute(t.Context(), f.account.ID, f.app.ID, "reports")
	if err != nil {
		t.Fatal(err)
	}
	return route.BucketID
}

type recoveryHost struct {
	object.Host
	store    *state.MemStore
	registry *objectstorage.Registry
}

func (h recoveryHost) Store() state.Store                     { return h.store }
func (h recoveryHost) ObjectStorage() *objectstorage.Registry { return h.registry }

func TestRunArtifactLostProviderAcknowledgementRecoversWithoutAnotherWrite(t *testing.T) {
	f := newFixture(t)
	f.provider.outcome = objectstorage.ErrUnavailable
	first, err := f.svc.Deliver(t.Context(), f.access, "reports", f.req, f.open)
	if err != nil || first.Status != "pending" || first.Object != nil {
		t.Fatal(first, err)
	}
	// A fresh apid object worker owns the persisted receipt after restart.
	f.store.SetClockForTest(func() time.Time { return time.Now().UTC().Add(api.ObjectUploadRecoveryRetry + time.Hour) })
	recovery := object.Service{Host: recoveryHost{store: f.store, registry: f.svc.Registry}}
	if err = recovery.ReconcileObjectUploads(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	f.svc.Enabled = false
	done, err := f.svc.Deliver(t.Context(), f.access, "reports", f.req, nil)
	if err != nil || done.ID != first.ID || done.Status != "completed" || done.Object == nil || f.provider.writes != 1 {
		t.Fatal(done, err, f.provider.writes)
	}
}

func TestRunArtifactConcurrentDeliveryHasOneProviderAttempt(t *testing.T) {
	f := newFixture(t)
	f.provider.started, f.provider.release = make(chan struct{}), make(chan struct{})
	finished := make(chan error, 1)
	go func() { _, err := f.svc.Deliver(t.Context(), f.access, "reports", f.req, f.open); finished <- err }()
	select {
	case <-f.provider.started:
	case <-time.After(5 * time.Second):
		t.Fatal("provider did not start")
	}
	replay, err := f.svc.Deliver(t.Context(), f.access, "reports", f.req, f.open)
	close(f.provider.release)
	if firstErr := <-finished; firstErr != nil {
		t.Fatal(firstErr)
	}
	if err != nil || replay.Status != "pending" || f.provider.writes != 1 {
		t.Fatal(replay, err, f.provider.writes)
	}
}

func TestRunArtifactEncryptedSourceStreamsToVerifiedDurableDestination(t *testing.T) {
	f := newFixture(t)
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	files := runfiles.Service{Store: f.store, Storage: backend, Recipients: []*age.X25519Recipient{identity.Recipient()}, Identities: []*age.X25519Identity{identity}}
	access := runfiles.Access{Account: f.account, Wide: true}
	prepared, err := files.Prepare(t.Context(), access, api.CreateExecutionFileRequest{SizeBytes: f.req.SizeBytes, SHA256: f.req.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = files.Upload(t.Context(), access, prepared.ID, f.req.SizeBytes, f.req.SHA256, io.NopCloser(bytes.NewReader(f.bytes))); err != nil {
		t.Fatal(err)
	}
	result, err := f.svc.Deliver(t.Context(), f.access, "reports", f.req, func(ctx context.Context, _ api.DeliverRunArtifactRequest) (io.ReadCloser, error) {
		_, body, err := files.Open(ctx, access, prepared.ID)
		return body, err
	})
	if err != nil || result.Status != "completed" || !bytes.Equal(f.provider.body, f.bytes) {
		t.Fatal(result, err)
	}
}
