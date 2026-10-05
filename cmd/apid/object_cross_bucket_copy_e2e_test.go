//go:build !no_pg

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 562
type crossCopyNative struct {
	mu                                        sync.Mutex
	requests, sourceHeads, copies, partCopies int
	policy                                    string
	objects                                   map[string]http.Header
	uploads                                   map[string]http.Header
	parts                                     map[string]bool
}

func (f *crossCopyNative) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests++
	w.Header().Set("Content-Type", "application/xml")
	key := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/physical/"), "/source-private/")
	q := r.URL.Query()
	switch {
	case r.Header.Get("X-Amz-Target") == "TrentService.DescribeKey":
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = io.WriteString(w, `{"KeyMetadata":{"Arn":"`+journalNativeKMSKey+`","AWSAccountId":"111122223333","KeyId":"abcd8987-12d6-45ad-a4bc-d384c10d9149","Enabled":true,"KeyState":"Enabled","KeyUsage":"ENCRYPT_DECRYPT","KeySpec":"SYMMETRIC_DEFAULT","KeyManager":"CUSTOMER"}}`)
	case q.Has("encryption"):
		switch r.Method {
		case "GET":
			if f.policy == "" {
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `<Error><Code>ServerSideEncryptionConfigurationNotFoundError</Code></Error>`)
				return
			}
			_, _ = io.WriteString(w, f.policy)
		case "PUT":
			b, e := io.ReadAll(r.Body)
			if e != nil {
				t.Error(e)
			}
			f.policy = string(b)
		default:
			t.Error("unexpected policy method", r.Method)
			w.WriteHeader(500)
		}
	case r.Method == "HEAD" && strings.HasPrefix(r.URL.Path, "/source-private/"):
		f.sourceHeads++
		if key != "allowed/source" {
			t.Error("ungranted native source probe", r.URL)
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Length", fmt.Sprint(api.MinMultipartPartBytes+1))
		w.Header().Set("ETag", `"source"`)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("X-Amz-Meta-Owner", "customer")
		w.Header().Set("X-Amz-Meta-"+objectstorage.ReservedUploadReceiptMetadataKey, "source-proof")
		if q.Get("versionId") != "" {
			w.Header().Set("X-Amz-Version-Id", q.Get("versionId"))
		}
	case q.Has("uploads") && r.Method == "GET":
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	case q.Has("uploads") && r.Method == "POST":
		f.uploads[key] = r.Header.Clone()
		nativeEncryptionJournalHeaders(w)
		_, _ = fmt.Fprintf(w, `<InitiateMultipartUploadResult><UploadId>native-%s</UploadId></InitiateMultipartUploadResult>`, key)
	case q.Get("uploadId") != "":
		if _, ok := f.uploads[key]; !ok {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		switch r.Method {
		case "PUT":
			f.partCopies++
			f.checkSource(t, r)
			if r.Header.Get("X-Amz-Copy-Source-Range") != "bytes=1-3" {
				t.Error("part range lost")
			}
			if r.Header.Get("X-Amz-Server-Side-Encryption") != "" {
				t.Error("part changed destination cipher")
			}
			f.parts[key] = true
			if key == "multipart-lost" {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				_ = conn.Close()
				return
			}
			f.sourceVersionHeader(w, r)
			_, _ = io.WriteString(w, `<CopyPartResult><ETag>&quot;part&quot;</ETag><LastModified>2026-10-04T00:00:00Z</LastModified></CopyPartResult>`)
		case "GET":
			if !f.parts[key] {
				_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated></ListPartsResult>`)
				return
			}
			_, _ = io.WriteString(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>&quot;part&quot;</ETag><Size>3</Size></Part></ListPartsResult>`)
		case "POST":
			f.objects[key] = f.uploads[key]
			delete(f.uploads, key)
			nativeEncryptionJournalHeaders(w)
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>&quot;copied&quot;</ETag></CompleteMultipartUploadResult>`)
		case "DELETE":
			delete(f.uploads, key)
			w.WriteHeader(204)
		default:
			t.Error("unexpected multipart method", r.Method)
			w.WriteHeader(500)
		}
	case r.Method == "PUT":
		f.copies++
		f.checkSource(t, r)
		if !strings.HasPrefix(r.URL.Path, "/physical/") || r.Header.Get("X-Amz-Meta-Owner") != "customer" || r.Header.Get("Content-Type") != "image/png" {
			t.Error("copy lost destination or source metadata")
		}
		if r.Header.Get("X-Amz-Server-Side-Encryption") != "aws:kms" || r.Header.Get("X-Amz-Server-Side-Encryption-Aws-Kms-Key-Id") != journalNativeKMSKey {
			t.Error("destination default cipher lost")
		}
		receipt := r.Header.Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)
		if receipt == "" || receipt == "source-proof" {
			t.Error("source receipt propagated")
		}
		f.objects[key] = r.Header.Clone()
		if key == "lost" {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		nativeEncryptionJournalHeaders(w)
		f.sourceVersionHeader(w, r)
		_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;copied&quot;</ETag><LastModified>2026-10-04T00:00:00Z</LastModified></CopyObjectResult>`)
	case r.Method == "HEAD":
		headers, ok := f.objects[key]
		if !ok {
			w.WriteHeader(404)
			return
		}
		nativeEncryptionJournalHeaders(w)
		w.Header().Set("ETag", `"copied"`)
		size := fmt.Sprint(api.MinMultipartPartBytes + 1)
		if strings.HasPrefix(key, "multipart") {
			size = "3"
		}
		w.Header().Set("Content-Length", size)
		for name, values := range headers {
			if strings.HasPrefix(strings.ToLower(name), "x-amz-meta-") {
				w.Header()[name] = values
			}
		}
	default:
		t.Error("unexpected cross-copy native request", r.Method, r.URL)
		w.WriteHeader(500)
	}
}
func (f *crossCopyNative) checkSource(t *testing.T, r *http.Request) {
	t.Helper()
	raw, err := url.PathUnescape(r.Header.Get("X-Amz-Copy-Source"))
	source, _, _ := strings.Cut(raw, "?")
	if err != nil || source != "source-private/allowed/source" || r.Header.Get("X-Amz-Copy-Source-If-Match") != `"source"` {
		t.Error("copy did not bind authorized source", raw, err)
	}
}
func (f *crossCopyNative) sourceVersionHeader(w http.ResponseWriter, r *http.Request) {
	_, query, ok := strings.Cut(r.Header.Get("X-Amz-Copy-Source"), "?")
	if !ok {
		return
	}
	q, _ := url.ParseQuery(query)
	w.Header().Set("X-Amz-Copy-Source-Version-Id", q.Get("versionId"))
}
func (f *crossCopyNative) counts() (int, int, int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests, f.sourceHeads, f.copies, f.partCopies
}

func TestCrossBucketCopyControlE2EMem(t *testing.T) {
	e := setup(t, api.PlanPro)
	crossBucketCopyControlE2E(t, e.s, e.store, e.acct, e.key, nil)
}
func TestCrossBucketCopyControlE2EPG(t *testing.T) {
	e := setupPGHandler(t, api.PlanPro)
	crossBucketCopyControlE2E(t, e.s, e.store, e.acct, e.key, e.pool)
}
func crossBucketCopyControlE2E(t *testing.T, s *server, st state.Store, acct state.Account, bearer string, pool *pgxpool.Pool) {
	identity, teardown := withTestIdentities(t)
	defer teardown()
	native := &crossCopyNative{objects: map[string]http.Header{}, uploads: map[string]http.Header{}, parts: map[string]bool{}}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }))
	defer upstream.Close()
	registry, b, backend, _ := seedEncryptionJournalStorage(t, s, st, acct, upstream.URL)
	s.objectStorage.Accounting.MaxAccountBytes = 100 * 1024 * 1024
	s.objectStorage.Accounting.MaxBucketBytes = 100 * 1024 * 1024
	app, err := st.CreateApp(t.Context(), state.App{AccountID: acct.ID, Slug: "copy-origin", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	buckets := st.(state.ObjectBucketStore)
	source, err := buckets.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Name: b.Name, Scope: "default", Region: b.Region, BackendID: b.BackendID, BackendFingerprint: b.BackendFingerprint, PhysicalName: "source-private"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(t.Context(), acct.ID, app.ID, source.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = buckets.FinishObjectBucket(t.Context(), source.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	accounting := st.(state.ObjectStorageAccountingStore)
	if err = accounting.ClaimObjectInventory(t.Context(), source.ID, "source-baseline"); err != nil {
		t.Fatal(err)
	}
	if err = accounting.FinishObjectInventory(t.Context(), source.ID, "source-baseline", api.MinMultipartPartBytes+1, 1); err != nil {
		t.Fatal(err)
	}
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("true")); err != nil {
		t.Fatal(err)
	}
	control := httptest.NewServer(s.handler())
	defer control.Close()
	client := api.NewClient(control.URL, bearer)
	credential, err := client.CreateObjectS3Credential(t.Context(), "encrypted-journal", b.ID, api.CreateObjectS3CredentialRequest{Label: "copy only writer", Permission: api.ObjectBucketPermissionWrite})
	if err != nil {
		t.Fatal(err)
	}
	crossCopyManagementAuthority(t, s, st, acct, b, source, credential.ID)
	grant, err := client.SetObjectS3CopySource(t.Context(), "encrypted-journal", b.ID, credential.ID, source.ID, api.SetObjectS3CopySourceRequest{Prefix: "allowed/"})
	if err != nil || grant.SourceBucketID != source.ID {
		t.Fatal(grant, err)
	}
	replay, err := client.SetObjectS3CopySource(t.Context(), "encrypted-journal", b.ID, credential.ID, source.ID, api.SetObjectS3CopySourceRequest{Prefix: "allowed/"})
	if err != nil || replay != grant {
		t.Fatal("grant retry changed", replay, err)
	}
	list, err := client.ListObjectS3CopySources(t.Context(), "encrypted-journal", b.ID, credential.ID)
	if err != nil || len(list.Items) != 1 {
		t.Fatal(list, err)
	}
	selection := api.ObjectEncryption{Algorithm: "aws:kms", KeyID: backend.Encryption.Keys[0].Reference}
	if _, err = client.PutObjectBucketEncryption(t.Context(), "encrypted-journal", b.ID, selection); err != nil {
		t.Fatal(err)
	}
	if err = s.reconcileObjectBucketEncryption(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	var gateway http.Handler
	public := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gateway.ServeHTTP(w, r) }))
	defer public.Close()
	s.objectStorage.PublicEndpoint = public.URL
	h, err := s3gateway.New(s3gateway.Config{Registry: s.objectStorage, Store: st.(s3gateway.Store), RequestMetrics: st.(state.ObjectStorageProviderUsageStore), SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1, OpenSecret: func(blob []byte) (string, error) {
		ns, plain, e := secretbox.OpenBytes(identity, blob)
		if e != nil || ns != s3gateway.CredentialSecretNamespace {
			return "", objectstorage.ErrConfiguration
		}
		return string(plain), nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	gateway = h
	sdk := awss3.NewFromConfig(aws.Config{Region: s.objectStorage.PublicRegion, Credentials: credentials.NewStaticCredentialsProvider(credential.AccessKeyID, credential.SecretAccessKey, ""), HTTPClient: public.Client()}, func(o *awss3.Options) {
		o.BaseEndpoint = aws.String(public.URL)
		o.UsePathStyle = true
		o.RetryMaxAttempts = 1
	})
	copyObject := func(key, selector string) (*awss3.CopyObjectOutput, error) {
		return sdk.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String(b.Name), Key: aws.String(key), CopySource: aws.String(selector), TaggingDirective: types.TaggingDirectiveReplace, CopySourceIfMatch: aws.String(`"source"`)})
	}
	before, _, _, _ := native.counts()
	for _, selector := range []string{source.ID + "/forbidden/key", uuid.NewString() + "/allowed/source", "copy-origin/allowed/source", b.Name + "/allowed/source"} {
		_, err = copyObject("denied", selector)
		if err == nil {
			t.Fatal("unauthorized copy accepted", selector)
		}
	}
	if _, err = sdk.HeadObject(t.Context(), &awss3.HeadObjectInput{Bucket: aws.String(b.Name), Key: aws.String("allowed/source")}); err == nil {
		t.Fatal("copy grant permitted ordinary read")
	}
	after, _, _, _ := native.counts()
	if before != after {
		t.Fatal("unauthorized selector contacted provider", before, after)
	}
	result, err := copyObject("copied", source.ID+"/allowed/source")
	if err != nil || aws.ToString(result.SSEKMSKeyId) != selection.KeyID {
		t.Fatal(result, err)
	}
	native.mu.Lock()
	id := native.objects["copied"].Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)
	native.mu.Unlock()
	receipt, err := st.(state.ObjectTrackedGatewayCopyStore).GetObjectUploadReceipt(t.Context(), acct.ID, b.AppID, "", credential.ID, id)
	if err != nil || receipt.Status != "completed" || receipt.SourceBucketID != source.ID || receipt.SourceCopyGrantID == "" || receipt.EncryptionDefaultRevision != 1 {
		t.Fatal(receipt, err)
	}
	refs, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(t.Context(), acct.ID, source.ID, []state.ObjectVersionIdentity{{Key: "allowed/source", ProviderVersionID: "private-source-version"}})
	if err != nil {
		t.Fatal(err)
	}
	capacity := st.(state.ObjectCapacityStore)
	job, err := capacity.RequestObjectCapacityReconciliation(t.Context(), acct.ID, source.AppID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = capacity.ClaimObjectCapacityReconciliation(t.Context(), job.ID, "source-versions")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte("allowed/source\x00private-source-version"))
	job, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(t.Context(), job.ID, job.Token, "", []state.ObjectVersionInventoryRecord{{Identity: hex.EncodeToString(hash[:]), Bytes: api.MinMultipartPartBytes + 1}})
	if err != nil || job.State != "completed" {
		t.Fatal(job, err)
	}
	selected, err := copyObject("selected", source.ID+"/allowed/source?versionId="+refs[0].ID)
	if err != nil || aws.ToString(selected.CopySourceVersionId) != refs[0].ID {
		t.Fatal("cross source version lost", selected, err)
	}
	// Multipart part authority is scoped to the source; destination cipher stays captured.
	init, err := sdk.CreateMultipartUpload(t.Context(), &awss3.CreateMultipartUploadInput{Bucket: aws.String(b.Name), Key: aws.String("multipart")})
	if err != nil {
		t.Fatal(err)
	}
	part, err := sdk.UploadPartCopy(t.Context(), &awss3.UploadPartCopyInput{Bucket: aws.String(b.Name), Key: aws.String("multipart"), UploadId: init.UploadId, PartNumber: aws.Int32(1), CopySource: aws.String(source.ID + "/allowed/source?versionId=" + refs[0].ID), CopySourceRange: aws.String("bytes=1-3")})
	if err != nil {
		t.Fatal(err)
	}
	done, err := sdk.CompleteMultipartUpload(t.Context(), &awss3.CompleteMultipartUploadInput{Bucket: aws.String(b.Name), Key: aws.String("multipart"), UploadId: init.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: part.CopyPartResult.ETag}}}})
	if err != nil || aws.ToString(done.SSEKMSKeyId) != selection.KeyID {
		t.Fatal(done, err)
	}
	// A lost part acknowledgment retains the destination capacity and prevents another native part write.
	uncertain, err := sdk.CreateMultipartUpload(t.Context(), &awss3.CreateMultipartUploadInput{Bucket: aws.String(b.Name), Key: aws.String("multipart-lost")})
	if err != nil {
		t.Fatal(err)
	}
	uncertainInput := &awss3.UploadPartCopyInput{Bucket: aws.String(b.Name), Key: aws.String("multipart-lost"), UploadId: uncertain.UploadId, PartNumber: aws.Int32(1), CopySource: aws.String(source.ID + "/allowed/source?versionId=" + refs[0].ID), CopySourceRange: aws.String("bytes=1-3")}
	if _, err = sdk.UploadPartCopy(t.Context(), uncertainInput); err == nil {
		t.Fatal("lost part ACK reported success")
	}
	_, _, _, partAttempts := native.counts()
	if _, err = sdk.UploadPartCopy(t.Context(), uncertainInput); err == nil {
		t.Fatal("uncertain part allowed another attempt")
	}
	_, _, _, partAttemptsAfter := native.counts()
	if partAttemptsAfter != partAttempts {
		t.Fatal("uncertain part replayed native copy")
	}
	if _, err = sdk.AbortMultipartUpload(t.Context(), &awss3.AbortMultipartUploadInput{Bucket: aws.String(b.Name), Key: aws.String("multipart-lost"), UploadId: uncertain.UploadId}); err != nil {
		t.Fatal(err)
	}
	// Lost ACK stays dispatched and recovers from destination proof after revocation.
	_, err = copyObject("lost", source.ID+"/allowed/source")
	if err == nil {
		t.Fatal("lost ACK reported success")
	}
	native.mu.Lock()
	lostID := native.objects["lost"].Get("X-Amz-Meta-" + objectstorage.ReservedUploadReceiptMetadataKey)
	native.mu.Unlock()
	lost, err := st.(state.ObjectTrackedGatewayCopyStore).GetObjectUploadReceipt(t.Context(), acct.ID, b.AppID, "", credential.ID, lostID)
	if err != nil || lost.WritePhase != state.ObjectUploadDispatched {
		t.Fatal(lost, err)
	}
	if err = client.DeleteObjectS3CopySource(t.Context(), "encrypted-journal", b.ID, credential.ID, source.ID); err != nil {
		t.Fatal(err)
	}
	before, heads, copies, parts := native.counts()
	if _, err = copyObject("revoked", source.ID+"/allowed/source"); err == nil {
		t.Fatal("revoked source dispatched")
	}
	after, _, _, _ = native.counts()
	if before != after {
		t.Fatal("revoked grant probed source")
	}
	if pool != nil {
		if _, err = pool.Exec(t.Context(), `UPDATE object_upload_completions SET recovery_retry_at=clock_timestamp() WHERE id=$1`, lostID); err != nil {
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
	recovered, err := st.(state.ObjectTrackedGatewayCopyStore).GetObjectUploadReceipt(t.Context(), acct.ID, b.AppID, "", credential.ID, lostID)
	if err != nil || recovered.Status != "completed" || recovered.SourceCopyGrantID != lost.SourceCopyGrantID {
		t.Fatal("cross copy recovery failed", recovered, err)
	}
	if _, err = client.SetObjectS3CopySource(t.Context(), "encrypted-journal", b.ID, credential.ID, source.ID, api.SetObjectS3CopySourceRequest{Prefix: "allowed/"}); err != nil {
		t.Fatal(err)
	}
	crossCopyPlacementDenial(t, s, st, acct, b, credential.ID, bearer)
	if err = s.runtimeConfig.apply(runtimeConfigS3, json.RawMessage("false")); err != nil {
		t.Fatal(err)
	}
	if err = client.DeleteObjectS3CopySource(t.Context(), "encrypted-journal", b.ID, credential.ID, source.ID); err != nil {
		t.Fatal("disabled ingress blocked source grant cleanup", err)
	}
	_, headsAfter, copiesAfter, partsAfter := native.counts()
	if headsAfter != heads || copiesAfter != copies || partsAfter != parts {
		t.Fatal("recovery replayed source or copy")
	}
}

func crossCopyPlacementDenial(t *testing.T, s *server, st state.Store, acct state.Account, destination state.ObjectBucket, credential, bearer string) {
	t.Helper()
	buckets := st.(state.ObjectBucketStore)
	source, err := buckets.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: destination.AppID, Name: "other-placement", Scope: "default", Region: destination.Region, BackendID: "other-placement", BackendFingerprint: destination.BackendFingerprint, PhysicalName: "other-private"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = buckets.ClaimObjectBucket(t.Context(), acct.ID, destination.AppID, source.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = buckets.FinishObjectBucket(t.Context(), source.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/encrypted-journal/buckets/" + destination.ID + "/s3-credentials/" + credential + "/copy-sources/" + source.ID
	r := httptest.NewRequest("PUT", path, strings.NewReader(`{"prefix":""}`))
	r.Header.Set("Authorization", "Bearer "+bearer)
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	s.handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotImplemented || strings.Contains(w.Body.String(), "other-private") {
		t.Fatal("different placement was not explicitly unsupported", w.Code, w.Body.String())
	}
}

func crossCopyManagementAuthority(t *testing.T, s *server, st state.Store, acct state.Account, destination, source state.ObjectBucket, credential string) {
	t.Helper()
	plain, hash, err := api.GenerateAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	key, err := st.CreateAPIKey(t.Context(), acct.ID, hash, "copy-manager", []string{api.ScopeStorageManage, api.ScopeStorageRead, api.ScopeStorageWrite})
	if err != nil {
		t.Fatal(err)
	}
	access := st.(state.ObjectBucketAccessStore)
	path := "/v1/apps/encrypted-journal/buckets/" + destination.ID + "/s3-credentials/" + credential + "/copy-sources/" + source.ID
	do := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("PUT", path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+plain)
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		s.handler().ServeHTTP(w, r)
		return w
	}
	if r := do(`{"prefix":"allowed/"}`); r.Code != 403 {
		t.Fatal("destination write grant missing", r.Code, r.Body.String())
	}
	if _, err = access.SetObjectBucketAccessGrant(t.Context(), acct.ID, destination.ID, key.ID, state.ObjectBucketPermissionWrite); err != nil {
		t.Fatal(err)
	}
	if r := do(`{"prefix":"allowed/"}`); r.Code != 403 {
		t.Fatal("source read grant missing", r.Code, r.Body.String())
	}
	if _, err = access.SetObjectBucketAccessGrant(t.Context(), acct.ID, source.ID, key.ID, state.ObjectBucketPermissionRead); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"prefix":"x\n"}`, `{"prefix":"ok","ignored":true}`, `{"prefix":"` + strings.Repeat("x", api.MaxObjectCopySourcePrefixBytes+1) + `"}`} {
		if r := do(body); r.Code != 400 {
			t.Fatal("invalid prefix accepted", r.Code, r.Body.String())
		}
	}
	if r := do(`{"prefix":"allowed/"}`); r.Code != 200 || strings.Contains(r.Body.String(), "grant_id") || strings.Contains(r.Body.String(), "physical") {
		t.Fatal(r.Code, r.Body.String())
	}
	// Management itself performs no provider probe and leaks no native identity.
}
