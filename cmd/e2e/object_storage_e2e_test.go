// End-to-end coverage for Gregale's provider-backed object-storage path.
//
// The tests boot a real apid against Postgres and point the S3 driver at a
// small in-process S3-compatible server. This keeps the test hermetic while
// exercising the production registry, AWS SDK adapter, durable bucket and
// multipart state, tenant authorization, and sealed-secret lifecycle.
package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const objectStorageE2EBackendID = "e2e-s3"

type objectStorageE2EEnv struct {
	pool    *pgxpool.Pool
	h       *e2etest.Harness
	stub    *objectStorageS3Stub
	backend objectstorage.Backend
}

func startObjectStorageE2E(t *testing.T, pool *pgxpool.Pool) objectStorageE2EEnv {
	t.Helper()
	stub := newObjectStorageS3Stub(t)
	config := objectstorage.Config{
		Accounting: &api.ObjectStoragePolicy{
			MaxAccountBytes: 1 << 30, MaxBucketBytes: 1 << 30, MaxAccountKeys: 1000,
			MaxMonthlyCostMillicents: 1 << 30, MaxMonthlyRequests: 1000,
			MaxMonthlyEgressBytes: 1 << 30, MaxMonthlyAuthorizations: 1000,
			MaxReportAgeSeconds: 3600,
		},
		DefaultRegion:    "us-east-1",
		Defaults:         map[string]string{"us-east-1": objectStorageE2EBackendID},
		MaxBucketsPerApp: 10,
		MaxUploadBytes:   api.MaxObjectUploadBytes,
		PublicEndpoint:   "https://s3.gregale.dev",
		PublicRegion:     "us-east-1",
		Backends: []objectstorage.BackendConfig{{
			ID: objectStorageE2EBackendID, Driver: "s3", Region: "us-east-1", Namespace: "e2e-s3",
			Endpoint: stub.server.URL, S3Region: "us-east-1", PathStyle: true,
			AccessKeyEnv: "FAAS_E2E_S3_ACCESS_KEY", SecretKeyEnv: "FAAS_E2E_S3_SECRET_KEY", AllowHTTP: true,
		}},
	}
	getenv := func(name string) string {
		switch name {
		case "FAAS_E2E_S3_ACCESS_KEY":
			return "e2e-access"
		case "FAAS_E2E_S3_SECRET_KEY":
			return "e2e-secret"
		default:
			return ""
		}
	}
	registry, err := objectstorage.NewRegistry(config, getenv, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatalf("object storage registry: %v", err)
	}
	backend, err := registry.Default(config.DefaultRegion)
	if err != nil {
		t.Fatalf("object storage backend: %v", err)
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("object storage config: %v", err)
	}
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "object-storage.json")
	if err := os.WriteFile(configPath, encoded, 0o600); err != nil {
		t.Fatalf("write object storage config: %v", err)
	}
	recipientPath := filepath.Join(tmpDir, "host.age.pub")
	if err := writeTestRecipient(recipientPath); err != nil {
		t.Fatalf("write host age recipient: %v", err)
	}
	store := state.NewPgStore(pool)
	runtimeRow, err := store.UpsertRuntimeConfig(context.Background(), state.RuntimeConfigUpdate{
		Key: "s3_enabled", Scope: state.RuntimeConfigScopeGlobal, DesiredValue: json.RawMessage("true"),
		ApplyMode: state.RuntimeConfigApplyHot, Reason: "object storage e2e fixture",
	})
	if err != nil {
		t.Fatalf("seed object storage runtime flag: %v", err)
	}
	if err := store.MarkRuntimeConfigApplied(context.Background(), runtimeRow.Key, runtimeRow.Scope, runtimeRow.ScopeID, runtimeRow.Version, json.RawMessage("true"), ""); err != nil {
		t.Fatalf("apply object storage runtime flag: %v", err)
	}
	h := e2etest.StartWithEnv(t, pool, e2etest.APID, []string{
		"FAAS_OBJECT_STORAGE_CONFIG=" + configPath,
		"FAAS_E2E_S3_ACCESS_KEY=e2e-access",
		"FAAS_E2E_S3_SECRET_KEY=e2e-secret",
		"FAAS_HOST_AGE_RECIPIENT_PATH=" + recipientPath,
		"FAAS_HOST_AGE_IDENTITY_PATH=" + recipientPath + ".priv",
	})
	return objectStorageE2EEnv{pool: pool, h: h, stub: stub, backend: backend}
}

func createObjectStorageApp(t *testing.T, env objectStorageE2EEnv, key, slug string) {
	t.Helper()
	if status := statusOnly(t, env.h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: slug}); status != http.StatusCreated {
		t.Fatalf("create app %q: %d", slug, status)
	}
}

func createObjectStorageBucket(t *testing.T, env objectStorageE2EEnv, key, slug, name string) api.ObjectBucket {
	t.Helper()
	raw, status := doReq(t, env.h, key, http.MethodPost, "/v1/apps/"+slug+"/buckets", api.CreateObjectBucketRequest{Name: name})
	if status != http.StatusCreated {
		t.Fatalf("create bucket: status=%d body=%s", status, raw)
	}
	var bucket api.ObjectBucket
	if err := json.Unmarshal(raw, &bucket); err != nil {
		t.Fatalf("decode bucket: %v", err)
	}
	return bucket
}

func objectStorageAccountAndApp(t *testing.T, pool *pgxpool.Pool, plan api.Plan, label, slug string) (state.Account, state.App) {
	t.Helper()
	ctx := context.Background()
	store := state.NewPgStore(pool)
	account, err := store.AccountByEmail(ctx, seedEmail(plan, label))
	if err != nil {
		t.Fatalf("account: %v", err)
	}
	app, err := store.AppBySlug(ctx, slug)
	if err != nil {
		t.Fatalf("app: %v", err)
	}
	return account, app
}

type objectStorageScopedKey struct {
	state.APIKey
	Secret string
}

func createObjectStorageScopedKey(t *testing.T, pool *pgxpool.Pool, accountID, label string, scopes []string) objectStorageScopedKey {
	t.Helper()
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatalf("generate scoped key: %v", err)
	}
	key, err := state.NewPgStore(pool).CreateAPIKey(context.Background(), accountID, hash, label, scopes)
	if err != nil {
		t.Fatalf("create scoped key: %v", err)
	}
	return objectStorageScopedKey{APIKey: key, Secret: plaintext}
}

func seedObjectStorageAccounting(t *testing.T, env objectStorageE2EEnv, accountID, appID, bucketID string) {
	t.Helper()
	ctx := context.Background()
	store := state.NewPgStore(env.pool)
	bucketStore := any(store).(state.ObjectBucketStore)
	bucket, err := bucketStore.GetObjectBucket(ctx, accountID, appID, bucketID)
	if err != nil {
		t.Fatalf("load accounting bucket: %v", err)
	}
	accounting, ok := any(store).(state.ObjectStorageAccountingStore)
	if !ok {
		t.Fatal("PgStore does not implement object accounting")
	}
	token := "e2e-inventory-" + bucket.ID
	if err := accounting.ClaimObjectInventory(ctx, bucket.ID, token); err != nil {
		t.Fatalf("claim object inventory: %v", err)
	}
	if err := accounting.FinishObjectInventory(ctx, bucket.ID, token, 0, 0); err != nil {
		t.Fatalf("finish object inventory: %v", err)
	}
	if err := accounting.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{
		AccountID: accountID, BackendID: env.backend.ID, BackendFingerprint: env.backend.Fingerprint,
		Source: "e2e", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().UTC().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("record object usage report: %v", err)
	}
}

func TestE2E_ObjectStorage_ProviderBackedIsolationAndIdempotency(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	env := startObjectStorageE2E(t, pool)
	keyA := env.h.SeedAccount(context.Background(), api.PlanPro, "object-storage-a")
	keyB := env.h.SeedAccount(context.Background(), api.PlanPro, "object-storage-b")
	createObjectStorageApp(t, env, keyA, "object-storage-a")

	bucket := createObjectStorageBucket(t, env, keyA, "object-storage-a", "assets")
	if bucket.Scope != api.DefaultEnvScope || bucket.Region != "us-east-1" || bucket.State != "ready" {
		t.Fatalf("bucket=%+v", bucket)
	}
	accountA, appA := objectStorageAccountAndApp(t, pool, api.PlanPro, "object-storage-a", "object-storage-a")
	stored, err := state.NewPgStore(pool).GetObjectBucket(context.Background(), accountA.ID, appA.ID, bucket.ID)
	if err != nil {
		t.Fatalf("stored bucket: %v", err)
	}
	if stored.PhysicalName == "" || strings.Contains(string(mustJSON(t, bucket)), stored.PhysicalName) {
		t.Fatalf("physical bucket leaked: response=%+v physical=%q", bucket, stored.PhysicalName)
	}
	if got := env.stub.createCount(stored.PhysicalName); got != 1 {
		t.Fatalf("provider create calls=%d, want 1", got)
	}

	raw, status := doReq(t, env.h, keyA, http.MethodPost, "/v1/apps/object-storage-a/buckets", api.CreateObjectBucketRequest{Name: "assets"})
	if status != http.StatusOK {
		t.Fatalf("idempotent bucket retry: %d %s", status, raw)
	}
	var retry api.ObjectBucket
	if err := json.Unmarshal(raw, &retry); err != nil || retry.ID != bucket.ID {
		t.Fatalf("retry bucket=%+v err=%v", retry, err)
	}
	if got := env.stub.createCount(stored.PhysicalName); got != 1 {
		t.Fatalf("provider create calls after retry=%d, want 1", got)
	}

	env.stub.putObject(stored.PhysicalName, "folder/readme.txt", []byte("e2e-data"))
	raw, status = doReq(t, env.h, keyA, http.MethodGet, "/v1/apps/object-storage-a/buckets/"+bucket.ID+"/objects", nil)
	if status != http.StatusOK {
		t.Fatalf("list objects: %d %s", status, raw)
	}
	var page api.BucketObjectPage
	if err := json.Unmarshal(raw, &page); err != nil || len(page.Items) != 1 || page.Items[0].Key != "folder/readme.txt" || page.Items[0].SizeBytes != 8 {
		t.Fatalf("object page=%+v err=%v", page, err)
	}

	if status := statusOnly(t, env.h, keyB, http.MethodGet, "/v1/apps/object-storage-a/buckets", nil); status != http.StatusNotFound {
		t.Fatalf("cross-account app list status=%d, want 404", status)
	}
	if status := statusOnly(t, env.h, keyB, http.MethodGet, "/v1/apps/object-storage-a/buckets/"+bucket.ID+"/objects", nil); status != http.StatusNotFound {
		t.Fatalf("cross-account object list status=%d, want 404", status)
	}

	reader := createObjectStorageScopedKey(t, pool, accountA.ID, "object-reader", []string{api.ScopeStorageRead})
	if status := statusOnly(t, env.h, reader.Secret, http.MethodGet, "/v1/apps/object-storage-a/buckets/"+bucket.ID+"/objects", nil); status != http.StatusForbidden {
		t.Fatalf("ungranted reader status=%d, want 403", status)
	}
	grantPath := "/v1/apps/object-storage-a/buckets/" + bucket.ID + "/access-grants/" + reader.ID
	if status := statusOnly(t, env.h, keyA, http.MethodPut, grantPath, api.SetObjectBucketAccessGrantRequest{Permission: api.ObjectBucketPermissionRead}); status != http.StatusOK {
		t.Fatalf("grant reader status=%d", status)
	}
	if status := statusOnly(t, env.h, reader.Secret, http.MethodGet, "/v1/apps/object-storage-a/buckets/"+bucket.ID+"/objects", nil); status != http.StatusOK {
		t.Fatalf("granted reader status=%d, want 200", status)
	}
	if status := statusOnly(t, env.h, reader.Secret, http.MethodDelete, "/v1/apps/object-storage-a/buckets/"+bucket.ID+"/objects?key="+url.QueryEscape("folder/readme.txt"), nil); status != http.StatusForbidden {
		t.Fatalf("read-only delete status=%d, want 403", status)
	}
	if status := statusOnly(t, env.h, keyA, http.MethodDelete, "/v1/apps/object-storage-a/buckets/"+bucket.ID+"/objects?key="+url.QueryEscape("folder/readme.txt"), nil); status != http.StatusNoContent {
		t.Fatalf("admin object delete status=%d", status)
	}

	env.stub.putObject(stored.PhysicalName, "keep.txt", []byte("keep"))
	if status := statusOnly(t, env.h, keyA, http.MethodDelete, "/v1/apps/object-storage-a/buckets/"+bucket.ID, nil); status != http.StatusConflict {
		t.Fatalf("non-empty bucket delete status=%d, want 409", status)
	}
	if status := statusOnly(t, env.h, keyA, http.MethodDelete, "/v1/apps/object-storage-a/buckets/"+bucket.ID+"/objects?key=keep.txt", nil); status != http.StatusNoContent {
		t.Fatalf("cleanup object status=%d", status)
	}
	if status := statusOnly(t, env.h, keyA, http.MethodDelete, "/v1/apps/object-storage-a/buckets/"+bucket.ID, nil); status != http.StatusNoContent {
		t.Fatalf("empty bucket delete status=%d", status)
	}
}

func TestE2E_ObjectStorage_CredentialAndComputeBindingLifecycle(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	env := startObjectStorageE2E(t, pool)
	key := env.h.SeedAccount(context.Background(), api.PlanPro, "object-storage-bindings")
	slug := "object-storage-bindings"
	createObjectStorageApp(t, env, key, slug)
	bucket := createObjectStorageBucket(t, env, key, slug, "assets")
	base := "/v1/apps/" + slug + "/buckets/" + bucket.ID

	raw, status := doReq(t, env.h, key, http.MethodPost, base+"/s3-credentials", api.CreateObjectS3CredentialRequest{Label: "uploader", Permission: api.ObjectBucketPermissionReadWrite})
	if status != http.StatusCreated {
		t.Fatalf("create S3 credential: %d %s", status, raw)
	}
	var secret api.ObjectS3CredentialSecret
	if err := json.Unmarshal(raw, &secret); err != nil {
		t.Fatalf("decode S3 credential: %v", err)
	}
	if secret.ID == "" || secret.AccessKeyID == "" || secret.SecretAccessKey == "" || secret.Endpoint != "https://s3.gregale.dev" || secret.Region != "us-east-1" || secret.AddressingStyle != "path" {
		t.Fatalf("credential secret=%+v", secret)
	}
	raw, status = doReq(t, env.h, key, http.MethodGet, base+"/s3-credentials", nil)
	if status != http.StatusOK || strings.Contains(string(raw), secret.SecretAccessKey) || !strings.Contains(string(raw), secret.AccessKeyID) {
		t.Fatalf("credential list status=%d body=%s", status, raw)
	}
	if status := statusOnly(t, env.h, key, http.MethodDelete, base+"/s3-credentials/"+secret.ID, nil); status != http.StatusNoContent {
		t.Fatalf("revoke S3 credential status=%d", status)
	}

	raw, status = doReq(t, env.h, key, http.MethodPost, base+"/compute-bindings", api.CreateObjectStorageComputeBindingRequest{Label: "compute", Permission: api.ObjectBucketPermissionReadWrite, Prefix: "GREGALE_S3_ASSETS"})
	if status != http.StatusCreated {
		t.Fatalf("create compute binding: %d %s", status, raw)
	}
	var binding api.ObjectStorageComputeBinding
	if err := json.Unmarshal(raw, &binding); err != nil {
		t.Fatalf("decode compute binding: %v", err)
	}
	if binding.ID == "" || binding.Credential.AccessKeyID == "" || binding.SecretKeys.Endpoint == "" || binding.SecretKeys.SecretAccessKey == "" {
		t.Fatalf("binding=%+v", binding)
	}
	account, app := objectStorageAccountAndApp(t, pool, api.PlanPro, "object-storage-bindings", slug)
	store := state.NewPgStore(pool)
	rows, err := store.ListAppSecretsInScope(context.Background(), account.ID, app.ID, api.DefaultEnvScope)
	if err != nil {
		t.Fatalf("list managed binding secrets: %v", err)
	}
	if len(rows) != 6 {
		t.Fatalf("managed binding secret count=%d, want 6", len(rows))
	}
	hashes := make(map[string]string, len(rows))
	for _, row := range rows {
		if row.ManagedObjectStorageCredentialID != binding.ID || len(row.Ciphertext) == 0 || row.ValueHash == "" {
			t.Fatalf("managed secret=%+v", row)
		}
		hashes[row.Key] = row.ValueHash
	}
	raw, status = doReq(t, env.h, key, http.MethodGet, "/v1/apps/"+slug+"/secrets", nil)
	if status != http.StatusOK || strings.Contains(string(raw), binding.Credential.AccessKeyID) {
		t.Fatalf("app secret response leaked binding value: %d %s", status, raw)
	}

	raw, status = doReq(t, env.h, key, http.MethodPost, base+"/compute-bindings/"+binding.ID+"/rotate", nil)
	if status != http.StatusOK {
		t.Fatalf("rotate compute binding: %d %s", status, raw)
	}
	var rotated api.ObjectStorageComputeBinding
	if err := json.Unmarshal(raw, &rotated); err != nil || rotated.ID != binding.ID || rotated.Credential.AccessKeyID == binding.Credential.AccessKeyID {
		t.Fatalf("rotated binding=%+v err=%v", rotated, err)
	}
	rows, err = store.ListAppSecretsInScope(context.Background(), account.ID, app.ID, api.DefaultEnvScope)
	if err != nil {
		t.Fatalf("list rotated secrets: %v", err)
	}
	changed := 0
	for _, row := range rows {
		if row.Key == binding.SecretKeys.AccessKeyID || row.Key == binding.SecretKeys.SecretAccessKey {
			if row.ValueHash == hashes[row.Key] {
				t.Fatalf("rotated secret %q retained the old value hash", row.Key)
			}
			changed++
		}
	}
	if changed != 2 {
		t.Fatalf("rotated managed secret keys changed=%d, want 2", changed)
	}
	if status := statusOnly(t, env.h, key, http.MethodDelete, base+"/compute-bindings/"+binding.ID, nil); status != http.StatusNoContent {
		t.Fatalf("delete compute binding status=%d", status)
	}
	rows, err = store.ListAppSecretsInScope(context.Background(), account.ID, app.ID, api.DefaultEnvScope)
	if err != nil || len(rows) != 0 {
		t.Fatalf("managed secrets after delete=%d err=%v", len(rows), err)
	}
	raw, status = doReq(t, env.h, key, http.MethodGet, base+"/compute-bindings", nil)
	if status != http.StatusOK {
		t.Fatalf("list bindings after delete: %d %s", status, raw)
	}
	var bindings api.ObjectStorageComputeBindingList
	if err := json.Unmarshal(raw, &bindings); err != nil || len(bindings.Items) != 0 {
		t.Fatalf("bindings after delete=%+v err=%v", bindings.Items, err)
	}
}

func TestE2E_ObjectStorage_MultipartLifecycleAndDurableRetry(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		return
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	env := startObjectStorageE2E(t, pool)
	key := env.h.SeedAccount(context.Background(), api.PlanPro, "object-storage-multipart")
	slug := "object-storage-multipart"
	createObjectStorageApp(t, env, key, slug)
	bucket := createObjectStorageBucket(t, env, key, slug, "assets")
	account, app := objectStorageAccountAndApp(t, pool, api.PlanPro, "object-storage-multipart", slug)
	seedObjectStorageAccounting(t, env, account.ID, app.ID, bucket.ID)
	base := "/v1/apps/" + slug + "/buckets/" + bucket.ID + "/multipart-uploads"

	upload := createObjectMultipartUpload(t, env.h, key, base, "large.bin")
	raw, status := doReq(t, env.h, key, http.MethodPost, base, api.CreateObjectMultipartUploadRequest{Key: "large.bin", SizeBytes: 10, ContentType: "application/octet-stream"})
	if status != http.StatusOK {
		t.Fatalf("idempotent multipart retry: %d %s", status, raw)
	}
	var retry api.ObjectMultipartUpload
	if err := json.Unmarshal(raw, &retry); err != nil || retry.ID != upload.ID || retry.State != state.ObjectMultipartActive {
		t.Fatalf("multipart retry=%+v err=%v", retry, err)
	}

	part := signAndUploadObjectMultipartPart(t, env.h, key, base, upload.ID, 1, 10)
	raw, status = doReq(t, env.h, key, http.MethodGet, base+"/"+upload.ID+"/parts", nil)
	if status != http.StatusOK {
		t.Fatalf("list multipart parts: %d %s", status, raw)
	}
	var partList api.ObjectMultipartPartList
	if err := json.Unmarshal(raw, &partList); err != nil || len(partList.Items) != 1 || partList.Items[0].ETag != part.ETag || partList.Items[0].SizeBytes != 10 {
		t.Fatalf("multipart parts=%+v err=%v", partList, err)
	}

	completeRequest := api.CompleteObjectMultipartUploadRequest{Parts: []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: part.ETag}}}
	raw, status = doReq(t, env.h, key, http.MethodPost, base+"/"+upload.ID+"/complete", completeRequest)
	if status != http.StatusOK {
		t.Fatalf("complete multipart: %d %s", status, raw)
	}
	var completed api.ObjectMultipartUpload
	if err := json.Unmarshal(raw, &completed); err != nil || completed.State != state.ObjectMultipartCompleted {
		t.Fatalf("completed upload=%+v err=%v", completed, err)
	}
	raw, status = doReq(t, env.h, key, http.MethodPost, base+"/"+upload.ID+"/complete", completeRequest)
	if status != http.StatusOK || !strings.Contains(string(raw), `"state":"completed"`) {
		t.Fatalf("idempotent completion: %d %s", status, raw)
	}

	recovery := createObjectMultipartUpload(t, env.h, key, base, "recovery.bin")
	recoveryPart := signAndUploadObjectMultipartPart(t, env.h, key, base, recovery.ID, 1, 10)
	recoveryComplete := api.CompleteObjectMultipartUploadRequest{Parts: []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: recoveryPart.ETag}}}
	env.stub.setCompleteFailure(true)
	if status := statusOnly(t, env.h, key, http.MethodPost, base+"/"+recovery.ID+"/complete", recoveryComplete); status != http.StatusServiceUnavailable {
		t.Fatalf("provider failure status=%d, want 503", status)
	}
	env.stub.setCompleteFailure(false)
	stored, err := state.NewPgStore(pool).GetObjectMultipartUpload(context.Background(), account.ID, app.ID, bucket.ID, recovery.ID)
	if err != nil || stored.State != state.ObjectMultipartCompleting || stored.LeaseToken != "" || stored.LastErrorCode == "" {
		t.Fatalf("durable failed completion=%+v err=%v", stored, err)
	}
	if _, err := pool.Exec(context.Background(), "UPDATE object_storage_multipart_uploads SET retry_at=now() WHERE id=$1", recovery.ID); err != nil {
		t.Fatalf("make multipart retry due: %v", err)
	}
	raw, status = doReq(t, env.h, key, http.MethodPost, base+"/"+recovery.ID+"/complete", recoveryComplete)
	if status != http.StatusOK {
		t.Fatalf("retry durable completion: %d %s", status, raw)
	}
	var recovered api.ObjectMultipartUpload
	if err := json.Unmarshal(raw, &recovered); err != nil || recovered.State != state.ObjectMultipartCompleted {
		t.Fatalf("recovered upload=%+v err=%v", recovered, err)
	}

	abort := createObjectMultipartUpload(t, env.h, key, base, "abort.bin")
	if status := statusOnly(t, env.h, key, http.MethodDelete, base+"/"+abort.ID, nil); status != http.StatusNoContent {
		t.Fatalf("abort multipart status=%d", status)
	}
	if status := statusOnly(t, env.h, key, http.MethodDelete, base+"/"+abort.ID, nil); status != http.StatusNoContent {
		t.Fatalf("idempotent abort status=%d", status)
	}
}

func createObjectMultipartUpload(t *testing.T, h *e2etest.Harness, key, base, objectKey string) api.ObjectMultipartUpload {
	t.Helper()
	raw, status := doReq(t, h, key, http.MethodPost, base, api.CreateObjectMultipartUploadRequest{Key: objectKey, SizeBytes: 10, ContentType: "application/octet-stream"})
	if status != http.StatusCreated {
		t.Fatalf("create multipart %q: %d %s", objectKey, status, raw)
	}
	var upload api.ObjectMultipartUpload
	if err := json.Unmarshal(raw, &upload); err != nil {
		t.Fatalf("decode multipart: %v", err)
	}
	return upload
}

func signAndUploadObjectMultipartPart(t *testing.T, h *e2etest.Harness, key, base, uploadID string, partNumber int, size int) api.ObjectMultipartCompletedPart {
	t.Helper()
	raw, status := doReq(t, h, key, http.MethodPost, fmt.Sprintf("%s/%s/parts/%d/signed-url", base, uploadID, partNumber), api.ObjectMultipartPartSignRequest{})
	if status != http.StatusOK {
		t.Fatalf("sign multipart part: %d %s", status, raw)
	}
	var signed api.ObjectSignedRequest
	if err := json.Unmarshal(raw, &signed); err != nil {
		t.Fatalf("decode signed part: %v", err)
	}
	if signed.URL == "" || signed.Method != http.MethodPut || signed.Headers["Content-Length"] == "" {
		t.Fatalf("signed part=%+v", signed)
	}
	body := bytes.Repeat([]byte("x"), size)
	req, err := http.NewRequest(http.MethodPut, signed.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("new signed part request: %v", err)
	}
	for name, value := range signed.Headers {
		req.Header.Set(name, value)
	}
	req.ContentLength = int64(len(body))
	resp, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("upload signed part: %v", err)
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatalf("read signed part response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("upload signed part status=%d", resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatalf("signed part response omitted ETag")
	}
	return api.ObjectMultipartCompletedPart{PartNumber: int32(partNumber), ETag: etag}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return raw
}

type objectStorageS3Object struct {
	body         []byte
	lastModified time.Time
}

type objectStorageS3Part struct {
	etag         string
	size         int64
	lastModified time.Time
}

type objectStorageS3Multipart struct {
	bucket string
	key    string
	parts  map[int]objectStorageS3Part
}

type objectStorageS3Stub struct {
	server *httptest.Server

	mu            sync.Mutex
	buckets       map[string]map[string]objectStorageS3Object
	multiparts    map[string]objectStorageS3Multipart
	createCalls   map[string]int
	completeCalls map[string]int
	nextUploadID  int
	failComplete  bool
}

func newObjectStorageS3Stub(t *testing.T) *objectStorageS3Stub {
	t.Helper()
	stub := &objectStorageS3Stub{
		buckets:     make(map[string]map[string]objectStorageS3Object),
		multiparts:  make(map[string]objectStorageS3Multipart),
		createCalls: make(map[string]int), completeCalls: make(map[string]int),
	}
	stub.server = httptest.NewServer(http.HandlerFunc(stub.handle))
	t.Cleanup(stub.server.Close)
	return stub
}

func (s *objectStorageS3Stub) createCount(bucket string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.createCalls[bucket]
}

func (s *objectStorageS3Stub) putObject(bucket, key string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	objects := s.buckets[bucket]
	if objects == nil {
		objects = make(map[string]objectStorageS3Object)
		s.buckets[bucket] = objects
	}
	objects[key] = objectStorageS3Object{body: append([]byte(nil), body...), lastModified: time.Now().UTC()}
}

func (s *objectStorageS3Stub) setCompleteFailure(value bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failComplete = value
}

func (s *objectStorageS3Stub) handle(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/")
	parts := strings.SplitN(path, "/", 2)
	bucket, key := "", ""
	if len(parts) > 0 {
		bucket, _ = url.PathUnescape(parts[0])
	}
	if len(parts) == 2 {
		key, _ = url.PathUnescape(parts[1])
	}
	query := r.URL.Query()

	switch {
	case r.Method == http.MethodPut && key == "" && !query.Has("uploads"):
		s.mu.Lock()
		if s.buckets[bucket] == nil {
			s.buckets[bucket] = make(map[string]objectStorageS3Object)
		}
		s.createCalls[bucket]++
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodDelete && key == "":
		s.mu.Lock()
		objects := s.buckets[bucket]
		if len(objects) != 0 {
			s.mu.Unlock()
			s3StubError(w, http.StatusConflict, "BucketNotEmpty", "bucket has objects")
			return
		}
		delete(s.buckets, bucket)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && key == "" && query.Get("list-type") == "2":
		s.listObjects(w, bucket, query.Get("prefix"))
	case r.Method == http.MethodPut && key != "" && query.Get("uploadId") == "":
		body, err := io.ReadAll(io.LimitReader(r.Body, api.MaxObjectSinglePutBytes+1))
		if err != nil {
			s3StubError(w, http.StatusInternalServerError, "InternalError", "read failed")
			return
		}
		s.putObject(bucket, key, body)
		w.Header().Set("ETag", `"e2e-object"`)
		w.WriteHeader(http.StatusOK)
	case r.Method == http.MethodDelete && key != "" && query.Get("uploadId") == "":
		s.mu.Lock()
		delete(s.buckets[bucket], key)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && key != "" && query.Get("uploadId") == "":
		s.mu.Lock()
		object, ok := s.buckets[bucket][key]
		s.mu.Unlock()
		if !ok {
			s3StubError(w, http.StatusNotFound, "NoSuchKey", "object not found")
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(object.body)))
		_, _ = w.Write(object.body)
	case r.Method == http.MethodGet && key == "" && query.Has("uploads"):
		s.listMultipartUploads(w, bucket)
	case r.Method == http.MethodPost && key != "" && query.Has("uploads"):
		s.initiateMultipart(w, bucket, key)
	case r.Method == http.MethodPut && key != "" && query.Get("uploadId") != "":
		s.putMultipartPart(w, r, bucket, key, query.Get("uploadId"), query.Get("partNumber"))
	case r.Method == http.MethodGet && key != "" && query.Get("uploadId") != "":
		s.listMultipartParts(w, bucket, key, query.Get("uploadId"))
	case r.Method == http.MethodPost && key != "" && query.Get("uploadId") != "":
		s.completeMultipart(w, r, bucket, key, query.Get("uploadId"))
	case r.Method == http.MethodDelete && key != "" && query.Get("uploadId") != "":
		s.abortMultipart(w, bucket, query.Get("uploadId"))
	default:
		s3StubError(w, http.StatusNotFound, "NotFound", "unsupported S3 request")
	}
}

func (s *objectStorageS3Stub) listObjects(w http.ResponseWriter, bucket, prefix string) {
	s.mu.Lock()
	objects := s.buckets[bucket]
	keys := make([]string, 0, len(objects))
	for key := range objects {
		if strings.HasPrefix(key, prefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	items := make([]objectStorageS3Object, len(keys))
	for i, key := range keys {
		items[i] = objects[key]
	}
	s.mu.Unlock()

	var body strings.Builder
	body.WriteString(`<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	body.WriteString(`<Name>` + html.EscapeString(bucket) + `</Name><Prefix>` + html.EscapeString(prefix) + `</Prefix>`)
	body.WriteString(fmt.Sprintf(`<KeyCount>%d</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>`, len(keys)))
	for i, key := range keys {
		body.WriteString(`<Contents><Key>` + html.EscapeString(key) + `</Key><LastModified>` + items[i].lastModified.Format(time.RFC3339) + `</LastModified><ETag>"e2e-object"</ETag>`)
		body.WriteString(fmt.Sprintf(`<Size>%d</Size><StorageClass>STANDARD</StorageClass></Contents>`, len(items[i].body)))
	}
	body.WriteString(`</ListBucketResult>`)
	s3StubXML(w, body.String())
}

func (s *objectStorageS3Stub) listMultipartUploads(w http.ResponseWriter, bucket string) {
	s.mu.Lock()
	keys := make([]string, 0)
	for id, upload := range s.multiparts {
		if upload.bucket == bucket {
			keys = append(keys, id)
		}
	}
	s.mu.Unlock()
	var body strings.Builder
	body.WriteString(`<ListMultipartUploadsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">`)
	body.WriteString(`<Name>` + html.EscapeString(bucket) + `</Name><KeyMarker></KeyMarker><UploadIdMarker></UploadIdMarker><NextKeyMarker></NextKeyMarker><NextUploadIdMarker></NextUploadIdMarker><Delimiter></Delimiter><Prefix></Prefix><MaxUploads>1000</MaxUploads><IsTruncated>false</IsTruncated>`)
	for _, id := range keys {
		s.mu.Lock()
		upload := s.multiparts[id]
		s.mu.Unlock()
		body.WriteString(`<Upload><Key>` + html.EscapeString(upload.key) + `</Key><UploadId>` + html.EscapeString(id) + `</UploadId><Initiator><ID>e2e</ID></Initiator><Owner><ID>e2e</ID></Owner><StorageClass>STANDARD</StorageClass><Initiated>` + time.Now().UTC().Format(time.RFC3339) + `</Initiated></Upload>`)
	}
	body.WriteString(`</ListMultipartUploadsResult>`)
	s3StubXML(w, body.String())
}

func (s *objectStorageS3Stub) initiateMultipart(w http.ResponseWriter, bucket, key string) {
	s.mu.Lock()
	s.nextUploadID++
	id := fmt.Sprintf("e2e-upload-%d", s.nextUploadID)
	s.multiparts[id] = objectStorageS3Multipart{bucket: bucket, key: key, parts: make(map[int]objectStorageS3Part)}
	s.mu.Unlock()
	s3StubXML(w, `<InitiateMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>`+html.EscapeString(bucket)+`</Bucket><Key>`+html.EscapeString(key)+`</Key><UploadId>`+id+`</UploadId></InitiateMultipartUploadResult>`)
}

func (s *objectStorageS3Stub) putMultipartPart(w http.ResponseWriter, r *http.Request, bucket, key, uploadID, rawPart string) {
	partNumber, err := strconv.Atoi(rawPart)
	if err != nil || partNumber < 1 {
		s3StubError(w, http.StatusBadRequest, "InvalidArgument", "invalid part")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, api.MaxObjectSinglePutBytes+1))
	if err != nil {
		s3StubError(w, http.StatusInternalServerError, "InternalError", "read failed")
		return
	}
	etag := fmt.Sprintf(`"e2e-part-%d"`, partNumber)
	s.mu.Lock()
	upload, ok := s.multiparts[uploadID]
	if ok && upload.bucket == bucket && upload.key == key {
		upload.parts[partNumber] = objectStorageS3Part{etag: etag, size: int64(len(body)), lastModified: time.Now().UTC()}
		s.multiparts[uploadID] = upload
	}
	s.mu.Unlock()
	if !ok {
		s3StubError(w, http.StatusNotFound, "NoSuchUpload", "upload not found")
		return
	}
	w.Header().Set("ETag", etag)
	w.WriteHeader(http.StatusOK)
}

func (s *objectStorageS3Stub) listMultipartParts(w http.ResponseWriter, bucket, key, uploadID string) {
	s.mu.Lock()
	upload, ok := s.multiparts[uploadID]
	parts := make([]int, 0)
	if ok && upload.bucket == bucket && upload.key == key {
		for number := range upload.parts {
			parts = append(parts, number)
		}
	}
	sort.Ints(parts)
	s.mu.Unlock()
	if !ok {
		s3StubError(w, http.StatusNotFound, "NoSuchUpload", "upload not found")
		return
	}
	var body strings.Builder
	body.WriteString(`<ListPartsResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Bucket>` + html.EscapeString(bucket) + `</Bucket><Key>` + html.EscapeString(key) + `</Key><UploadId>` + html.EscapeString(uploadID) + `</UploadId><Initiator><ID>e2e</ID></Initiator><Owner><ID>e2e</ID></Owner><StorageClass>STANDARD</StorageClass>`)
	for _, number := range parts {
		s.mu.Lock()
		part := upload.parts[number]
		s.mu.Unlock()
		body.WriteString(fmt.Sprintf(`<Part><PartNumber>%d</PartNumber><LastModified>%s</LastModified><ETag>%s</ETag><Size>%d</Size></Part>`, number, part.lastModified.Format(time.RFC3339), part.etag, part.size))
	}
	body.WriteString(`<IsTruncated>false</IsTruncated></ListPartsResult>`)
	s3StubXML(w, body.String())
}

func (s *objectStorageS3Stub) completeMultipart(w http.ResponseWriter, r *http.Request, bucket, key, uploadID string) {
	_, _ = io.ReadAll(r.Body)
	s.mu.Lock()
	s.completeCalls[uploadID]++
	fail := s.failComplete
	upload, ok := s.multiparts[uploadID]
	if fail {
		s.mu.Unlock()
		s3StubError(w, http.StatusServiceUnavailable, "SlowDown", "temporary provider failure")
		return
	}
	if !ok || upload.bucket != bucket || upload.key != key {
		s.mu.Unlock()
		s3StubError(w, http.StatusNotFound, "NoSuchUpload", "upload not found")
		return
	}
	var size int64
	for _, part := range upload.parts {
		size += part.size
	}
	if s.buckets[bucket] == nil {
		s.buckets[bucket] = make(map[string]objectStorageS3Object)
	}
	s.buckets[bucket][key] = objectStorageS3Object{body: make([]byte, size), lastModified: time.Now().UTC()}
	delete(s.multiparts, uploadID)
	s.mu.Unlock()
	s3StubXML(w, `<CompleteMultipartUploadResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/"><Location>`+html.EscapeString(r.URL.String())+`</Location><Bucket>`+html.EscapeString(bucket)+`</Bucket><Key>`+html.EscapeString(key)+`</Key><ETag>"e2e-complete"</ETag></CompleteMultipartUploadResult>`)
}

func (s *objectStorageS3Stub) abortMultipart(w http.ResponseWriter, bucket, uploadID string) {
	s.mu.Lock()
	upload, ok := s.multiparts[uploadID]
	if ok && upload.bucket == bucket {
		delete(s.multiparts, uploadID)
	}
	s.mu.Unlock()
	if !ok {
		s3StubError(w, http.StatusNotFound, "NoSuchUpload", "upload not found")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func s3StubXML(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, body)
}

func s3StubError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(status)
	_, _ = io.WriteString(w, `<Error><Code>`+html.EscapeString(code)+`</Code><Message>`+html.EscapeString(message)+`</Message><RequestId>e2e</RequestId></Error>`)
}
