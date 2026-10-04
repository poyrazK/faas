package objectstorage

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 561
func TestBucketEncryptionNativeRecoveryAndWrite(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprint(pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			var mu sync.Mutex
			native := `<ServerSideEncryptionConfiguration><Rule><ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault><BlockedEncryptionTypes><EncryptionType>NONE</EncryptionType><EncryptionType>SSE-C</EncryptionType></BlockedEncryptionTypes></Rule></ServerSideEncryptionConfiguration>`
			var lost atomic.Bool
			lost.Store(true)
			var disabled atomic.Bool
			var configWrites, objectWrites, keyChecks, gets atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("X-Amz-Target") == "TrentService.DescribeKey" {
					keyChecks.Add(1)
					w.Header().Set("Content-Type", "application/x-amz-json-1.1")
					body := encryptionKeyResponse()
					if disabled.Load() {
						body = strings.ReplaceAll(body, `"Enabled":true`, `"Enabled":false`)
					}
					_, _ = io.WriteString(w, body)
					return
				}
				if r.URL.Query().Has("encryption") {
					mu.Lock()
					defer mu.Unlock()
					w.Header().Set("Content-Type", "application/xml")
					switch r.Method {
					case http.MethodGet:
						gets.Add(1)
						_, _ = io.WriteString(w, native)
					case http.MethodPut:
						configWrites.Add(1)
						body, err := io.ReadAll(r.Body)
						if err != nil || !validBucketEncryptionResponse(body) || !strings.Contains(string(body), "<EncryptionType>NONE</EncryptionType>") || !strings.Contains(string(body), "<EncryptionType>SSE-C</EncryptionType>") {
							t.Error("native update dropped blocking settings", string(body), err)
						}
						native = string(body)
						if lost.Swap(false) {
							w.WriteHeader(500)
							_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
						}
					case http.MethodDelete:
						configWrites.Add(1)
						native = `<ServerSideEncryptionConfiguration><Rule><ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault></Rule></ServerSideEncryptionConfiguration>`
					default:
						t.Error("unexpected configuration request", r.Method)
					}
					return
				}
				if r.Method != http.MethodPut {
					t.Error("unexpected object request", r.Method)
					return
				}
				objectWrites.Add(1)
				body, err := io.ReadAll(r.Body)
				if err != nil || string(body) != "abc" || r.Header.Get("X-Amz-Server-Side-Encryption") != "aws:kms" || r.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != encryptionTestNativeKey || r.Header.Get("X-Amz-Meta-"+ReservedObjectEncryptionMetadataKey) == "" {
					t.Error("default was absent from native write", err)
				}
				for name, values := range r.Header {
					if strings.HasPrefix(strings.ToLower(name), "x-amz-server-side-encryption") {
						w.Header()[name] = values
					}
				}
				w.Header().Set("ETag", `"defaulted"`)
			}))
			defer server.Close()
			backend := encryptionTestBackend()
			backend.Endpoint, backend.Encryption.KMSEndpoint, backend.AllowHTTP = server.URL, server.URL, true
			backend.Encryption.Keys[0].AccountID = uuid.MustParse(f.bucket.AccountID).String()
			_, placement := encryptionTestRegistry(t, backend)
			p := placement.Provider.(*S3)
			e, err := p.encryption.Resolve(uuid.MustParse(f.bucket.AccountID).String(), api.ObjectEncryption{Algorithm: "aws:kms", KeyID: p.encryption.Keys[0].Reference})
			if err != nil {
				t.Fatal(err)
			}
			defaults := f.st.(state.ObjectBucketEncryptionStore)
			metrics := f.st.(state.ObjectStorageProviderUsageStore)
			svc := BucketEncryptionService{Store: defaults, Provider: p, BeforeRequest: VersioningRequestRecorder(metrics, f.bucket.ID)}
			if _, err = svc.Request(t.Context(), f.bucket, e); err != nil {
				t.Fatal(err)
			}
			if _, err = svc.Reconcile(t.Context(), f.bucket); !errors.Is(err, ErrUnavailable) {
				t.Fatal("lost acknowledgment was published", err)
			}
			j, err := defaults.GetObjectBucketEncryption(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
			if err != nil || j.State != "waiting" || !j.Dispatched || !j.Encryption.Empty() {
				t.Fatal("uncertain config was admitted", j, err)
			}
			if f.pool != nil {
				if _, err = f.pool.Exec(t.Context(), `UPDATE object_bucket_encryption SET retry_at=clock_timestamp() WHERE bucket_id=$1`, f.bucket.ID); err != nil {
					t.Fatal(err)
				}
			} else {
				f.retry()
			}
			disabled.Store(true)
			st := f.reopen()
			svc.Store = st.(state.ObjectBucketEncryptionStore)
			svc.Provider = restartEncryptionS3(t, p)
			j, err = svc.Reconcile(t.Context(), f.bucket)
			if err != nil || j.State != "ready" || !j.Encryption.Equal(e) || configWrites.Load() != 1 || keyChecks.Load() != 1 || gets.Load() != 2 {
				t.Fatal("restart repeated mutation or reauthorized saved key", j, err, configWrites.Load(), keyChecks.Load(), gets.Load())
			}
			writer := st.(state.ObjectTrackedGatewayUploadStore)
			c, err := writer.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: f.bucket.AccountID, AppID: f.bucket.AppID, BucketID: f.bucket.ID, SubjectID: "default", Key: "default", Bytes: 3, Status: "pending"}, f.policy)
			if err != nil || !c.Encryption.Equal(e) || c.EncryptionDefaultRevision != 1 {
				t.Fatal(c, err)
			}
			encrypted := svc.Provider.(*S3)
			writeCtx := WithEncryptionRequestRecorder(t.Context(), svc.before)
			writeCtx = WithEncryptionWriteRecorder(writeCtx, func(ctx context.Context) error {
				var dispatchErr error
				c, dispatchErr = writer.DispatchTrackedObjectUpload(ctx, c.AccountID, c.BucketID, c.ID)
				return dispatchErr
			})
			if _, err = encrypted.WriteEncryptedObject(writeCtx, f.bucket.PhysicalName, c.Key, c.ID, strings.NewReader("abc"), 3, ObjectMetadata{ContentType: "text/plain"}, c.Encryption); !errors.Is(err, ErrConfiguration) || objectWrites.Load() != 0 {
				t.Fatal("disabled key dispatched object", err)
			}
			disabled.Store(false)
			result, err := encrypted.WriteEncryptedObject(writeCtx, f.bucket.PhysicalName, c.Key, c.ID, strings.NewReader("abc"), 3, ObjectMetadata{ContentType: "text/plain"}, c.Encryption)
			if err != nil {
				t.Fatal(err)
			}
			c.Status, c.ETag, c.VerifiedEncryption = "completed", result.ETag, result.Encryption
			if c, err = writer.FinishTrackedObjectUpload(t.Context(), c); err != nil || c.EncryptionDefaultRevision != 1 {
				t.Fatal(c, err)
			}
			if _, err = svc.Request(t.Context(), f.bucket, ResolvedObjectEncryption{}); err != nil {
				t.Fatal(err)
			}
			j, err = svc.Reconcile(t.Context(), f.bucket)
			if err != nil || !j.Encryption.Empty() || j.Revision != 2 || configWrites.Load() != 2 {
				t.Fatal("clear failed to retain blocking policy", j, err)
			}
			observed, err := encrypted.GetBucketEncryption(t.Context(), f.bucket.PhysicalName)
			if err != nil || observed.Algorithm != "AES256" || len(observed.BlockedEncryptionTypes) != 2 {
				t.Fatal(observed, err)
			}
			// An external change is fenced and scheduled through a new revision.
			mu.Lock()
			native = `<ServerSideEncryptionConfiguration><Rule><ApplyServerSideEncryptionByDefault><SSEAlgorithm>aws:kms</SSEAlgorithm><KMSMasterKeyID>` + encryptionTestNativeKey + `</KMSMasterKeyID></ApplyServerSideEncryptionByDefault></Rule></ServerSideEncryptionConfiguration>`
			mu.Unlock()
			j, _, err = svc.Read(t.Context(), f.bucket)
			if err != nil || j.State != "waiting" || j.Revision != 3 || !j.DesiredEncryption.Empty() {
				t.Fatal("drift was advertised as ready", j, err)
			}
			j, err = svc.Reconcile(t.Context(), f.bucket)
			if err != nil || j.State != "ready" || j.Revision != 3 || !j.Encryption.Empty() {
				t.Fatal("drift was not reconciled", j, err)
			}
		})
	}
}

func TestS3BucketEncryptionStrictProtocol(t *testing.T) {
	good := `<ServerSideEncryptionConfiguration><Rule><ApplyServerSideEncryptionByDefault><SSEAlgorithm>AES256</SSEAlgorithm></ApplyServerSideEncryptionByDefault><BucketKeyEnabled>false</BucketKeyEnabled></Rule></ServerSideEncryptionConfiguration>`
	for _, body := range []string{
		"<Error><Code>InternalError</Code></Error>",
		"<ServerSideEncryptionConfiguration/>",
		strings.ReplaceAll(good, "<SSEAlgorithm>AES256</SSEAlgorithm>", "<SSEAlgorithm>AES256</SSEAlgorithm><SSEAlgorithm>aws:kms</SSEAlgorithm>"),
		strings.ReplaceAll(good, "<BucketKeyEnabled>false</BucketKeyEnabled>", "<BucketKeyEnabled>true</BucketKeyEnabled>"),
		strings.ReplaceAll(good, "<BucketKeyEnabled>false</BucketKeyEnabled>", "<Unknown/>"),
		strings.ReplaceAll(good, "</Rule>", "<BlockedEncryptionTypes><EncryptionType>future</EncryptionType></BlockedEncryptionTypes></Rule>"),
		strings.ReplaceAll(good, "<SSEAlgorithm>", "<SSEAlgorithm xmlns=\"urn:unexpected\">"),
		good + good,
		strings.Repeat(" ", 17<<10),
	} {
		t.Run(fmt.Sprint(len(body)), func(t *testing.T) {
			var calls atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); _, _ = io.WriteString(w, body) })).(BucketEncryptionProvider)
			if _, err := p.GetBucketEncryption(t.Context(), "physical"); !errors.Is(err, ErrUnavailable) || calls.Load() != 1 {
				t.Fatal(err, calls.Load())
			}
		})
	}
	var calls atomic.Int32
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method == http.MethodDelete {
			w.WriteHeader(204)
			return
		}
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>ServerSideEncryptionConfigurationNotFoundError</Code></Error>`)
	})).(BucketEncryptionProvider)
	n, err := p.GetBucketEncryption(t.Context(), "physical")
	if err != nil || n.Algorithm != "" {
		t.Fatal(n, err)
	}
	if err = p.ClearBucketEncryption(t.Context(), "physical", n); err != nil || calls.Load() != 2 {
		t.Fatal(err, calls.Load())
	}
	var parsed struct{ XMLName xml.Name }
	if xml.Unmarshal([]byte(good), &parsed) != nil || !validBucketEncryptionResponse([]byte(good)) {
		t.Fatal("valid provider AES baseline rejected")
	}
}
