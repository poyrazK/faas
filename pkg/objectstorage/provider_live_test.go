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
// The test exercises only the Provider interface, so the same qualification
// applies to OVH, AWS, R2, Ceph RGW, or another compatible backend selected by
// the registry's default region. It intentionally does not call the Gregale
// API: API auth/quotas are covered by the existing unit and end-to-end suites.
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
	multipartKey := "qualification/multipart.bin"
	abortKey := "qualification/abort.bin"
	created := false
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute) //nolint:contextcheck // Cleanup must outlive the test context.
		defer cleanupCancel()
		if !created {
			return
		}
		for _, key := range []string{singleKey, multipartKey, abortKey} {
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
	signed, err := backend.Provider.Presign(ctx, bucket, SignRequest{
		Method: "PUT", Key: singleKey, SizeBytes: &singleSize,
		ContentType: "text/plain", ExpiresIn: 300,
	})
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
	assertObjectListed(t, ctx, backend.Provider, bucket, "qualification/", singleKey, int64(len(singlePayload)))

	// Five MiB is the minimum non-final part size required by AWS S3 and is
	// accepted by the other qualified S3-compatible services. The second part
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
