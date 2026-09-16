package objectstorage

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// uploadTestProvider is deliberately a Provider plus the optional writer
// capability. The endpoint must use the capability without making it part of
// the portable Provider contract.
type uploadTestProvider struct {
	Provider
	err         error
	body        []byte
	bucket      string
	key         string
	contentType string
}

func (p *uploadTestProvider) WriteObject(_ context.Context, bucket, key string, body io.Reader, size int64, metadata ObjectMetadata) (UploadResult, error) {
	p.bucket = bucket
	p.key = key
	p.contentType = metadata.ContentType
	data, err := io.ReadAll(body)
	if err != nil {
		return UploadResult{}, err
	}
	p.body = data
	if int64(len(data)) != size {
		return UploadResult{}, errors.New("test provider received an unexpected size")
	}
	if p.err != nil {
		return UploadResult{}, p.err
	}
	return UploadResult{ETag: "etag-test"}, nil
}

type uploadTestAccounting struct {
	state.ObjectStorageAccountingStore
	err   error
	calls int
}

func (a *uploadTestAccounting) AdmitObjectURL(context.Context, string, string, string, int64, bool, api.ObjectStoragePolicy) error {
	a.calls++
	return a.err
}

type uploadFixture struct {
	handler    http.Handler
	store      *state.MemStore
	provider   *uploadTestProvider
	accounting *uploadTestAccounting
	account    state.Account
	app        state.App
	key        state.APIKey
	route      state.ObjectUploadRoute
	token      string
}

func newUploadFixture(t *testing.T) uploadFixture {
	t.Helper()
	ctx := context.Background()
	store := state.NewMemStore()
	account, err := store.CreateAccount(ctx, "upload-test@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "upload-test", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	provider := &uploadTestProvider{}
	policy := &api.ObjectStoragePolicy{
		MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1000,
		MaxMonthlyCostMillicents: 1 << 30, MaxMonthlyRequests: 1000,
		MaxMonthlyEgressBytes: 1 << 30, MaxMonthlyAuthorizations: 1000,
		MaxReportAgeSeconds: 3600,
	}
	registry, err := NewRegistry(Config{
		Accounting: policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"},
		Backends: []BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: "https://storage.test", S3Region: "us-east-1"}},
	}, func(string) string { return "" }, map[string]Factory{
		"s3": func(BackendConfig, func(string) string) (Provider, error) {
			return provider, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := store.ReserveObjectBucket(ctx, state.ObjectBucket{
		ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "uploads", Scope: "default",
		Region: "us-east-1", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint,
		PhysicalName: "gregale-upload-test",
	}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ClaimObjectBucket(ctx, account.ID, app.ID, bucket.ID, "upload-test-token", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err := store.FinishObjectBucket(ctx, bucket.ID, "upload-test-token", "ready"); err != nil {
		t.Fatal(err)
	}
	route := state.ObjectUploadRoute{
		ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "avatar", BucketID: bucket.ID,
		KeyPrefix: "avatars", MaxBytes: 64, AllowedContentTypes: []string{"image/*"}, Enabled: true,
	}
	if _, err := store.UpsertObjectUploadRoute(ctx, route); err != nil {
		t.Fatal(err)
	}
	token, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := store.CreateAPIKey(ctx, account.ID, hash, "upload-test", []string{"storage:write"})
	if err != nil {
		t.Fatal(err)
	}
	accounting := &uploadTestAccounting{}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Next", "called")
		w.WriteHeader(http.StatusTeapot)
	})
	handler, err := NewUploadHandler(UploadConfig{
		Store: store, Routes: store, Buckets: store, Authenticator: store, Registry: registry,
		Accounting: accounting, AppsDomain: "apps.test", Next: next,
	})
	if err != nil {
		t.Fatal(err)
	}
	return uploadFixture{handler: handler, store: store, provider: provider, accounting: accounting, account: account, app: app, key: key, route: route, token: token}
}

func (f uploadFixture) request(method, path, body, contentType string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://upload-test.apps.test"+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+f.token)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	res := httptest.NewRecorder()
	f.handler.ServeHTTP(res, req)
	return res
}

func TestUploadHandlerStreamsAuthenticatedUpload(t *testing.T) {
	f := newUploadFixture(t)
	res := f.request(http.MethodPost, "/uploads/avatar", "avatar", "image/png; charset=utf-8")
	if res.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", res.Code, res.Body.String())
	}
	var response struct {
		BucketID    string `json:"bucket_id"`
		Key         string `json:"key"`
		Bytes       int64  `json:"bytes"`
		ContentType string `json:"content_type"`
		ETag        string `json:"etag"`
		Status      string `json:"status"`
	}
	if err := json.NewDecoder(res.Body).Decode(&response); err != nil {
		t.Fatal(err)
	}
	if response.BucketID == "" || !strings.HasPrefix(response.Key, "avatars/"+f.key.ID+"/") {
		t.Fatalf("response does not contain an owner-scoped key: %+v", response)
	}
	if response.Bytes != int64(len("avatar")) || response.ContentType != "image/png" || response.ETag != "etag-test" || response.Status != "completed" {
		t.Fatalf("unexpected response: %+v", response)
	}
	if string(f.provider.body) != "avatar" || f.provider.bucket != "gregale-upload-test" || f.provider.key != response.Key || f.provider.contentType != "image/png" {
		t.Fatalf("provider received bucket=%q key=%q content_type=%q body=%q", f.provider.bucket, f.provider.key, f.provider.contentType, f.provider.body)
	}
	if f.accounting.calls != 1 {
		t.Fatalf("accounting calls = %d, want 1", f.accounting.calls)
	}
}

func TestUploadHandlerRejectsBeforeProviderWrite(t *testing.T) {
	tests := []struct {
		name       string
		configure  func(*uploadFixture, *http.Request)
		wantStatus int
	}{
		{name: "missing bearer", configure: func(f *uploadFixture, r *http.Request) { r.Header.Del("Authorization") }, wantStatus: http.StatusUnauthorized},
		{name: "wrong method", configure: func(_ *uploadFixture, r *http.Request) { r.Method = http.MethodGet }, wantStatus: http.StatusMethodNotAllowed},
		{name: "content type", configure: func(_ *uploadFixture, r *http.Request) { r.Header.Set("Content-Type", "text/plain") }, wantStatus: http.StatusUnsupportedMediaType},
		{name: "size", configure: func(_ *uploadFixture, r *http.Request) {
			r.Body = io.NopCloser(bytes.NewReader(bytes.Repeat([]byte("x"), 65)))
			r.ContentLength = 65
			r.Header.Set("Content-Type", "image/png")
		}, wantStatus: http.StatusRequestEntityTooLarge},
		{name: "length required", configure: func(_ *uploadFixture, r *http.Request) {
			r.ContentLength = -1
			r.Header.Set("Content-Type", "image/png")
		}, wantStatus: http.StatusLengthRequired},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newUploadFixture(t)
			req := httptest.NewRequest(http.MethodPost, "https://upload-test.apps.test/uploads/avatar", strings.NewReader("avatar"))
			req.Header.Set("Authorization", "Bearer "+f.token)
			req.Header.Set("Content-Type", "image/png")
			tc.configure(&f, req)
			res := httptest.NewRecorder()
			f.handler.ServeHTTP(res, req)
			if res.Code != tc.wantStatus {
				t.Fatalf("status = %d, body = %s; want %d", res.Code, res.Body.String(), tc.wantStatus)
			}
			if len(f.provider.body) != 0 {
				t.Fatalf("provider received %d bytes after rejection", len(f.provider.body))
			}
		})
	}
}

func TestUploadHandlerPassesThroughMissesAndDisabledRoutes(t *testing.T) {
	f := newUploadFixture(t)
	if res := f.request(http.MethodGet, "/not-an-upload", "", ""); res.Code != http.StatusTeapot || res.Header().Get("X-Next") != "called" {
		t.Fatalf("non-upload path was not passed through: status=%d headers=%v", res.Code, res.Header())
	}
	if res := f.request(http.MethodPost, "/uploads/missing", "avatar", "image/png"); res.Code != http.StatusTeapot || res.Header().Get("X-Next") != "called" {
		t.Fatalf("unknown route was not passed through: status=%d headers=%v", res.Code, res.Header())
	}
	f.route.Enabled = false
	if _, err := f.store.UpsertObjectUploadRoute(context.Background(), f.route); err != nil {
		t.Fatal(err)
	}
	if res := f.request(http.MethodPost, "/uploads/avatar", "avatar", "image/png"); res.Code != http.StatusNotFound {
		t.Fatalf("disabled route status = %d, want %d", res.Code, http.StatusNotFound)
	}
}

func TestUploadHandlerReturnsProviderFailure(t *testing.T) {
	f := newUploadFixture(t)
	f.provider.err = errors.New("upstream unavailable")
	res := f.request(http.MethodPost, "/uploads/avatar", "avatar", "image/png")
	if res.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, body = %s; want %d", res.Code, res.Body.String(), http.StatusBadGateway)
	}
}
