package objectstorage

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 536
func TestS3TrackedCopy(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		rejected   bool
		want       error
	}{
		{"success", `<CopyObjectResult><ETag>&quot;copied&quot;</ETag></CopyObjectResult>`, 200, false, nil},
		{"source changed", `<Error><Code>PreconditionFailed</Code></Error>`, 412, true, ErrPreconditionFailed},
		{"source removed", `<Error><Code>NoSuchKey</Code></Error>`, 404, true, ErrNotFound},
		{"timeout", `<Error><Code>RequestTimeout</Code></Error>`, 408, false, ErrUnavailable},
		{"server failure", `<Error><Code>InternalError</Code></Error>`, 503, false, ErrUnavailable},
		{"embedded error", `<Error><Code>InternalError</Code><Message>private detail</Message></Error>`, 200, false, ErrUnavailable},
		{"embedded rejection", `<Error><Code>AccessDenied</Code></Error>`, 200, false, ErrUnavailable},
		{"missing etag", `<CopyObjectResult/>`, 200, false, ErrUnavailable},
		{"oversized etag", `<CopyObjectResult><ETag>` + strings.Repeat("x", api.MaxObjectWriteETagBytes+1) + `</ETag></CopyObjectResult>`, 200, false, ErrUnavailable},
		{"truncated response", `<CopyObjectResult><ETag>`, 200, false, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var copies atomic.Int32
			id := uuid.NewString()
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == http.MethodHead {
					w.Header().Set("Content-Length", "5")
					w.Header().Set("ETag", `"source"`)
					w.Header().Set("Content-Type", "image/png")
					w.Header().Set("Cache-Control", "max-age=60")
					w.Header().Set("Content-Disposition", "inline")
					w.Header().Set("Content-Encoding", "gzip")
					w.Header().Set("Content-Language", "en")
					w.Header().Set("Expires", "Mon, 07 Sep 2026 12:00:00 GMT")
					w.Header().Set("X-Amz-Meta-Owner", "customer")
					w.Header().Set("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey, "old-receipt")
					w.Header().Set("X-Amz-Meta-"+ReservedMultipartSessionMetadataKey, "old-session")
					w.Header().Set("X-Amz-Meta-"+ReservedObjectTagsMetadataKey, "private-tag-state")
					return
				}
				copies.Add(1)
				if r.Header.Get("X-Amz-Copy-Source-If-Match") != `"source"` || r.Header.Get("X-Amz-Metadata-Directive") != "REPLACE" || r.Header.Get("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey) != id || r.Header.Get("X-Amz-Meta-Owner") != "customer" || r.Header.Get("X-Amz-Meta-"+ReservedMultipartSessionMetadataKey) != "" {
					t.Error("copy lost source condition, metadata or fresh receipt")
				}
				if r.Header.Get("Content-Type") != "image/png" || r.Header.Get("Cache-Control") != "max-age=60" || r.Header.Get("Expires") != "Mon, 07 Sep 2026 12:00:00 GMT" || r.Header.Get("X-Amz-Tagging-Directive") != "COPY" || r.Header.Get("Content-Disposition") != "inline" || r.Header.Get("Content-Encoding") != "gzip" || r.Header.Get("Content-Language") != "en" || r.Header.Get("X-Amz-Meta-"+ReservedObjectTagsMetadataKey) != "" {
					t.Error("COPY lost source HTTP metadata or tags")
				}
				w.Header().Set("Content-Type", "application/xml")
				w.WriteHeader(tc.status)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer upstream.Close()
			config := testBackend()
			config.Endpoint = upstream.URL
			p, err := NewS3(config, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			copier := p.(TrackedObjectCopier)
			source, err := copier.SnapshotCopySource(t.Context(), "bucket", "source")
			if err != nil || source.SizeBytes != 5 || source.Metadata.Metadata[ReservedUploadReceiptMetadataKey] != "" {
				t.Fatal(source, err)
			}
			result, err := copier.CopyTrackedObject(t.Context(), "bucket", id, CopyObjectRequest{SourceKey: "source", DestinationKey: "destination"}, source)
			if !errors.Is(err, tc.want) || errors.Is(err, ErrWriteRejected) != tc.rejected || copies.Load() != 1 {
				t.Fatal(result, err, copies.Load())
			}
			if tc.want == nil && result.ETag != `"copied"` {
				t.Fatal(result)
			}
			if source.Metadata.Metadata[ReservedUploadReceiptMetadataKey] != "" {
				t.Fatal("mutated source snapshot")
			}
		})
	}
}

func TestS3TrackedCopyReplaceAndValidation(t *testing.T) {
	var copies atomic.Int32
	id := uuid.NewString()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		copies.Add(1)
		if r.Header.Get("X-Amz-Meta-Owner") != "replacement" || r.Header.Get("Content-Type") != "text/plain" || r.Header.Get("X-Amz-Tagging") != "team=customer" || r.Header.Get("X-Amz-Tagging-Directive") != "REPLACE" || r.Header.Get("X-Amz-Meta-"+ReservedUploadReceiptMetadataKey) != id {
			t.Error("replacement metadata/tags lost")
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
	copier := p.(TrackedObjectCopier)
	source := CopySourceSnapshot{ETag: `"source"`, SizeBytes: 5, Metadata: ObjectMetadata{ContentType: "image/png", Metadata: map[string]string{"owner": "original"}}}
	r := CopyObjectRequest{SourceKey: "source", DestinationKey: "destination", MetadataDirective: "REPLACE", TaggingDirective: "REPLACE", Metadata: ObjectMetadata{ContentType: "text/plain", Metadata: map[string]string{"owner": "replacement"}, Tags: map[string]string{"team": "customer"}}}
	if _, err = copier.CopyTrackedObject(t.Context(), "bucket", id, r, source); err != nil {
		t.Fatal(err)
	}
	r.Metadata.Metadata[ReservedUploadReceiptMetadataKey] = "forged"
	if _, err = copier.CopyTrackedObject(t.Context(), "bucket", id, r, source); !errors.Is(err, ErrWriteRejected) {
		t.Fatal("marker forgery", err)
	}
	delete(r.Metadata.Metadata, ReservedUploadReceiptMetadataKey)
	source.SizeBytes = api.MaxObjectSinglePutBytes + 1
	if _, err = copier.CopyTrackedObject(t.Context(), "bucket", id, r, source); !errors.Is(err, ErrWriteRejected) || copies.Load() != 1 {
		t.Fatal("oversized source dispatched", err, copies.Load())
	}
}

func TestS3CopySourceRequiresProof(t *testing.T) {
	for _, tc := range []struct{ name, size, etag, version string }{
		{"missing size", "", `"source"`, ""}, {"missing etag", "0", "", ""}, {"weak etag", "0", `W/"source"`, ""}, {"unquoted etag", "0", "source", ""}, {"wildcard etag", "0", "*", ""}, {"invalid native version", "5", `"source"`, strings.Repeat("v", api.ObjectProviderVersionIDMaxBytes+1)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.size != "" {
					w.Header().Set("Content-Length", tc.size)
				}
				w.Header().Set("ETag", tc.etag)
				w.Header().Set("X-Amz-Version-Id", tc.version)
			}))
			defer upstream.Close()
			config := testBackend()
			config.Endpoint = upstream.URL
			p, err := NewS3(config, testCredentials)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = p.(TrackedObjectCopier).SnapshotCopySource(t.Context(), "bucket", "source"); err == nil {
				t.Fatal("accepted unproven source")
			}
		})
	}
}
