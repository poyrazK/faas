package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"regexp"
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
	multipartParts       objectstorage.MultipartPartsPage
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
func (p *fakeObjectProvider) ListMultipartParts(_ context.Context, b string, _ objectstorage.MultipartListPartsRequest) (objectstorage.MultipartPartsPage, error) {
	p.accessed = append(p.accessed, b)
	if p.multipartErr != nil {
		return objectstorage.MultipartPartsPage{}, p.multipartErr
	}
	return p.multipartParts, nil
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

func TestObjectS3CredentialLifecycle(t *testing.T) {
	e := setupSecrets(t, api.PlanHobby)
	if err := e.s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	createApp(t, e, "s3-credential-app")
	e.s.WithObjectStorage(objectRegistry(t, &fakeObjectProvider{}, &fakeObjectProvider{}, "external"))
	bucketPath := "/v1/apps/s3-credential-app/buckets"
	bucket := bucketResponse(t, e.do(t, "POST", bucketPath, map[string]any{"name": "assets"}, nil), 201)
	credentialPath := bucketPath + "/" + bucket.ID + "/s3-credentials"

	createdResponse := e.do(t, "POST", credentialPath, api.CreateObjectS3CredentialRequest{Label: "production uploader", Permission: api.ObjectBucketPermissionReadWrite}, nil)
	if createdResponse.Code != 201 || createdResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create credential = %d headers=%v body=%s", createdResponse.Code, createdResponse.Header(), createdResponse.Body.String())
	}
	var created api.ObjectS3CredentialSecret
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^GRGA[A-Z2-7]{16}$`).MatchString(created.AccessKeyID) || len(created.SecretAccessKey) != 40 {
		t.Fatalf("invalid credential shape: access=%q secret_length=%d", created.AccessKeyID, len(created.SecretAccessKey))
	}
	if created.BucketID != bucket.ID || created.Status != state.ObjectS3CredentialStatusActive || created.Endpoint != "https://s3.gregale.dev" || created.Region != "us-east-1" || created.AddressingStyle != "path" {
		t.Fatalf("unexpected credential response: %+v", created)
	}

	listResponse := e.do(t, "GET", credentialPath, nil, nil)
	var list api.ObjectS3CredentialList
	if listResponse.Code != 200 || json.Unmarshal(listResponse.Body.Bytes(), &list) != nil || len(list.Items) != 1 || list.Items[0].ID != created.ID {
		t.Fatalf("list credential = %d %s", listResponse.Code, listResponse.Body.String())
	}
	if strings.Contains(listResponse.Body.String(), created.SecretAccessKey) || strings.Contains(listResponse.Body.String(), "secret_access_key") {
		t.Fatal("one-time S3 secret leaked through list endpoint")
	}

	if response := e.do(t, "DELETE", credentialPath+"/"+created.ID, nil, nil); response.Code != 204 {
		t.Fatalf("revoke credential = %d %s", response.Code, response.Body.String())
	}
	if _, _, err := e.store.ResolveObjectS3Credential(context.Background(), created.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("revoked credential still resolves: %v", err)
	}
	listResponse = e.do(t, "GET", credentialPath, nil, nil)
	if listResponse.Code != 200 || strings.Contains(listResponse.Body.String(), created.AccessKeyID) {
		t.Fatalf("revoked credential still listed = %d %s", listResponse.Code, listResponse.Body.String())
	}
}

func TestObjectStorageComputeBindingLifecycle(t *testing.T) {
	_, teardown := withTestIdentities(t)
	defer teardown()
	e := setupSecrets(t, api.PlanHobby)
	if err := e.s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	app := createApp(t, e, "compute-binding-app")
	e.s.WithObjectStorage(objectRegistry(t, &fakeObjectProvider{}, &fakeObjectProvider{}, "external"))
	bucketPath := "/v1/apps/compute-binding-app/buckets"
	bucket := bucketResponse(t, e.do(t, "POST", bucketPath, map[string]any{"name": "assets"}, nil), 201)
	bindingPath := bucketPath + "/" + bucket.ID + "/compute-bindings"

	createdResponse := e.do(t, "POST", bindingPath, api.CreateObjectStorageComputeBindingRequest{Permission: api.ObjectBucketPermissionReadWrite}, nil)
	if createdResponse.Code != 201 || createdResponse.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("create compute binding = %d headers=%v body=%s", createdResponse.Code, createdResponse.Header(), createdResponse.Body.String())
	}
	var created api.ObjectStorageComputeBinding
	if err := json.Unmarshal(createdResponse.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Prefix != "GREGALE_S3_ASSETS" || created.Scope != state.DefaultEnvScope || created.Credential.Permission != api.ObjectBucketPermissionReadWrite {
		t.Fatalf("unexpected binding: %+v", created)
	}
	rows, err := e.store.ListAppSecretsInScope(context.Background(), e.acct.ID, app.ID, state.DefaultEnvScope)
	if err != nil || len(rows) != 6 {
		t.Fatalf("managed secret rows = %d, err=%v", len(rows), err)
	}
	for _, row := range rows {
		if row.ManagedObjectStorageCredentialID != created.ID {
			t.Fatalf("secret %s owner = %q, want %q", row.Key, row.ManagedObjectStorageCredentialID, created.ID)
		}
	}
	if response := e.do(t, "PUT", "/v1/apps/compute-binding-app/secrets/GREGALE_S3_ASSETS_BUCKET", api.PutAppSecretRequest{Value: "tamper"}, nil); response.Code != 409 {
		t.Fatalf("managed secret overwrite = %d %s", response.Code, response.Body.String())
	}

	rotatedResponse := e.do(t, "POST", bindingPath+"/"+created.ID+"/rotate", struct{}{}, nil)
	if rotatedResponse.Code != 200 {
		t.Fatalf("rotate compute binding = %d %s", rotatedResponse.Code, rotatedResponse.Body.String())
	}
	var rotated api.ObjectStorageComputeBinding
	if err := json.Unmarshal(rotatedResponse.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if rotated.ID != created.ID || rotated.Credential.AccessKeyID == created.Credential.AccessKeyID {
		t.Fatalf("rotation did not replace access key: before=%q after=%q", created.Credential.AccessKeyID, rotated.Credential.AccessKeyID)
	}
	if _, _, err := e.store.ResolveObjectS3Credential(context.Background(), created.Credential.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("old access key still resolves: %v", err)
	}

	if response := e.do(t, "DELETE", bindingPath+"/"+created.ID, nil, nil); response.Code != 204 {
		t.Fatalf("delete compute binding = %d %s", response.Code, response.Body.String())
	}
	rows, err = e.store.ListAppSecretsInScope(context.Background(), e.acct.ID, app.ID, state.DefaultEnvScope)
	if err != nil || len(rows) != 0 {
		t.Fatalf("managed secrets after delete = %d, err=%v", len(rows), err)
	}
	if _, _, err := e.store.ResolveObjectS3Credential(context.Background(), rotated.Credential.AccessKeyID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("rotated access key still resolves after delete: %v", err)
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
