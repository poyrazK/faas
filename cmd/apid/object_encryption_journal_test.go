//go:build !no_pg

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

const journalNativeKMSKey = "arn:aws:kms:us-east-1:111122223333:key/abcd8987-12d6-45ad-a4bc-d384c10d9149"

type encryptionJournalHTTP struct {
	mu                                        sync.Mutex
	headers                                   http.Header
	part                                      string
	completed                                 bool
	disabled                                  bool
	partWrites                                int
	requests, creates, completions, keyChecks int
}

func (f *encryptionJournalHTTP) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	switch {
	case r.Header.Get("X-Amz-Target") == "TrentService.DescribeKey":
		f.keyChecks++
		if f.disabled {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = io.WriteString(w, `{"__type":"DisabledException"}`)
			return
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = io.WriteString(w, `{"KeyMetadata":{"Arn":"`+journalNativeKMSKey+`","AWSAccountId":"111122223333","KeyId":"abcd8987-12d6-45ad-a4bc-d384c10d9149","Enabled":true,"KeyState":"Enabled","KeyUsage":"ENCRYPT_DECRYPT","KeySpec":"SYMMETRIC_DEFAULT","KeyManager":"CUSTOMER"}}`)
	case r.Method == http.MethodGet && r.URL.Query().Has("uploads"):
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	case r.Method == http.MethodPost && r.URL.Query().Has("uploads"):
		f.creates++
		f.headers = r.Header.Clone()
		if r.Header.Get("X-Amz-Server-Side-Encryption") != "aws:kms" || r.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != journalNativeKMSKey || r.Header.Get("X-Amz-Server-Side-Encryption-Bucket-Key-Enabled") != "false" || r.Header.Get("X-Amz-Meta-"+objectstorage.ReservedObjectEncryptionMetadataKey) == "" {
			t.Error("initializer dropped encryption snapshot")
		}
		nativeEncryptionJournalHeaders(w)
		_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><Bucket>physical</Bucket><Key>encrypted</Key><UploadId>native-upload</UploadId></InitiateMultipartUploadResult>`)
	case r.Method == http.MethodPut && r.URL.Query().Get("uploadId") == "native-upload":
		f.partWrites++
		data, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		f.part = string(data)
		nativeEncryptionJournalHeaders(w)
		w.Header().Set("ETag", `"part"`)
	case r.Method == http.MethodGet && r.URL.Query().Get("uploadId") == "native-upload":
		if f.completed {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>"part"</ETag><Size>3</Size></Part></ListPartsResult>`)
	case r.Method == http.MethodPost && r.URL.Query().Get("uploadId") == "native-upload":
		f.completions++
		if f.completed {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		if f.part != "abc" {
			t.Error("completion did not retain native part")
		}
		f.completed = true
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Error(err)
			return
		}
		_ = conn.Close()
	case r.Method == http.MethodHead:
		if !f.completed {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		nativeEncryptionJournalHeaders(w)
		w.Header().Set("Content-Length", strconv.Itoa(len(f.part)))
		w.Header().Set("ETag", `"completed"`)
		w.Header().Set("X-Amz-Version-Id", "native-private-version")
		w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedMultipartSessionMetadataKey, f.headers.Get("X-Amz-Meta-"+objectstorage.ReservedMultipartSessionMetadataKey))
		w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedObjectEncryptionMetadataKey, f.headers.Get("X-Amz-Meta-"+objectstorage.ReservedObjectEncryptionMetadataKey))
	default:
		t.Errorf("unexpected native journal request: %s %s", r.Method, r.URL)
		w.WriteHeader(500)
	}
}

func nativeEncryptionJournalHeaders(w http.ResponseWriter) {
	w.Header().Set("X-Amz-Server-Side-Encryption", "aws:kms")
	w.Header().Set("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id", journalNativeKMSKey)
	w.Header().Set("X-Amz-Server-Side-Encryption-Bucket-Key-Enabled", "false")
}

// adr: 555
func TestObjectEncryptionMultipartWorkerMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	encryptionMultipartWorker(t, e.s, e.store, e.acct, nil)
}
func TestObjectEncryptionMultipartWorkerPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	encryptionMultipartWorker(t, e.s, e.store, e.acct, e.pool)
}

func encryptionMultipartWorker(t *testing.T, s *server, st state.Store, account state.Account, pool *pgxpool.Pool) {
	ctx := t.Context()
	fixture := &encryptionJournalHTTP{}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { fixture.serve(t, w, r) }))
	defer upstream.Close()
	registry, b, backend, policy := seedEncryptionJournalStorage(t, s, st, account, upstream.URL)
	snapshot, err := backend.Encryption.Resolve(uuid.MustParse(account.ID).String(), api.ObjectEncryption{Algorithm: "aws:kms", KeyID: backend.Encryption.Keys[0].Reference})
	if err != nil {
		t.Fatal(err)
	}
	sessions := st.(state.ObjectMultipartUploadStore)
	transfers := st.(state.ObjectMultipartTransferStore)
	u, err := sessions.ReserveObjectMultipartUpload(ctx, state.ObjectMultipartUpload{ID: uuid.NewString(), AccountID: account.ID, AppID: b.AppID, BucketID: b.ID, Key: "encrypted", ExpiresAt: time.Now().Add(time.Hour), Encryption: snapshot}, 100)
	if err != nil {
		t.Fatal(err)
	}
	u, err = sessions.ClaimObjectMultipartUpload(ctx, account.ID, b.AppID, b.ID, u.ID, "init", state.ObjectMultipartInitiating, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.executeObjectMultipartOperation(ctx, sessions, b, u); err != nil {
		t.Fatal(err)
	}
	u, err = sessions.GetObjectMultipartUpload(ctx, account.ID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = transfers.BeginObjectMultipartPart(ctx, account.ID, b.ID, u.ID, "part", 1, 3, 100, policy); err != nil {
		t.Fatal(err)
	}
	signed, err := backend.Provider.PresignMultipartPart(ctx, b.PhysicalName, objectstorage.MultipartPartRequest{Key: u.Key, ProviderUploadID: u.ProviderUploadID, PartNumber: 1, SizeBytes: 3, ExpiresIn: 60})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, signed.URL, strings.NewReader("abc"))
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range signed.Headers {
		request.Header.Set(key, value)
	}
	if err = st.(state.ObjectStorageProviderUsageStore).RecordObjectStorageProviderRequest(ctx, b.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	response, err := upstream.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	if err = transfers.SettleObjectMultipartPart(ctx, account.ID, u.ID, 1, "part"); err != nil {
		t.Fatal(err)
	}
	u, err = sessions.GetObjectMultipartUpload(ctx, account.ID, b.AppID, b.ID, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	u, err = transfers.PrepareObjectMultipartCompletion(ctx, u, "complete", 3, []api.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: `"part"`}}, policy)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.executeObjectMultipartOperation(ctx, sessions, b, u); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("lost completion acknowledged", err)
	}
	u, err = sessions.GetObjectMultipartUpload(ctx, account.ID, b.AppID, b.ID, u.ID)
	if err != nil || u.State != state.ObjectMultipartCompleting || !u.Encryption.Equal(snapshot) {
		t.Fatal("uncertain intent lost", u, err)
	}
	if pool != nil {
		if _, err = pool.Exec(ctx, `UPDATE object_storage_multipart_uploads SET retry_at=now()-interval '1 second' WHERE id=$1`, u.ID); err != nil {
			t.Fatal(err)
		}
		st = state.NewPgStore(pool)
	} else {
		st.(*state.MemStore).SetClockForTest(func() time.Time { return time.Now().Add(2 * time.Minute) })
	}
	// Reconstruct both the owner and native clients. Ingress is disabled by default.
	fresh := newServer(st, s.log, "gregale.dev", noopNotifier{}).WithObjectStorage(registry())
	if err = fresh.reconcileObjectMultipartUploads(ctx, nil); err != nil {
		t.Fatal(err)
	}
	done, err := st.(state.ObjectMultipartUploadStore).GetObjectMultipartUpload(ctx, account.ID, b.AppID, b.ID, u.ID)
	if err != nil || done.State != state.ObjectMultipartCompleted || done.CompletionETag != `"completed"` || !state.ValidObjectVersionID(done.CompletionVersionID) || !done.Encryption.Equal(snapshot) {
		t.Fatal("reconstruction failed", done, err)
	}
	raw, err := json.Marshal(viewMultipartUpload(done))
	if err != nil || strings.Contains(string(raw), journalNativeKMSKey) || strings.Contains(string(raw), "native-private") {
		t.Fatal("private enrollment escaped projection", string(raw), err)
	}
	metrics, err := st.(state.ObjectStorageProviderUsageStore).ListObjectStorageProviderRequestMetrics(ctx, backend.ID, backend.Fingerprint, state.ObjectStoragePeriod(time.Now()))
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	// Bound initiation creates once without a discovery listing; every actual
	// native request must still have its corresponding usage record.
	if err != nil || len(metrics) != 1 || metrics[0].RequestCount != int64(fixture.requests) || fixture.requests != 6 || fixture.creates != 1 || fixture.completions != 2 || fixture.keyChecks != 1 {
		t.Fatal("native dispatch/metering mismatch", metrics, err, fixture.requests, fixture.creates, fixture.completions, fixture.keyChecks)
	}
}

func seedEncryptionJournalStorage(t *testing.T, s *server, st state.Store, account state.Account, endpoint string) (func() *objectstorage.Registry, state.ObjectBucket, objectstorage.Backend, api.ObjectStoragePolicy) {
	t.Helper()
	ctx := t.Context()
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 100, MaxBucketBytes: 100, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 100, MaxMonthlyEgressBytes: 100, MaxMonthlyAuthorizations: 100, MaxReportAgeSeconds: 3600}
	config := objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "test"}, Backends: []objectstorage.BackendConfig{{ID: "test", Driver: "s3", Region: "us-east-1", Namespace: "test", Endpoint: endpoint, S3Region: "us-east-1", PathStyle: true, AllowHTTP: true, AccessKeyEnv: "TEST_KEY", SecretKeyEnv: "TEST_SECRET", Encryption: objectstorage.EncryptionConfig{KMSEndpoint: endpoint, Algorithms: []string{"aws:kms"}, Keys: []objectstorage.EncryptionKeyBinding{{ID: uuid.NewString(), AccountID: uuid.MustParse(account.ID).String(), ProviderKeyID: journalNativeKMSKey}}}}}}
	registry := func() *objectstorage.Registry {
		t.Helper()
		r, err := objectstorage.NewRegistry(config, func(string) string { return "fixture-secret" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	s.WithObjectStorage(registry())
	backend, err := s.objectStorage.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "encrypted-journal", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	buckets := st.(state.ObjectBucketStore)
	b, err := buckets.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, PhysicalName: "physical", Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(ctx, account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = buckets.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	accounting := st.(state.ObjectStorageAccountingStore)
	if err = accounting.ClaimObjectInventory(ctx, b.ID, "baseline"); err != nil {
		t.Fatal(err)
	}
	if err = accounting.FinishObjectInventory(ctx, b.ID, "baseline", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err = accounting.RecordObjectUsageReport(ctx, api.ObjectStorageUsageReport{AccountID: account.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "fixture", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	return registry, b, backend, policy
}

func TestObjectEncryptionWriteRecoveryMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	encryptionWriteRecovery(t, e.s, e.store, e.acct, nil)
}
func TestObjectEncryptionWriteRecoveryPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	encryptionWriteRecovery(t, e.s, e.store, e.acct, e.pool)
}

func encryptionWriteRecovery(t *testing.T, s *server, st state.Store, account state.Account, pool *pgxpool.Pool) {
	var mu sync.Mutex
	var stored http.Header
	writes, keyChecks := 0, 0
	corrupt, disabled := false, false
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Header.Get("X-Amz-Target") == "TrentService.DescribeKey":
			keyChecks++
			if disabled {
				w.WriteHeader(400)
				_, _ = io.WriteString(w, `{"__type":"DisabledException"}`)
				return
			}
			w.Header().Set("Content-Type", "application/x-amz-json-1.1")
			_, _ = io.WriteString(w, `{"KeyMetadata":{"Arn":"`+journalNativeKMSKey+`","AWSAccountId":"111122223333","KeyId":"abcd8987-12d6-45ad-a4bc-d384c10d9149","Enabled":true,"KeyState":"Enabled","KeyUsage":"ENCRYPT_DECRYPT","KeySpec":"SYMMETRIC_DEFAULT","KeyManager":"CUSTOMER"}}`)
		case r.Method == http.MethodPut:
			data, err := io.ReadAll(r.Body)
			if err != nil || string(data) != "abc" {
				t.Error("write body changed", err)
			}
			writes++
			stored = r.Header.Clone()
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		case r.Method == http.MethodHead:
			nativeEncryptionJournalHeaders(w)
			w.Header().Set("Content-Length", "3")
			w.Header().Set("ETag", `"write-proof"`)
			w.Header().Set("X-Amz-Version-Id", "native-private-write")
			for _, name := range []string{objectstorage.ReservedUploadReceiptMetadataKey, objectstorage.ReservedObjectEncryptionMetadataKey} {
				w.Header().Set("X-Amz-Meta-"+name, stored.Get("X-Amz-Meta-"+name))
			}
			if corrupt {
				w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedObjectEncryptionMetadataKey, strings.Repeat("0", 64))
			}
		case r.Method == http.MethodGet && r.URL.Query().Has("versions"):
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated></ListVersionsResult>`)
		default:
			t.Errorf("unexpected write recovery request: %s %s", r.Method, r.URL)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	registry, b, backend, policy := seedEncryptionJournalStorage(t, s, st, account, upstream.URL)
	snapshot, err := backend.Encryption.Resolve(uuid.MustParse(account.ID).String(), api.ObjectEncryption{Algorithm: "aws:kms", KeyID: backend.Encryption.Keys[0].Reference})
	if err != nil {
		t.Fatal(err)
	}
	journals := st.(state.ObjectTrackedGatewayUploadStore)
	c, err := journals.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: account.ID, AppID: b.AppID, BucketID: b.ID, SubjectID: "subject", Key: "write", Bytes: 3, Status: "pending", Encryption: snapshot}, policy)
	if err != nil {
		t.Fatal(err)
	}
	c, err = journals.DispatchTrackedObjectUpload(t.Context(), account.ID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = backend.Provider.(objectstorage.ObjectEncryptionProvider).WriteEncryptedObject(t.Context(), b.PhysicalName, c.Key, c.ID, strings.NewReader("abc"), 3, objectstorage.ObjectMetadata{}, c.Encryption); !errors.Is(err, objectstorage.ErrUnavailable) {
		t.Fatal("lost write acknowledged", err)
	}
	mu.Lock()
	disabled, corrupt = true, true
	mu.Unlock()
	// Ordinary receipt/size matches cannot settle the deliberately corrupted proof.
	if _, err = s.probeObjectUpload(t.Context(), c); err == nil {
		t.Fatal("corrupted encrypted proof accepted")
	}
	mu.Lock()
	corrupt = false
	mu.Unlock()
	if pool != nil {
		if _, err = pool.Exec(t.Context(), `UPDATE object_upload_completions SET recovery_retry_at=now()-interval '1 second' WHERE id=$1`, c.ID); err != nil {
			t.Fatal(err)
		}
		st = state.NewPgStore(pool)
	} else {
		st.(*state.MemStore).SetClockForTest(func() time.Time { return time.Now().Add(2 * time.Minute) })
	}
	fresh := newServer(st, s.log, "gregale.dev", noopNotifier{}).WithObjectStorage(registry())
	if err = fresh.reconcileObjectUploads(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	done, err := st.(state.ObjectTrackedUploadStore).GetObjectUploadReceipt(t.Context(), account.ID, b.AppID, "", "subject", c.ID)
	if err != nil || done.Status != "completed" || done.ETag != `"write-proof"` || !done.Encryption.Equal(snapshot) || !state.ValidObjectVersionID(done.VersionID) {
		t.Fatal("disabled-key proof lost on restart", done, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if writes != 1 || keyChecks != 1 {
		t.Fatal("recovery replayed write or enabled-key check", writes, keyChecks)
	}
}
