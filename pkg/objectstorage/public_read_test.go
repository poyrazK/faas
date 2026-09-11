package objectstorage

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type publicReadTestProvider struct{ endpoint string }

func (p publicReadTestProvider) CreateBucket(context.Context, string) error { return nil }
func (p publicReadTestProvider) DeleteBucket(context.Context, string) error { return nil }
func (p publicReadTestProvider) ListObjects(context.Context, string, string, string, int32) (ObjectPage, error) {
	return ObjectPage{}, nil
}
func (p publicReadTestProvider) DeleteObject(context.Context, string, string) error { return nil }
func (p publicReadTestProvider) Presign(_ context.Context, _ string, req SignRequest) (SignedRequest, error) {
	return SignedRequest{URL: p.endpoint, Method: req.Method}, nil
}
func (p publicReadTestProvider) EnsureMultipartUpload(context.Context, string, MultipartCreateRequest) (string, error) {
	return "upload", nil
}
func (p publicReadTestProvider) PresignMultipartPart(context.Context, string, MultipartPartRequest) (SignedRequest, error) {
	return SignedRequest{}, nil
}
func (p publicReadTestProvider) ListMultipartParts(context.Context, string, MultipartListPartsRequest) (MultipartPartsPage, error) {
	return MultipartPartsPage{}, nil
}
func (p publicReadTestProvider) CompleteMultipartUpload(context.Context, string, MultipartCompleteRequest) error {
	return nil
}
func (p publicReadTestProvider) AbortMultipartUpload(context.Context, string, MultipartAbortRequest) error {
	return nil
}

func TestValidPublicReadPath(t *testing.T) {
	for _, tc := range []struct {
		public bool
		path   string
		valid  bool
	}{
		{false, "", true}, {false, "/assets", false}, {true, "/assets", true},
		{true, "/static/assets", true}, {true, "assets", false}, {true, "/assets/", false},
		{true, "/assets/../private", false}, {true, "/assets?x", false},
	} {
		if got := ValidPublicReadPath(tc.public, tc.path); got != tc.valid {
			t.Errorf("ValidPublicReadPath(%v, %q) = %v, want %v", tc.public, tc.path, got, tc.valid)
		}
	}
}

func TestPublicReadHandlerServesWithoutNext(t *testing.T) {
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/" {
			t.Errorf("upstream request = %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "image/png")
		_, _ = io.WriteString(w, "png")
	}))
	defer provider.Close()
	registry, err := NewRegistry(Config{
		Accounting:    &api.ObjectStoragePolicy{MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1000, MaxMonthlyCostMillicents: 1 << 30, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1 << 30, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600},
		DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"},
		Backends: []BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: "https://storage.test", S3Region: "us-east-1"}},
	}, func(string) string { return "" }, map[string]Factory{"s3": func(BackendConfig, func(string) string) (Provider, error) {
		return publicReadTestProvider{endpoint: provider.URL}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	store := state.NewMemStore()
	account, err := store.CreateAccount(context.Background(), "public-read@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(context.Background(), state.App{AccountID: account.ID, Slug: "assets-app", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := store.ReserveObjectBucket(context.Background(), state.ObjectBucket{
		ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1",
		BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "gregale-assets",
		PublicRead: true, ServeAt: "/assets",
	}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimObjectBucket(context.Background(), account.ID, app.ID, bucket.ID, "token", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectBucket(context.Background(), bucket.ID, "token", "ready"); err != nil {
		t.Fatal(err)
	}
	if err := store.ClaimObjectInventory(context.Background(), bucket.ID, "inventory"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectInventory(context.Background(), bucket.ID, "inventory", 0, 0); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if err := store.RecordObjectUsageReport(context.Background(), api.ObjectStorageUsageReport{
		AccountID: account.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		Source: "test", PeriodStart: state.ObjectStoragePeriod(now), ObservedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	nextCalled := false
	h, err := NewPublicReadHandler(PublicReadConfig{
		Store: store, Registry: registry, RequestMetrics: store, Accounting: store, AppsDomain: "apps.example",
		Next: http.HandlerFunc(func(http.ResponseWriter, *http.Request) { nextCalled = true }),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://assets-app.apps.example/assets/logo.png", nil)
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusOK || res.Body.String() != "png" {
		t.Fatalf("response = %d %q", res.Code, res.Body.String())
	}
	if nextCalled {
		t.Fatal("public asset fell through to app handler")
	}
	if got := res.Header().Get("Cache-Control"); got != publicImmutableCacheControl {
		t.Fatalf("Cache-Control = %q", got)
	}
	metrics, err := store.ListObjectStorageProviderRequestMetrics(context.Background(), backend.ID, backend.Fingerprint, time.Now().UTC())
	if err != nil || len(metrics) != 1 || metrics[0].RequestCount != 1 || metrics[0].EgressBytes != 3 {
		t.Fatalf("metrics = %+v, err=%v", metrics, err)
	}
}
