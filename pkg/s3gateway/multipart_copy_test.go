package s3gateway

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 538
func TestMultipartCopyRequestIsolation(t *testing.T) {
	for _, tc := range []struct {
		name, code string
		change     func(*awss3.UploadPartCopyInput)
	}{
		{"foreign source bucket", "NoSuchKey", func(in *awss3.UploadPartCopyInput) { in.CopySource = aws.String("other/source") }},
		{"foreign destination bucket", "NoSuchBucket", func(in *awss3.UploadPartCopyInput) { in.Bucket = aws.String("other") }},
		{"unknown upload", "NoSuchUpload", func(in *awss3.UploadPartCopyInput) { in.UploadId = aws.String(uuid.NewString()) }},
		{"wrong destination key", "NoSuchUpload", func(in *awss3.UploadPartCopyInput) { in.Key = aws.String("other") }},
		{"source version", "NoSuchVersion", func(in *awss3.UploadPartCopyInput) { in.CopySource = aws.String("assets/source?versionId=unowned") }},
		{"reversed range", "InvalidArgument", func(in *awss3.UploadPartCopyInput) { in.CopySourceRange = aws.String("bytes=9-0") }},
		{"suffix range", "InvalidArgument", func(in *awss3.UploadPartCopyInput) { in.CopySourceRange = aws.String("bytes=-9") }},
		{"multiple ranges", "InvalidArgument", func(in *awss3.UploadPartCopyInput) { in.CopySourceRange = aws.String("bytes=0-9,20-29") }},
		{"range past source", "InvalidArgument", func(in *awss3.UploadPartCopyInput) { in.CopySourceRange = aws.String("bytes=5368709121-5368709121") }},
		{"whole source too large", "InvalidArgument", func(in *awss3.UploadPartCopyInput) { in.CopySourceRange = nil }},
		{"configured part ceiling", "InvalidArgument", func(in *awss3.UploadPartCopyInput) { in.CopySourceRange = aws.String("bytes=0-67108864") }},
		{"invalid ETag predicate", "InvalidArgument", func(in *awss3.UploadPartCopyInput) { in.CopySourceIfMatch = aws.String("unquoted") }},
		{"excluded source ETag", "PreconditionFailed", func(in *awss3.UploadPartCopyInput) { in.CopySourceIfNoneMatch = aws.String(`"source"`) }},
		{"time condition", "NotImplemented", func(in *awss3.UploadPartCopyInput) { in.CopySourceIfUnmodifiedSince = aws.Time(time.Now()) }},
		{"source encryption", "NotImplemented", func(in *awss3.UploadPartCopyInput) { in.CopySourceSSECustomerAlgorithm = aws.String("AES256") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMultipartCopyIntegration(t, state.NewMemStore())
			id := f.initiate(t, "destination")
			in := &awss3.UploadPartCopyInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), PartNumber: aws.Int32(1), CopySource: aws.String("assets/source"), CopySourceRange: aws.String("bytes=0-9")}
			tc.change(in)
			_, err := f.client.UploadPartCopy(t.Context(), in)
			assertSDKErrorCode(t, err, tc.code)
			f.provider.mu.Lock()
			copies := f.provider.copies
			f.provider.mu.Unlock()
			if copies != 0 {
				t.Fatal("invalid request dispatched a copy", copies)
			}
			usage, err := f.store.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
			if err != nil || usage.Buckets[0].MultipartBytes != 0 {
				t.Fatal("invalid request reserved part capacity", usage, err)
			}
		})
	}
}

func TestMultipartCopyNeedsReadAndWrite(t *testing.T) {
	for _, permission := range []string{state.ObjectBucketPermissionRead, state.ObjectBucketPermissionWrite} {
		t.Run(permission, func(t *testing.T) {
			f := newMultipartCopyIntegration(t, state.NewMemStore(), permission)
			_, err := f.copyPart(t, "destination", uuid.NewString(), 1, "")
			assertSDKErrorCode(t, err, "AccessDenied")
			f.provider.mu.Lock()
			copies := f.provider.copies
			f.provider.mu.Unlock()
			if copies != 0 {
				t.Fatal("insufficient permission dispatched a copy")
			}
		})
	}
}

func TestMultipartCopySourceRaceDoesNotFenceRetry(t *testing.T) {
	f := newMultipartCopyIntegration(t, state.NewMemStore())
	id := f.initiate(t, "destination")
	f.provider.mu.Lock()
	f.provider.failure = "source_changed"
	f.provider.mu.Unlock()
	_, err := f.copyPart(t, "destination", id, 1, "")
	assertSDKErrorCode(t, err, "PreconditionFailed")
	f.provider.mu.Lock()
	f.provider.failure = ""
	f.provider.mu.Unlock()
	if _, err = f.copyPart(t, "destination", id, 1, ""); err != nil {
		t.Fatal("definite rejection retained transfer fence", err)
	}
	usage, err := f.store.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
	if err != nil || usage.Buckets[0].MultipartBytes != 10 {
		t.Fatal("same part retry accumulated quota", usage, err)
	}
}

func TestCopySourceHeadersMustBeSigned(t *testing.T) {
	h, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
		t.Fatal("unsigned copy reached provider")
		return nil, nil
	})
	r := signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/destination", nil, "UNSIGNED-PAYLOAD")
	r.Header.Set("X-Amz-Copy-Source", "assets/source")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden || !strings.Contains(w.Body.String(), "SignatureDoesNotMatch") {
		t.Fatal(w.Code, w.Body.String())
	}
	// The same holds if a condition is appended to an otherwise signed copy.
	r = signedCopyTestRequest(t)
	r.Header.Set("X-Amz-Copy-Source-If-Match", `"source"`)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestCopyHeadersCannotBeIgnoredByOtherOperations(t *testing.T) {
	for _, tc := range []struct{ method, target string }{
		{http.MethodGet, "https://s3.gregale.dev/assets/destination"},
		{http.MethodPut, "https://s3.gregale.dev/assets/destination?tagging"},
		{http.MethodPut, "https://s3.gregale.dev/assets"},
		{http.MethodPost, "https://s3.gregale.dev/assets/destination?uploads"},
	} {
		t.Run(tc.method+tc.target, func(t *testing.T) {
			h, _, p := newGatewayTestHandler(t, state.ObjectBucketPermissionReadWrite, func(*http.Request) (*http.Response, error) {
				t.Fatal("invalid copy headers reached provider")
				return nil, nil
			})
			r := signedGatewayRequest(t, tc.method, tc.target, nil, "UNSIGNED-PAYLOAD")
			r.Header.Set("X-Amz-Copy-Source", "assets/source")
			r.Header.Set("X-Amz-Copy-Source-If-Match", `"source"`)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != http.StatusNotImplemented || len(p.copyRequests) != 0 || len(p.presignRequests) != 0 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestMultipartCopyRejectsMalformedBodyAndHeaders(t *testing.T) {
	for _, tc := range []struct {
		name, code, body string
		change           func(*http.Request)
	}{
		{"body", "InvalidRequest", "x", nil},
		{"duplicate source", "InvalidRequest", "", func(r *http.Request) { r.Header.Add("X-Amz-Copy-Source", "assets/source") }},
		{"duplicate range", "InvalidArgument", "", func(r *http.Request) { r.Header.Add("X-Amz-Copy-Source-Range", "bytes=0-9") }},
		{"empty range", "InvalidArgument", "", func(r *http.Request) { r.Header.Set("X-Amz-Copy-Source-Range", "") }},
		{"duplicate predicate", "InvalidArgument", "", func(r *http.Request) { r.Header.Add("X-Amz-Copy-Source-If-Match", `"source"`) }},
		{"per-part metadata directive", "NotImplemented", "", func(r *http.Request) { r.Header.Set("X-Amz-Metadata-Directive", "REPLACE") }},
		{"bad empty-body digest", "XAmzContentSHA256Mismatch", "", func(r *http.Request) { r.Header.Set("X-Amz-Content-Sha256", strings.Repeat("0", 64)) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMultipartCopyIntegration(t, state.NewMemStore())
			id := f.initiate(t, "destination")
			r := httptest.NewRequest(http.MethodPut, "http://"+f.handler.host+"/assets/destination?uploadId="+id+"&partNumber=1", bytes.NewBufferString(tc.body))
			r.Header.Set("X-Amz-Copy-Source", "assets/source")
			r.Header.Set("X-Amz-Copy-Source-If-Match", `"source"`)
			r.Header.Set("X-Amz-Copy-Source-Range", "bytes=0-9")
			r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
			if tc.change != nil {
				tc.change(r)
			}
			if err := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(t.Context(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, r, r.Header.Get("X-Amz-Content-Sha256"), "s3", "us-east-1", f.handler.now()); err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			f.handler.ServeHTTP(w, r)
			if w.Code >= 300 && strings.Contains(w.Body.String(), "<Code>"+tc.code+"</Code>") {
				return
			}
			t.Fatal(w.Code, w.Body.String())
		})
	}
}
