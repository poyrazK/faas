package objectstorage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestLiveProviderQualification is deliberately opt-in because it creates,
// writes, and deletes upstream object-storage data. Run it only with a
// dedicated provider account/project and an isolated object-storage config:
//
//	FAAS_OBJECT_STORAGE_LIVE_TEST=1 \
//	FAAS_OBJECT_STORAGE_CONFIG=/etc/faas/object-storage-qualification.json \
//	go test ./pkg/objectstorage -run '^TestLiveProviderQualification$' -count=1 -v
//
// The test exercises the Provider interface and its optional capabilities, so
// the same qualification applies to GCS, OVH, AWS, R2, Ceph RGW, or another
// backend selected by the registry's default region. It intentionally does
// not call the Gregale API: API auth/quotas are covered by the existing unit
// and end-to-end suites.
func TestLiveProviderQualification(t *testing.T) {
	if os.Getenv("FAAS_OBJECT_STORAGE_LIVE_TEST") != "1" {
		t.Skip("set FAAS_OBJECT_STORAGE_LIVE_TEST=1 to run the object-storage provider qualification")
	}
	if os.Getenv("FAAS_OBJECT_STORAGE_CONFIG") == "" {
		t.Fatal("FAAS_OBJECT_STORAGE_CONFIG is required for the object-storage provider qualification")
	}

	registry, err := Load(os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	if registry == nil {
		t.Fatal("object-storage registry is not configured")
	}
	region := os.Getenv("FAAS_OBJECT_STORAGE_TEST_REGION")
	if region == "" {
		region = registry.DefaultRegion
	}
	backend, err := registry.Default(region)
	if err != nil {
		t.Fatalf("resolve qualification backend for region %q: %v", region, err)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	bucket := "gregale-qual-" + strings.ReplaceAll(uuid.NewString(), "-", "")
	singleKey := "qualification/hello +世界.txt"
	copyKey := "qualification/copies/preserved.txt"
	replacedCopyKey := "qualification/copies/replaced.txt"
	multipartKey := "qualification/multipart.bin"
	abortKey := "qualification/abort.bin"
	created := false
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute) //nolint:contextcheck // Cleanup must outlive the test context.
		defer cleanupCancel()
		if !created {
			return
		}
		for _, key := range []string{singleKey, copyKey, replacedCopyKey, multipartKey, abortKey} {
			if err := backend.Provider.DeleteObject(cleanupCtx, bucket, key); err != nil && !errors.Is(err, ErrNotFound) {
				t.Errorf("cleanup object %q: %v", key, err)
			}
		}
		if err := backend.Provider.DeleteBucket(cleanupCtx, bucket); err != nil {
			t.Errorf("cleanup bucket %q: %v", bucket, err)
		}
	})

	if err := backend.Provider.CreateBucket(ctx, bucket); err != nil {
		t.Fatalf("create qualification bucket: %v", err)
	}
	created = true

	singlePayload := []byte("gregale object-storage provider qualification\n")
	singleSize := int64(len(singlePayload))
	singleMetadata := SignRequest{
		Method: http.MethodPut, Key: singleKey, SizeBytes: &singleSize, ContentType: "text/plain",
		CacheControl: "max-age=60", ContentDisposition: `attachment; filename="qualification.txt"`,
		ContentEncoding: "gzip", ContentLanguage: "en-US",
		Metadata:  map[string]string{"owner": "qualification"},
		Tags:      map[string]string{"env": "test", "purpose": "qualification"},
		ExpiresIn: 300,
	}
	signed, err := backend.Provider.Presign(ctx, bucket, singleMetadata)
	if err != nil {
		t.Fatalf("sign single PUT: %v", err)
	}
	if err := putSigned(ctx, signed, singlePayload, "text/plain"); err != nil {
		t.Fatalf("single PUT: %v", err)
	}

	signed, err = backend.Provider.Presign(ctx, bucket, SignRequest{Method: "GET", Key: singleKey, ExpiresIn: 300})
	if err != nil {
		t.Fatalf("sign single GET: %v", err)
	}
	got, err := getSigned(ctx, signed)
	if err != nil {
		t.Fatalf("single GET: %v", err)
	}
	if !bytes.Equal(got, singlePayload) {
		t.Fatalf("single GET body mismatch: got %d bytes, want %d", len(got), len(singlePayload))
	}
	head, err := backend.Provider.Presign(ctx, bucket, SignRequest{Method: http.MethodHead, Key: singleKey, ExpiresIn: 300})
	if err != nil {
		t.Fatalf("sign metadata HEAD: %v", err)
	}
	headHeaders, err := headSigned(ctx, head)
	if err != nil {
		t.Fatalf("metadata HEAD: %v", err)
	}
	assertObjectMetadata(t, headHeaders, singleMetadata)

	tagger, ok := backend.Provider.(ObjectTagger)
	if !ok {
		t.Fatal("qualified provider does not implement object tags")
	}
	tags, err := tagger.GetObjectTags(ctx, bucket, singleKey)
	if err != nil {
		t.Fatalf("get signed-upload tags: %v", err)
	}
	assertTags(t, tags, singleMetadata.Tags)
	updatedTags := map[string]string{"env": "staging", "purpose": "qualification"}
	if err := tagger.PutObjectTags(ctx, bucket, singleKey, updatedTags); err != nil {
		t.Fatalf("replace object tags: %v", err)
	}
	tags, err = tagger.GetObjectTags(ctx, bucket, singleKey)
	if err != nil {
		t.Fatalf("get replaced object tags: %v", err)
	}
	assertTags(t, tags, updatedTags)
	if err := tagger.DeleteObjectTags(ctx, bucket, singleKey); err != nil {
		t.Fatalf("delete object tags: %v", err)
	}
	tags, err = tagger.GetObjectTags(ctx, bucket, singleKey)
	if err != nil {
		t.Fatalf("get deleted object tags: %v", err)
	}
	assertTags(t, tags, nil)
	if err := tagger.PutObjectTags(ctx, bucket, singleKey, singleMetadata.Tags); err != nil {
		t.Fatalf("restore object tags: %v", err)
	}

	copier, ok := backend.Provider.(ObjectCopier)
	if !ok {
		t.Fatal("qualified provider does not implement object copy")
	}
	if _, err := copier.CopyObject(ctx, bucket, CopyObjectRequest{
		SourceKey: singleKey, DestinationKey: copyKey, MetadataDirective: "COPY", TaggingDirective: "COPY",
	}); err != nil {
		t.Fatalf("copy object with preserved metadata: %v", err)
	}
	assertSignedObject(t, ctx, backend.Provider, bucket, copyKey, singlePayload)
	copiedHead, err := backend.Provider.Presign(ctx, bucket, SignRequest{Method: http.MethodHead, Key: copyKey, ExpiresIn: 300})
	if err != nil {
		t.Fatalf("sign copied metadata HEAD: %v", err)
	}
	copiedHeaders, err := headSigned(ctx, copiedHead)
	if err != nil {
		t.Fatalf("copied metadata HEAD: %v", err)
	}
	assertObjectMetadata(t, copiedHeaders, singleMetadata)
	copiedTags, err := tagger.GetObjectTags(ctx, bucket, copyKey)
	if err != nil {
		t.Fatalf("get copied object tags: %v", err)
	}
	assertTags(t, copiedTags, singleMetadata.Tags)

	replacement := SignRequest{
		Method: http.MethodPut, Key: replacedCopyKey, SizeBytes: &singleSize, ContentType: "application/json",
		CacheControl: "no-store", ContentDisposition: `inline; filename="replacement.json"`,
		ContentEncoding: "gzip", ContentLanguage: "fr-FR",
		Metadata: map[string]string{"owner": "replacement"},
		Tags:     map[string]string{"env": "prod", "purpose": "replacement"},
	}
	if _, err := copier.CopyObject(ctx, bucket, CopyObjectRequest{
		SourceKey: singleKey, DestinationKey: replacedCopyKey, MetadataDirective: "REPLACE", TaggingDirective: "REPLACE",
		Metadata: ObjectMetadata{
			CacheControl: replacement.CacheControl, ContentDisposition: replacement.ContentDisposition,
			ContentEncoding: replacement.ContentEncoding, ContentLanguage: replacement.ContentLanguage,
			ContentType: replacement.ContentType, Metadata: replacement.Metadata, Tags: replacement.Tags,
		},
	}); err != nil {
		t.Fatalf("copy object with replaced metadata: %v", err)
	}
	assertSignedObject(t, ctx, backend.Provider, bucket, replacedCopyKey, singlePayload)
	replacedHead, err := backend.Provider.Presign(ctx, bucket, SignRequest{Method: http.MethodHead, Key: replacedCopyKey, ExpiresIn: 300})
	if err != nil {
		t.Fatalf("sign replaced metadata HEAD: %v", err)
	}
	replacedHeaders, err := headSigned(ctx, replacedHead)
	if err != nil {
		t.Fatalf("replaced metadata HEAD: %v", err)
	}
	assertObjectMetadata(t, replacedHeaders, replacement)
	replacedTags, err := tagger.GetObjectTags(ctx, bucket, replacedCopyKey)
	if err != nil {
		t.Fatalf("get replaced object tags: %v", err)
	}
	assertTags(t, replacedTags, replacement.Tags)

	lister, ok := backend.Provider.(DelimitedObjectLister)
	if !ok {
		t.Fatal("qualified provider does not implement delimiter listing")
	}
	delimited, err := lister.ListObjectsDelimited(ctx, bucket, "qualification/", "/", "", 100)
	if err != nil {
		t.Fatalf("list objects with delimiter: %v", err)
	}
	if !containsString(delimited.CommonPrefixes, "qualification/copies/") {
		t.Fatalf("delimiter listing did not return copy prefix: %+v", delimited)
	}
	assertObjectListed(t, ctx, backend.Provider, bucket, "qualification/", singleKey, int64(len(singlePayload)))

	// Five MiB is the minimum non-final part size required by the S3 and GCS XML
	// multipart protocols. The second part
	// is deliberately small so this remains a cheap qualification run.
	partOne := bytes.Repeat([]byte("a"), 5<<20)
	partTwo := []byte("last-part")
	multipartSize := int64(len(partOne) + len(partTwo))
	sessionID := uuid.NewString()
	providerUploadID, err := backend.Provider.EnsureMultipartUpload(ctx, bucket, MultipartCreateRequest{
		SessionID: sessionID, Key: multipartKey, SizeBytes: multipartSize, ContentType: "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("initiate multipart upload: %v", err)
	}
	recoveredUploadID, err := backend.Provider.EnsureMultipartUpload(ctx, bucket, MultipartCreateRequest{
		SessionID: sessionID, Key: multipartKey, SizeBytes: multipartSize, ContentType: "application/octet-stream",
	})
	if err != nil || recoveredUploadID != providerUploadID {
		t.Fatalf("recover multipart upload: id=%q err=%v", recoveredUploadID, err)
	}

	etagOne := uploadPart(t, ctx, backend.Provider, bucket, multipartKey, providerUploadID, 1, partOne)
	etagTwo := uploadPart(t, ctx, backend.Provider, bucket, multipartKey, providerUploadID, 2, partTwo)
	page, err := backend.Provider.ListMultipartParts(ctx, bucket, MultipartListPartsRequest{
		Key: multipartKey, ProviderUploadID: providerUploadID, PartNumberMarker: 0, Limit: 1,
	})
	if err != nil {
		t.Fatalf("list first multipart page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].PartNumber != 1 || page.Items[0].SizeBytes != int64(len(partOne)) || page.NextPartNumberMarker != 1 {
		t.Fatalf("unexpected first multipart page: %+v", page)
	}
	page, err = backend.Provider.ListMultipartParts(ctx, bucket, MultipartListPartsRequest{
		Key: multipartKey, ProviderUploadID: providerUploadID, PartNumberMarker: page.NextPartNumberMarker, Limit: 1,
	})
	if err != nil {
		t.Fatalf("list second multipart page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].PartNumber != 2 || page.Items[0].SizeBytes != int64(len(partTwo)) || page.NextPartNumberMarker != 0 {
		t.Fatalf("unexpected second multipart page: %+v", page)
	}

	if err := backend.Provider.CompleteMultipartUpload(ctx, bucket, MultipartCompleteRequest{
		SessionID: sessionID, Key: multipartKey, ProviderUploadID: providerUploadID, SizeBytes: multipartSize,
		Parts: []CompletedPart{{PartNumber: 1, ETag: etagOne}, {PartNumber: 2, ETag: etagTwo}},
	}); err != nil {
		t.Fatalf("complete multipart upload: %v", err)
	}
	signed, err = backend.Provider.Presign(ctx, bucket, SignRequest{Method: "GET", Key: multipartKey, ExpiresIn: 300})
	if err != nil {
		t.Fatalf("sign multipart GET: %v", err)
	}
	got, err = getSigned(ctx, signed)
	if err != nil {
		t.Fatalf("multipart GET: %v", err)
	}
	want := append(append([]byte(nil), partOne...), partTwo...)
	if !bytes.Equal(got, want) {
		t.Fatalf("multipart GET body mismatch: got %d bytes, want %d", len(got), len(want))
	}

	abortID, err := backend.Provider.EnsureMultipartUpload(ctx, bucket, MultipartCreateRequest{
		SessionID: uuid.NewString(), Key: abortKey, SizeBytes: int64(len(partOne)), ContentType: "application/octet-stream",
	})
	if err != nil {
		t.Fatalf("initiate abort upload: %v", err)
	}
	if err := backend.Provider.AbortMultipartUpload(ctx, bucket, MultipartAbortRequest{Key: abortKey, ProviderUploadID: abortID}); err != nil {
		t.Fatalf("abort multipart upload: %v", err)
	}
	if err := backend.Provider.AbortMultipartUpload(ctx, bucket, MultipartAbortRequest{Key: abortKey, ProviderUploadID: abortID}); err != nil {
		t.Fatalf("repeat abort multipart upload: %v", err)
	}

	t.Logf("qualified backend %s (%s) in region %s", backend.ID, backend.Fingerprint[:12], region)
}

func putSigned(ctx context.Context, signed SignedRequest, body []byte, contentType string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, signed.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for name, value := range signed.Headers {
		if !strings.EqualFold(name, "Content-Length") {
			req.Header.Set(name, value)
		}
	}
	req.Header.Set("Content-Type", contentType)
	req.ContentLength = int64(len(body))
	resp, err := qualificationHTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	return nil
}

func getSigned(ctx context.Context, signed SignedRequest) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, signed.URL, nil)
	if err != nil {
		return nil, err
	}
	for name, value := range signed.Headers {
		if !strings.EqualFold(name, "Content-Length") {
			req.Header.Set(name, value)
		}
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := qualificationHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

func headSigned(ctx context.Context, signed SignedRequest) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, signed.URL, nil)
	if err != nil {
		return nil, err
	}
	for name, value := range signed.Headers {
		if !strings.EqualFold(name, "Content-Length") {
			req.Header.Set(name, value)
		}
	}
	req.Header.Set("Accept-Encoding", "identity")
	resp, err := qualificationHTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("provider returned HTTP %d", resp.StatusCode)
	}
	return resp.Header, nil
}

func assertSignedObject(t *testing.T, ctx context.Context, provider Provider, bucket, key string, want []byte) {
	t.Helper()
	signed, err := provider.Presign(ctx, bucket, SignRequest{Method: http.MethodGet, Key: key, ExpiresIn: 300})
	if err != nil {
		t.Fatalf("sign GET for %q: %v", key, err)
	}
	got, err := getSigned(ctx, signed)
	if err != nil {
		t.Fatalf("GET %q: %v", key, err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("GET %q body mismatch: got %d bytes, want %d", key, len(got), len(want))
	}
}

func assertObjectMetadata(t *testing.T, headers http.Header, expected SignRequest) {
	t.Helper()
	for name, want := range map[string]string{
		"Content-Type":        expected.ContentType,
		"Cache-Control":       expected.CacheControl,
		"Content-Disposition": expected.ContentDisposition,
		"Content-Encoding":    expected.ContentEncoding,
		"Content-Language":    expected.ContentLanguage,
	} {
		if got := headers.Get(name); got != want {
			t.Fatalf("HEAD %s = %q, want %q", name, got, want)
		}
	}
	for key, want := range expected.Metadata {
		got := headers.Get("x-amz-meta-" + key)
		if got == "" {
			got = headers.Get("x-goog-meta-" + key)
		}
		if got != want {
			t.Fatalf("HEAD metadata %q = %q, want %q (headers=%v)", key, got, want, headers)
		}
	}
}

func assertTags(t *testing.T, got, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("tags = %#v, want %#v", got, want)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("tags = %#v, want %#v", got, want)
		}
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

var qualificationHTTPClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

func uploadPart(t *testing.T, ctx context.Context, provider Provider, bucket, key, uploadID string, partNumber int32, body []byte) string {
	t.Helper()
	signed, err := provider.PresignMultipartPart(ctx, bucket, MultipartPartRequest{
		Key: key, ProviderUploadID: uploadID, PartNumber: partNumber, SizeBytes: int64(len(body)), ExpiresIn: 300,
	})
	if err != nil {
		t.Fatalf("sign multipart part %d: %v", partNumber, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, signed.URL, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build multipart part %d request: %v", partNumber, err)
	}
	for name, value := range signed.Headers {
		if !strings.EqualFold(name, "Content-Length") {
			req.Header.Set(name, value)
		}
	}
	req.ContentLength = int64(len(body))
	resp, err := qualificationHTTPClient.Do(req)
	if err != nil {
		t.Fatalf("upload multipart part %d: %v", partNumber, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		t.Fatalf("upload multipart part %d: provider returned HTTP %d", partNumber, resp.StatusCode)
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		t.Fatalf("upload multipart part %d: provider omitted ETag", partNumber)
	}
	return etag
}

func assertObjectListed(t *testing.T, ctx context.Context, provider Provider, bucket, prefix, key string, size int64) {
	t.Helper()
	page, err := provider.ListObjects(ctx, bucket, prefix, "", 100)
	if err != nil {
		t.Fatalf("list objects: %v", err)
	}
	for _, object := range page.Items {
		if object.Key == key {
			if object.Size != size {
				t.Fatalf("listed object %q has size %d, want %d", key, object.Size, size)
			}
			return
		}
	}
	t.Fatalf("listed objects did not contain %q", key)
}
