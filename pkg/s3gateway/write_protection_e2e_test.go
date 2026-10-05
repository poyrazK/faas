package s3gateway

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type protectedWriteHTTPObject struct {
	headers       http.Header
	body, version string
}
type protectedWriteHTTP struct {
	mu                         sync.Mutex
	objects, versions, uploads map[string]protectedWriteHTTPObject
	clock                      func() time.Time
	puts                       int
}

func (f *protectedWriteHTTP) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := strings.TrimPrefix(r.URL.Path, "/physical/")
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/xml")
	switch {
	case r.Method == http.MethodGet && q.Has("uploads"):
		_, _ = io.WriteString(w, `<ListMultipartUploadsResult><IsTruncated>false</IsTruncated></ListMultipartUploadsResult>`)
	case r.Method == http.MethodPost && q.Has("uploads"):
		f.uploads[key] = protectedWriteHTTPObject{headers: r.Header.Clone()}
		_, _ = fmt.Fprintf(w, `<InitiateMultipartUploadResult><UploadId>native-%s</UploadId></InitiateMultipartUploadResult>`, key)
	case q.Has("uploadId"):
		obj, ok := f.uploads[key]
		if !ok {
			w.WriteHeader(404)
			_, _ = io.WriteString(w, `<Error><Code>NoSuchUpload</Code></Error>`)
			return
		}
		switch r.Method {
		case http.MethodPut:
			if r.Header.Get("Content-Md5") == "" {
				t.Error("protected part omitted signed checksum")
			}
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			obj.body = string(b)
			f.uploads[key] = obj
			w.Header().Set("ETag", `"part"`)
		case http.MethodGet:
			_, _ = fmt.Fprintf(w, `<ListPartsResult><IsTruncated>false</IsTruncated><Part><PartNumber>1</PartNumber><ETag>&quot;part&quot;</ETag><Size>%d</Size></Part></ListPartsResult>`, len(obj.body))
		case http.MethodPost:
			f.commit(key, &obj)
			delete(f.uploads, key)
			w.Header().Set("X-Amz-Version-Id", obj.version)
			_, _ = io.WriteString(w, `<CompleteMultipartUploadResult><ETag>&quot;stored&quot;</ETag></CompleteMultipartUploadResult>`)
		}
	case r.Method == http.MethodPut:
		f.puts++
		obj := protectedWriteHTTPObject{headers: r.Header.Clone()}
		if copy := r.Header.Get("X-Amz-Copy-Source"); copy != "" {
			source, _ := url.PathUnescape(strings.TrimPrefix(copy, "/"))
			source = strings.TrimPrefix(strings.SplitN(source, "?", 2)[0], "physical/")
			original := f.objects[source]
			obj.body = original.body
			w.Header().Set("X-Amz-Copy-Source-Version-Id", original.version)
		} else {
			b, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
			}
			obj.body = string(b)
			if r.Header.Get("Content-Md5") == "" {
				t.Error("protected broker omitted checksum")
			}
		}
		if obj.headers.Get("X-Amz-Meta-"+objectstorage.ReservedObjectProtectionMetadataKey) == "" {
			t.Error("write omitted policy proof")
		}
		f.commit(key, &obj)
		w.Header().Set("X-Amz-Version-Id", obj.version)
		if key == "lost-ack" {
			return
		}
		w.Header().Set("ETag", `"stored"`)
		if r.Header.Get("X-Amz-Copy-Source") != "" {
			_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;stored&quot;</ETag><LastModified>2026-10-05T00:00:00Z</LastModified></CopyObjectResult>`)
		}
	case r.Method == http.MethodHead:
		obj, ok := f.objects[key]
		if v := q.Get("versionId"); v != "" {
			obj, ok = f.versions[v]
		}
		if !ok {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(obj.body)))
		w.Header().Set("ETag", `"stored"`)
		w.Header().Set("X-Amz-Version-Id", obj.version)
		w.Header().Set("Last-Modified", f.clock().Format(http.TimeFormat))
		for name, values := range obj.headers {
			lower := strings.ToLower(name)
			if strings.HasPrefix(lower, "x-amz-meta-") || strings.HasPrefix(lower, "x-amz-object-lock-") {
				w.Header()[name] = values
			}
		}
	default:
		t.Errorf("unexpected native request: %s %s", r.Method, r.URL)
		w.WriteHeader(500)
	}
}
func (f *protectedWriteHTTP) commit(key string, obj *protectedWriteHTTPObject) {
	obj.version = uuid.NewString()
	if obj.headers.Get("X-Amz-Object-Lock-Mode") == "" {
		obj.headers.Set("X-Amz-Object-Lock-Mode", "COMPLIANCE")
		obj.headers.Set("X-Amz-Object-Lock-Retain-Until-Date", f.clock().AddDate(0, 0, 3).Format(time.RFC3339Nano))
	}
	f.objects[key] = *obj
	f.versions[obj.version] = *obj
}

// adr: 590
func TestWriteProtectionSDKE2EMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC().Add(-17 * time.Minute)
	m.SetClockForTest(func() time.Time { return now })
	writeProtectionSDKE2E(t, m, func(b state.ObjectBucket) {
		if b.ID == "" {
			now = time.Now().UTC()
		} else {
			now = now.Add(16 * time.Minute)
		}
	}, func() time.Time { return now })
}
func TestWriteProtectionSDKE2EPG(t *testing.T) {
	st, pool := multipartCopyPGStore(t)
	writeProtectionSDKE2E(t, st, func(b state.ObjectBucket) {
		if b.ID == "" {
			return
		}
		for _, q := range []string{`UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state IN ('waiting','propagating') THEN clock_timestamp()-interval '1 second' ELSE propagation_until END WHERE bucket_id=$1`, `UPDATE object_upload_completions SET recovery_retry_at=clock_timestamp() WHERE bucket_id=$1 AND status='pending'`} {
			if _, err := pool.Exec(t.Context(), q, b.ID); err != nil {
				t.Fatal(err)
			}
		}
	}, func() time.Time { return time.Now().UTC() })
}
func writeProtectionSDKE2E(t *testing.T, st multipartCopyIntegrationStore, advance func(state.ObjectBucket), clock func() time.Time) {
	native := &protectedWriteHTTP{objects: map[string]protectedWriteHTTPObject{}, versions: map[string]protectedWriteHTTPObject{}, uploads: map[string]protectedWriteHTTPObject{}, clock: clock}
	f := newMultipartCopyIntegrationConfigured(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { native.serve(t, w, r) }), objectstorage.Config{}, 0, nil)
	configure := func(enabled bool) {
		policy := f.handler.registry.Accounting
		reg, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "local"}, Backends: []objectstorage.BackendConfig{{ID: "local", Driver: "s3", Region: "us-east-1", Namespace: "integration", Endpoint: f.nativeEndpoint, AllowHTTP: true, PathStyle: true, S3Region: "us-east-1", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET", ObjectLock: objectstorage.ObjectLockConfig{Enabled: enabled}}}}, func(string) string { return "local-provider-test-credential" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
		if err != nil {
			t.Fatal(err)
		}
		f.handler.registry = reg
	}
	configure(true)
	lock := st.(state.ObjectBucketObjectLockStore)
	days := int32(3)
	cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: &api.ObjectLockDefaultRetention{Mode: "COMPLIANCE", Days: &days}}
	if _, err := lock.RequestObjectBucketObjectLock(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, cfg); err != nil {
		t.Fatal(err)
	}
	prepareObjectLockGatewayVersioning(t, st, f.bucket, func() { advance(f.bucket) })
	j, err := lock.ClaimObjectBucketObjectLock(t.Context(), f.bucket.ID, "lock")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = lock.FinishObjectBucketObjectLock(t.Context(), f.bucket.ID, j.Token, cfg); err != nil {
		t.Fatal(err)
	}
	advance(state.ObjectBucket{})
	if err = st.RecordObjectUsageReport(t.Context(), api.ObjectStorageUsageReport{AccountID: f.bucket.AccountID, BackendID: f.bucket.BackendID, BackendFingerprint: f.bucket.BackendFingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(clock()), ObservedAt: clock()}); err != nil {
		t.Fatal(err)
	}
	until := clock().AddDate(0, 0, 7)
	out, err := f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("source"), Body: strings.NewReader("hello"), ObjectLockMode: types.ObjectLockModeCompliance, ObjectLockRetainUntilDate: &until, ObjectLockLegalHoldStatus: types.ObjectLockLegalHoldStatusOn})
	if err != nil || aws.ToString(out.VersionId) == "" {
		t.Fatal(out, err)
	}
	copied, err := f.client.CopyObject(t.Context(), &awss3.CopyObjectInput{Bucket: aws.String("assets"), Key: aws.String("copy"), CopySource: aws.String("assets/source"), TaggingDirective: types.TaggingDirectiveReplace})
	if err != nil || aws.ToString(copied.VersionId) == "" {
		t.Fatal(copied, err)
	}
	init, err := f.client.CreateMultipartUpload(t.Context(), &awss3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("multipart"), ObjectLockLegalHoldStatus: types.ObjectLockLegalHoldStatusOn})
	if err != nil {
		t.Fatal(err)
	}
	part, err := f.client.UploadPart(t.Context(), &awss3.UploadPartInput{Bucket: aws.String("assets"), Key: aws.String("multipart"), UploadId: init.UploadId, PartNumber: aws.Int32(1), Body: strings.NewReader("hello")})
	if err != nil {
		t.Fatal(err)
	}
	done, err := f.client.CompleteMultipartUpload(t.Context(), &awss3.CompleteMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("multipart"), UploadId: init.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: []types.CompletedPart{{PartNumber: aws.Int32(1), ETag: part.ETag}}}})
	if err != nil || aws.ToString(done.VersionId) == "" {
		t.Fatal(done, err)
	}
	// The native PUT committed but its acknowledgment was lost. No retransmission.
	_, err = f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("lost-ack"), Body: strings.NewReader("hello")})
	assertSDKErrorCode(t, err, "ServiceUnavailable")
	writes := st.(state.ObjectTrackedUploadStore)
	advance(f.bucket)
	rows, err := writes.DueTrackedObjectUploads(t.Context(), api.ObjectUploadRecoveryBatch)
	if err != nil || len(rows) != 1 {
		t.Fatal(rows, err)
	}
	c, err := writes.ClaimTrackedObjectUploadRecovery(t.Context(), rows[0].AccountID, rows[0].BucketID, rows[0].ID, "restart")
	if err != nil {
		t.Fatal(err)
	}
	configure(false)
	backend, err := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := objectstorage.WithObjectWriteProtection(t.Context(), backend.Provider, c.Protection, func(ctx context.Context) error {
		return st.RecordObjectStorageProviderRequest(ctx, f.bucket.ID, clock())
	})
	if err != nil {
		t.Fatal(err)
	}
	proof, err := backend.Provider.(objectstorage.ObjectWriteConfirmer).ConfirmTrackedObject(ctx, f.bucket.PhysicalName, c.Key, c.ID, c.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag, c.ProviderVersionID, c.VerifiedProtection = "completed", proof.ETag, proof.ProviderVersionID, proof.VerifiedProtection
	if _, err = writes.FinishTrackedObjectUploadRecovery(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	if native.puts != 3 {
		t.Fatal("recovery replayed mutation", native.puts)
	}
}
