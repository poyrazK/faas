package main

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type fakeObjectProvider struct {
	created              []string
	accessed             []string
	createErr, deleteErr error
	multipartErr         error
	multipartCompleted   []string
	multipartAborted     []string
}

func (p *fakeObjectProvider) CreateBucket(_ context.Context, b string) error {
	p.created = append(p.created, b)
	return p.createErr
}
func (p *fakeObjectProvider) DeleteBucket(_ context.Context, b string) error {
	p.accessed = append(p.accessed, b)
	return p.deleteErr
}
func (p *fakeObjectProvider) ListObjects(_ context.Context, b, prefix, cursor string, limit int32) (objectstorage.ObjectPage, error) {
	p.accessed = append(p.accessed, b)
	return objectstorage.ObjectPage{Items: []objectstorage.Object{{Key: prefix + "file", Size: 3}}, NextCursor: "next"}, nil
}
func (p *fakeObjectProvider) DeleteObject(_ context.Context, b, key string) error {
	p.accessed = append(p.accessed, b)
	return nil
}
func (p *fakeObjectProvider) Presign(_ context.Context, b string, r objectstorage.SignRequest) (objectstorage.SignedRequest, error) {
	p.accessed = append(p.accessed, b)
	return objectstorage.SignedRequest{URL: "https://storage.example.test/signed", Method: r.Method, Headers: map[string]string{}, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (p *fakeObjectProvider) EnsureMultipartUpload(_ context.Context, b string, r objectstorage.MultipartCreateRequest) (string, error) {
	p.accessed = append(p.accessed, b)
	if p.multipartErr != nil {
		return "", p.multipartErr
	}
	return "provider-" + r.SessionID, nil
}
func (p *fakeObjectProvider) PresignMultipartPart(_ context.Context, b string, r objectstorage.MultipartPartRequest) (objectstorage.SignedRequest, error) {
	p.accessed = append(p.accessed, b)
	if p.multipartErr != nil {
		return objectstorage.SignedRequest{}, p.multipartErr
	}
	return objectstorage.SignedRequest{URL: "https://storage.example.test/part", Method: "PUT", Headers: map[string]string{"Content-Length": strconv.FormatInt(r.SizeBytes, 10)}, ExpiresAt: time.Now().Add(time.Minute)}, nil
}
func (p *fakeObjectProvider) CompleteMultipartUpload(_ context.Context, b string, r objectstorage.MultipartCompleteRequest) error {
	p.accessed = append(p.accessed, b)
	if p.multipartErr == nil {
		p.multipartCompleted = append(p.multipartCompleted, r.SessionID)
	}
	return p.multipartErr
}
func (p *fakeObjectProvider) AbortMultipartUpload(_ context.Context, b string, r objectstorage.MultipartAbortRequest) error {
	p.accessed = append(p.accessed, b)
	if p.multipartErr == nil {
		p.multipartAborted = append(p.multipartAborted, r.ProviderUploadID)
	}
	return p.multipartErr
}

func objectRegistry(t *testing.T, a, b *fakeObjectProvider, defaultID string) *objectstorage.Registry {
	t.Helper()
	backends := []objectstorage.BackendConfig{}
	for _, id := range []string{"external", "ceph"} {
		backends = append(backends, objectstorage.BackendConfig{ID: id, Driver: id, Region: "us-east-1", Namespace: id, Endpoint: "https://" + id + ".example.test", S3Region: "us-east-1"})
	}
	r, err := objectstorage.NewRegistry(objectstorage.Config{DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": defaultID}, MaxUploadBytes: 100, Backends: backends}, func(string) string { return "" }, map[string]objectstorage.Factory{"external": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) { return a, nil }, "ceph": func(objectstorage.BackendConfig, func(string) string) (objectstorage.Provider, error) { return b, nil }})
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func bucketResponse(t *testing.T, r *httptest.ResponseRecorder, status int) bucketView {
	t.Helper()
	if r.Code != status {
		t.Fatalf("got %d want %d: %s", r.Code, status, r.Body.String())
	}
	var b bucketView
	if err := json.Unmarshal(r.Body.Bytes(), &b); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestObjectStorageLifecycleAndProviderSwitch(t *testing.T) {
	e := setup(t, api.PlanHobby)
	if err := e.s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	createApp(t, e, "bucket-app")
	a, b := &fakeObjectProvider{}, &fakeObjectProvider{}
	e.s.WithObjectStorage(objectRegistry(t, a, b, "external"))
	path := "/v1/apps/bucket-app/buckets"
	first := bucketResponse(t, e.do(t, "POST", path, map[string]any{"name": "assets"}, nil), 201)
	if first.State != "ready" || first.Scope != "default" || len(a.created) != 1 {
		t.Fatal(first, a.created)
	}
	e.s.WithObjectStorage(objectRegistry(t, a, b, "ceph"))
	retry := bucketResponse(t, e.do(t, "POST", path, map[string]any{"name": "assets"}, nil), 200)
	if retry.ID != first.ID || len(a.created) != 1 || len(b.created) != 0 {
		t.Fatal("retry changed placement")
	}
	bucketResponse(t, e.do(t, "POST", path, map[string]any{"name": "new-assets"}, nil), 201)
	if len(b.created) != 1 {
		t.Fatal("new default ignored")
	}
	r := e.do(t, "GET", path+"/"+first.ID+"/objects?prefix=folder%2F", nil, nil)
	if r.Code != 200 || len(a.accessed) != 1 || len(b.accessed) != 0 {
		t.Fatal("wrong provider", r.Code, r.Body.String())
	}
	if strings.Contains(e.do(t, "GET", path, nil, nil).Body.String(), "backend") {
		t.Fatal("operator placement leaked")
	}
	if r = e.do(t, "DELETE", "/v1/apps/bucket-app", nil, nil); r.Code != 409 {
		t.Fatal("app orphaned buckets", r.Code)
	}
	a.deleteErr = objectstorage.ErrNotEmpty
	if r = e.do(t, "DELETE", path+"/"+first.ID, nil, nil); r.Code != 409 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = e.do(t, "GET", path+"/"+first.ID+"/objects", nil, nil); r.Code != 200 {
		t.Fatal("nonempty bucket not restored", r.Code)
	}
	a.deleteErr = nil
	if r = e.do(t, "DELETE", path+"/"+first.ID, nil, nil); r.Code != 204 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r = e.do(t, "GET", path+"/"+first.ID+"/objects", nil, nil); r.Code != 404 {
		t.Fatal(r.Code)
	}
}

func TestObjectStorageFailuresAndAuthorization(t *testing.T) {
	e := setup(t, api.PlanHobby)
	if err := e.s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	createApp(t, e, "bucket-auth")
	createApp(t, e, "different-app")
	path := "/v1/apps/bucket-auth/buckets"
	if r := e.do(t, "POST", path, map[string]any{"name": "assets"}, nil); r.Code != 503 {
		t.Fatal(r.Code)
	}
	a, b := &fakeObjectProvider{createErr: objectstorage.ErrUnavailable}, &fakeObjectProvider{}
	e.s.WithObjectStorage(objectRegistry(t, a, b, "external"))
	if r := e.do(t, "POST", path, map[string]any{"name": "assets"}, nil); r.Code != 503 {
		t.Fatal(r.Code)
	}
	a.createErr = nil
	if r := e.do(t, "POST", path, map[string]any{"name": "assets"}, nil); r.Code != 409 {
		t.Fatal("retry bypassed cooldown", r.Code)
	}
	var pending api.ObjectBucketList
	if err := json.Unmarshal(e.do(t, "GET", path, nil, nil).Body.Bytes(), &pending); err != nil || len(pending.Items) != 1 {
		t.Fatal(err, pending)
	}
	if r := e.do(t, "DELETE", path+"/"+pending.Items[0].ID, nil, nil); r.Code != 204 {
		t.Fatal("failed provisioning cleanup", r.Code)
	}
	first := bucketResponse(t, e.do(t, "POST", path, map[string]any{"name": "assets"}, nil), 201)
	if len(a.created) != 2 || len(a.accessed) != 1 || a.accessed[0] != a.created[0] {
		t.Fatal("failed bucket not cleaned up")
	}
	qualifyObjectAccounting(t, e, first.ID)
	sign := path + "/" + first.ID + "/signed-url"
	if r := e.do(t, "POST", sign, map[string]any{"method": "PUT", "key": "file", "size_bytes": 101}, nil); r.Code != 400 {
		t.Fatal(r.Code)
	}
	if r := e.do(t, "POST", sign, map[string]any{"method": "PUT", "key": "file", "size_bytes": 10}, nil); r.Code != 200 || r.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := e.do(t, "GET", "/v1/apps/different-app/buckets/"+first.ID+"/objects", nil, nil); r.Code != 404 {
		t.Fatal("cross app access", r.Code)
	}
	other, err := e.store.CreateAccount(context.Background(), "other-bucket@example.test", api.PlanHobby)
	if err != nil {
		t.Fatal(err)
	}
	pt, hash, _ := api.GenerateAPIKey()
	_, err = e.store.CreateAPIKey(context.Background(), other.ID, hash, "other", api.ScopesAdminOnly)
	if err != nil {
		t.Fatal(err)
	}
	if r := e.do(t, "GET", path, nil, map[string]string{"Authorization": "Bearer " + pt}); r.Code != 404 {
		t.Fatal("cross account access", r.Code)
	}
	pt, hash, _ = api.GenerateAPIKey()
	readKey, err := e.store.CreateAPIKey(context.Background(), e.acct.ID, hash, "read-only", []string{api.ScopeStorageRead})
	if err != nil {
		t.Fatal(err)
	}
	headers := map[string]string{"Authorization": "Bearer " + pt}
	managerPlaintext, managerHash, _ := api.GenerateAPIKey()
	if _, err = e.store.CreateAPIKey(context.Background(), e.acct.ID, managerHash, "storage-manager", []string{api.ScopeStorageManage}); err != nil {
		t.Fatal(err)
	}
	managerHeaders := map[string]string{"Authorization": "Bearer " + managerPlaintext}
	if r := e.do(t, "POST", sign, map[string]any{"method": "GET", "key": "file"}, headers); r.Code != 403 {
		t.Fatal("ungranted key read bucket", r.Code, r.Body.String())
	}
	grantPath := path + "/" + first.ID + "/access-grants/" + readKey.ID
	if r := e.do(t, "PUT", grantPath, api.SetObjectBucketAccessGrantRequest{Permission: api.ObjectBucketPermissionRead}, managerHeaders); r.Code != 200 {
		t.Fatal("grant read key", r.Code, r.Body.String())
	}
	if r := e.do(t, "GET", path+"/"+first.ID+"/access-grants", nil, headers); r.Code != 403 {
		t.Fatal("data key managed grants", r.Code)
	}
	var grantList api.ObjectBucketAccessGrantList
	grantResponse := e.do(t, "GET", path+"/"+first.ID+"/access-grants", nil, managerHeaders)
	if grantResponse.Code != 200 || json.Unmarshal(grantResponse.Body.Bytes(), &grantList) != nil || len(grantList.Items) != 1 {
		t.Fatal("manager grant list", grantResponse.Code, grantResponse.Body.String())
	}
	var visible api.ObjectBucketList
	visibleResponse := e.do(t, "GET", path, nil, headers)
	if visibleResponse.Code != 200 || json.Unmarshal(visibleResponse.Body.Bytes(), &visible) != nil || len(visible.Items) != 1 || visible.Items[0].ID != first.ID {
		t.Fatal("grant-filtered bucket list", visibleResponse.Code, visibleResponse.Body.String())
	}
	if r := e.do(t, "POST", sign, map[string]any{"method": "GET", "key": "file"}, headers); r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	if r := e.do(t, "POST", sign, map[string]any{"method": "PUT", "key": "file", "size_bytes": 10}, headers); r.Code != 403 {
		t.Fatal("read key uploaded", r.Code)
	}
	if r := e.do(t, "DELETE", path+"/"+first.ID, nil, headers); r.Code != 403 {
		t.Fatal("read key deleted", r.Code)
	}
	if r := e.do(t, "DELETE", grantPath, nil, managerHeaders); r.Code != 204 {
		t.Fatal("manager revoked grant", r.Code, r.Body.String())
	}
	if r := e.do(t, "POST", sign, map[string]any{"method": "GET", "key": "file"}, headers); r.Code != 403 {
		t.Fatal("revoked grant still authorized", r.Code, r.Body.String())
	}
}

func qualifyObjectAccounting(t *testing.T, e testEnv, bucket string) {
	t.Helper()
	p := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 500, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 1000, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	e.s.objectStorage.Accounting = p
	st := any(e.store).(state.ObjectStorageAccountingStore)
	ctx := context.Background()
	if err := st.ClaimObjectInventory(ctx, bucket, "fixture"); err != nil {
		t.Fatal(err)
	}
	if err := st.FinishObjectInventory(ctx, bucket, "fixture", 0, 0); err != nil {
		t.Fatal(err)
	}
	snapshot, err := st.ObjectUsage(ctx, e.acct.ID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, b := range snapshot.Buckets {
		r := api.ObjectStorageUsageReport{AccountID: e.acct.ID, BackendID: b.Bucket.BackendID, BackendFingerprint: b.Bucket.BackendFingerprint, Source: "fixture", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().Add(-time.Second)}
		if err := st.RecordObjectUsageReport(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
}
