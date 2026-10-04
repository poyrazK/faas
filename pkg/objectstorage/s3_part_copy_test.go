package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 538
func TestS3MultipartCopyOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		rejected   bool
		want       error
	}{
		{"success", `<CopyPartResult><ETag>&quot;part&quot;</ETag><LastModified>2026-10-02T10:00:00Z</LastModified></CopyPartResult>`, 200, false, nil},
		{"source changed", `<Error><Code>PreconditionFailed</Code></Error>`, 412, true, ErrPreconditionFailed},
		{"source missing", `<Error><Code>NoSuchKey</Code></Error>`, 404, true, ErrNotFound},
		{"invalid range", `<Error><Code>InvalidArgument</Code></Error>`, 400, true, ErrInvalid},
		{"timeout", `<Error><Code>RequestTimeout</Code></Error>`, 408, false, ErrUnavailable},
		{"server failure", `<Error><Code>InternalError</Code></Error>`, 503, false, ErrUnavailable},
		{"embedded error", `<Error><Code>InternalError</Code><Message>provider-secret</Message></Error>`, 200, false, ErrUnavailable},
		{"truncated body", `<CopyPartResult><ETag>`, 200, false, ErrUnavailable},
		{"missing etag", `<CopyPartResult/>`, 200, false, ErrUnavailable},
		{"oversized etag", `<CopyPartResult><ETag>` + strings.Repeat("x", api.MaxObjectWriteETagBytes+1) + `</ETag></CopyPartResult>`, 200, false, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var copies atomic.Int32
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.Header().Set("Content-Length", strconv.FormatInt(api.MaxObjectSinglePutBytes+1, 10))
					w.Header().Set("ETag", `"source"`)
					return
				}
				copies.Add(1)
				source, _ := url.PathUnescape(r.Header.Get("X-Amz-Copy-Source"))
				if source != "physical/folder/hello 世界.txt" || r.URL.Query().Get("uploadId") != "private-upload" || r.URL.Query().Get("partNumber") != "7" || r.Header.Get("X-Amz-Copy-Source-If-Match") != `"source"` || r.Header.Get("X-Amz-Copy-Source-If-None-Match") != `"other"` || r.Header.Get("X-Amz-Copy-Source-Range") != "bytes=1-10" {
					t.Error("provider part copy lost source identity, range, conditions or private upload ID")
				}
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			config := testBackend()
			config.Endpoint = upstream.URL
			provider, err := NewS3(config, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			copier := provider.(MultipartPartCopier)
			source, err := copier.SnapshotMultipartCopySource(t.Context(), "physical", "folder/hello 世界.txt")
			if err != nil {
				t.Fatal(err)
			}
			result, err := copier.CopyMultipartPart(t.Context(), "physical", MultipartPartCopyRequest{SourceKey: "folder/hello 世界.txt", Key: "destination", ProviderUploadID: "private-upload", PartNumber: 7, Range: &CopySourceRange{First: 1, Last: 10}, Conditions: CopySourceConditions{IfNoneMatch: `"other"`}}, source)
			if !errors.Is(err, tc.want) || errors.Is(err, ErrWriteRejected) != tc.rejected || copies.Load() != 1 {
				t.Fatal(result, err, copies.Load())
			}
			if err != nil && strings.Contains(err.Error(), "provider-secret") {
				t.Fatal("provider error leaked")
			}
			if tc.want == nil && (result.ETag != `"part"` || result.LastModified.IsZero()) {
				t.Fatal(result)
			}
		})
	}
}

func TestS3ConditionalTrackedCopy(t *testing.T) {
	var copies atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		copies.Add(1)
		if r.Header.Get("X-Amz-Copy-Source-If-Match") != `"source"` || r.Header.Get("X-Amz-Copy-Source-If-None-Match") != `"other"` {
			t.Error("atomic source conditions missing")
		}
		_, _ = io.WriteString(w, `<CopyObjectResult><ETag>&quot;copied&quot;</ETag></CopyObjectResult>`)
	}))
	defer upstream.Close()
	config := testBackend()
	config.Endpoint = upstream.URL
	p, err := NewS3(config, testCredentials)
	if err != nil {
		t.Fatal(err)
	}
	copier := p.(ConditionalTrackedObjectCopier)
	source := CopySourceSnapshot{ETag: `"source"`, SizeBytes: 5}
	r := CopyObjectRequest{SourceKey: "source", DestinationKey: "destination"}
	if _, err = copier.CopyConditionalTrackedObject(t.Context(), "physical", uuid.NewString(), r, source, CopySourceConditions{IfMatch: `"source"`, IfNoneMatch: `"other"`}); err != nil {
		t.Fatal(err)
	}
	if _, err = copier.CopyConditionalTrackedObject(t.Context(), "physical", uuid.NewString(), r, source, CopySourceConditions{IfNoneMatch: `"source"`}); !errors.Is(err, ErrPreconditionFailed) || !errors.Is(err, ErrWriteRejected) || copies.Load() != 1 {
		t.Fatal(err, copies.Load())
	}
}
