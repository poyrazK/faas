package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 561
func TestBucketDefaultEncryptionControlE2EMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	var offset atomic.Int64
	now := time.Now().UTC()
	e.store.SetClockForTest(func() time.Time { return now.Add(time.Duration(offset.Load())) })
	bucketDefaultEncryptionControlE2E(t, e.s, e.store, e.acct, e.key, nil, func() { offset.Add(int64(api.ObjectBucketEncryptionRetry + time.Second)) })
}
func TestBucketDefaultEncryptionControlE2EPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	bucketDefaultEncryptionControlE2E(t, e.s, e.store, e.acct, e.key, e.pool, func() {
		if _, err := e.pool.Exec(t.Context(), `UPDATE object_bucket_encryption SET retry_at=clock_timestamp()`); err != nil {
			t.Fatal(err)
		}
	})
}

func bucketDefaultEncryptionControlE2E(t *testing.T, s *server, st state.Store, acct state.Account, bearer string, pool *pgxpool.Pool, retry func()) {
	t.Helper()
	identity, teardown := withTestIdentities(t)
	defer teardown()
	native := &signedURLNative{}
	var mu sync.Mutex
	configuration := ""
	puts := 0
	lost := true
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !r.URL.Query().Has("encryption") {
			native.serve(w, r)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/xml")
		switch r.Method {
		case http.MethodGet:
			if configuration == "" {
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `<Error><Code>ServerSideEncryptionConfigurationNotFoundError</Code></Error>`)
				return
			}
			_, _ = io.WriteString(w, configuration)
		case http.MethodPut:
			puts++
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			configuration = string(body)
			if lost {
				lost = false
				w.WriteHeader(500)
				_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
			}
		case http.MethodDelete:
			configuration = ""
			w.WriteHeader(204)
		default:
			t.Error(r.Method)
		}
	}))
	defer upstream.Close()
	registry, b, backend, _ := seedEncryptionJournalStorage(t, s, st, acct, upstream.URL)
	if err := s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	management := httptest.NewServer(s.handler())
	defer management.Close()
	client := api.NewClient(management.URL, bearer)
	selection := api.ObjectEncryption{Algorithm: "aws:kms", KeyID: backend.Encryption.Keys[0].Reference}
	caps, err := client.GetObjectBucketEncryptionCapabilities(t.Context(), "encrypted-journal", b.ID)
	if err != nil || !caps.BucketDefaults {
		t.Fatal("default capability missing", caps, err)
	}
	policy, err := client.PutObjectBucketEncryption(t.Context(), "encrypted-journal", b.ID, selection)
	if err != nil || policy.State != "waiting" || policy.Revision != 1 || policy.Encryption != nil || policy.DesiredEncryption == nil || policy.DesiredEncryption.KeyID != selection.KeyID {
		t.Fatal("owned intent missing", policy, err)
	}
	if err = s.reconcileObjectBucketEncryption(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	policy, err = client.GetObjectBucketEncryption(t.Context(), "encrypted-journal", b.ID)
	if err != nil || policy.State != "waiting" || policy.Encryption != nil {
		t.Fatal("lost ACK published policy", policy, err)
	}
	native.mu.Lock()
	native.disabled = true
	keysBefore := native.keys
	native.mu.Unlock()
	retry()
	if pool != nil {
		s.store = state.NewPgStore(pool)
	}
	s.WithObjectStorage(registry())
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcileObjectBucketEncryption(t.Context(), nil); err != nil {
		t.Fatal("disabled ingress blocked recovery", err)
	}
	policy, err = client.GetObjectBucketEncryption(t.Context(), "encrypted-journal", b.ID)
	if err != nil || policy.State != "ready" || policy.Encryption == nil || policy.Encryption.KeyID != selection.KeyID {
		t.Fatal("restart lost owned default", policy, err)
	}
	native.mu.Lock()
	keysAfter := native.keys
	native.disabled = false
	native.mu.Unlock()
	mu.Lock()
	configPuts := puts
	mu.Unlock()
	if configPuts != 1 || keysAfter != keysBefore {
		t.Fatal("recovery repeated configuration or key check", configPuts, keysBefore, keysAfter)
	}
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	var gateway http.Handler
	public := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gateway.ServeHTTP(w, r) }))
	defer public.Close()
	s.objectStorage.PublicEndpoint = public.URL
	h, err := s3gateway.New(s3gateway.Config{Registry: s.objectStorage, Store: s.store.(s3gateway.Store), RequestMetrics: s.store.(state.ObjectStorageProviderUsageStore), SpoolDir: t.TempDir(), OpenSecret: func(blob []byte) (string, error) {
		ns, plain, err := secretbox.OpenBytes(identity, blob)
		if err != nil || ns != s3gateway.CredentialSecretNamespace {
			return "", objectstorage.ErrConfiguration
		}
		return string(plain), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway = h
	size := int64(3)
	signed, err := client.SignBucketObject(t.Context(), "encrypted-journal", b.ID, api.ObjectSignRequest{Method: "PUT", Key: "default", SizeBytes: &size, ExpiresIn: 60, ContentType: "text/plain"})
	if err != nil || signed.UploadID == "" || signed.Headers["X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id"] != selection.KeyID {
		t.Fatal("signed URL did not bind default", signed, err)
	}
	request, err := http.NewRequestWithContext(t.Context(), signed.Method, signed.URL, strings.NewReader("abc"))
	if err != nil {
		t.Fatal(err)
	}
	for name, value := range signed.Headers {
		request.Header.Set(name, value)
	}
	response, err := public.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	_ = response.Body.Close()
	if response.StatusCode != 200 || response.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != selection.KeyID {
		t.Fatal("default encrypted broker PUT", response.StatusCode, response.Header)
	}
	receipt, err := s.store.(state.ObjectTrackedGatewayUploadStore).GetObjectUploadReceipt(t.Context(), acct.ID, b.AppID, "", signedCredentialSubject(t, s.store, signed.URL), signed.UploadID)
	if err != nil || receipt.EncryptionDefaultRevision != 1 || receipt.Status != "completed" || receipt.Encryption.ProviderKeyID != journalNativeKMSKey {
		t.Fatal("broker did not preserve default proof", receipt, err)
	}
	bucketDefaultEncryptionRoute(t, s, client, b, bearer, registry)
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	policy, err = client.DeleteObjectBucketEncryption(t.Context(), "encrypted-journal", b.ID)
	if err != nil || policy.State != "waiting" || policy.Revision != 2 {
		t.Fatal("disabled ingress blocked clear", policy, err)
	}
	if err = s.reconcileObjectBucketEncryption(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	policy, err = client.GetObjectBucketEncryption(t.Context(), "encrypted-journal", b.ID)
	if err != nil || policy.State != "ready" || policy.Encryption != nil || policy.Revision != 2 {
		t.Fatal("clear lost tombstone", policy, err)
	}
	native.mu.Lock()
	defer native.mu.Unlock()
	if native.writes != 2 {
		t.Fatal("unexpected object mutations", native.writes)
	}
}

func signedCredentialSubject(t *testing.T, st state.Store, signed string) string {
	t.Helper()
	parsed, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	access := strings.Split(parsed.Query().Get("X-Amz-Credential"), "/")[0]
	credential, _, err := st.(s3gateway.Store).ResolveObjectS3Credential(t.Context(), access)
	if err != nil {
		t.Fatal(err)
	}
	return credential.ID
}

func bucketDefaultEncryptionRoute(t *testing.T, s *server, client *api.Client, b state.ObjectBucket, bearer string, registry func() *objectstorage.Registry) {
	t.Helper()
	route, err := client.CreateObjectUploadRoute(t.Context(), "encrypted-journal", api.CreateObjectUploadRouteRequest{Name: "default-files", BucketID: b.ID, KeyPrefix: "uploads", MaxBytes: 3})
	if err != nil || route.Encryption != nil {
		t.Fatal(route, err)
	}
	handler, err := objectstorage.NewUploadHandler(objectstorage.UploadConfig{Store: s.store.(objectstorage.PublicReadStore), Routes: s.store.(state.ObjectUploadRouteStore), Buckets: s.store.(state.ObjectBucketStore), Authenticator: s.store, Registry: registry(), Accounting: s.store.(state.ObjectStorageAccountingStore), RequestMetrics: s.store.(state.ObjectStorageProviderUsageStore), AppsDomain: "apps.test", Next: http.NotFoundHandler()})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "http://encrypted-journal.apps.test/uploads/default-files", strings.NewReader("abc"))
	request.Header.Set("Authorization", "Bearer "+bearer)
	request.Header.Set("Content-Type", "text/plain")
	request.Header.Set("Idempotency-Key", "default-route")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatal("implicit encrypted route", response.Code, response.Body.String())
	}
	id := response.Header().Get("X-Gregale-Upload-ID")
	if id == "" {
		t.Fatal("route did not return its captured receipt")
	}
	receipt, err := s.store.(state.ObjectWriteReceiptStore).GetObjectWriteReceipt(t.Context(), b.AccountID, b.AppID, b.ID, id)
	if err != nil || receipt.Encryption == nil || receipt.Encryption.Algorithm != "aws:kms" || receipt.Status != "completed" {
		t.Fatal("default route did not settle its encrypted receipt", receipt, err)
	}
}
