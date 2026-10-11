// ADR-521: customer operations preserve ownership, execution fences and independent delivery.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type operationArtifactTestProvider struct {
	objectstorage.Provider
	mu      sync.Mutex
	body    []byte
	reads   int
	missing bool
}

// A lost write response leaves real bytes behind. The storage intent must
// survive that uncertainty and let cleanup remove the abandoned copy.
type operationArtifactInterruptedStorage struct {
	storage.StorageBackend
	key               string
	deleteUnavailable bool
}

func (s *operationArtifactInterruptedStorage) Put(ctx context.Context, key string, reader io.Reader) error {
	if err := s.StorageBackend.Put(ctx, key, reader); err != nil {
		return err
	}
	s.key = key
	return objectstorage.ErrUnavailable
}

func (s *operationArtifactInterruptedStorage) Delete(ctx context.Context, key string) error {
	if s.deleteUnavailable {
		return objectstorage.ErrUnavailable
	}
	return s.StorageBackend.Delete(ctx, key)
}

func (p *operationArtifactTestProvider) ReadObject(context.Context, string, string) (io.ReadCloser, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.reads++
	if p.missing {
		return nil, objectstorage.ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(append([]byte(nil), p.body...))), nil
}
func (p *operationArtifactTestProvider) replace(body string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.body = []byte(body)
}

type operationArtifactFixtureStore interface {
	state.ObjectBucketStore
	state.ObjectStorageAccountingStore
}

func operationArtifactFixture(t *testing.T, s *server, store operationArtifactFixtureStore, acct state.Account, app state.App, scope string) (*operationArtifactTestProvider, api.OperationArtifactRequest) {
	t.Helper()
	ctx := t.Context()
	body := []byte("id,count\nalice,1\n")
	p := &operationArtifactTestProvider{body: body}
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 500, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 1000, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "artifacts"}, Backends: []objectstorage.BackendConfig{{ID: "artifacts", Driver: "fixture", Region: "us-east-1", Namespace: "artifacts", Endpoint: "https://artifacts.example.test", S3Region: "us-east-1", UsageReportsPath: "/tmp/operations-artifact-usage.json"}}}, func(string) string { return "" }, map[string]objectstorage.Factory{"fixture": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) { return p, nil }})
	if err != nil {
		t.Fatal(err)
	}
	retained, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.WithOperationArtifactStorage(retained)
	s.WithObjectStorage(registry)
	if err := s.runtimeConfig.apply(runtimeConfigS3, []byte(`true`)); err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := store.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Scope: scope, Name: "exports", Region: backend.Region, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "operation-exports"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimObjectBucket(ctx, acct.ID, app.ID, bucket.ID, "fixture", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectBucket(ctx, bucket.ID, "fixture", "ready"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimObjectInventory(ctx, bucket.ID, "fixture"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectInventory(ctx, bucket.ID, "fixture", int64(len(body)), 1); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: acct.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "fixture", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().Add(-time.Second)}); err != nil {
		t.Fatal(err)
	}
	return p, api.OperationArtifactRequest{ReportID: "export-file", Name: "export.csv", URI: fmt.Sprintf("obj://%s/%s/export.csv", app.ID, bucket.ID), SizeBytes: int64(len(body)), SHA256: fmt.Sprintf("sha256:%x", sha256.Sum256(body))}
}

func TestOperationArtifactSpoolCapacityAndCleanup(t *testing.T) {
	budget := operationArtifactBudget{}
	for i := 0; i < api.OperationArtifactTransfersPerAccount; i++ {
		if !budget.reserve("one", 0) {
			t.Fatal("slot unavailable")
		}
	}
	if budget.reserve("one", 0) {
		t.Fatal("unbounded zero-byte transfers")
	}
	for i := 0; i < api.OperationArtifactTransfersPerAccount; i++ {
		if !budget.reserve("two", 0) {
			t.Fatal("other account blocked")
		}
	}
	if budget.reserve("three", 0) {
		t.Fatal("node transfer limit bypassed")
	}
	budget.release("one", 0)
	if !budget.reserve("three", 0) {
		t.Fatal("slot not released")
	}
	if budget.reserve("four", api.OperationArtifactSpoolMaxBytes) {
		t.Fatal("spool capacity bypassed")
	}
}
