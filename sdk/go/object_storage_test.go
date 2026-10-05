package faas_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestControlMultipartClient(t *testing.T) {
	const ownedKey = "arn:gregale:kms:us-east-1:11111111-1111-4111-8111-111111111111:key/22222222-2222-4222-8222-222222222222"
	var requests atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing management authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/buckets/bucket/multipart-uploads":
			var in faas.CreateObjectMultipartUploadRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Encryption == nil || in.Encryption.KeyID != ownedKey {
				t.Error("owned encryption was not serialized")
			}
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/buckets/bucket/multipart-uploads/session/parts/1/signed-url":
			_, _ = io.WriteString(w, `{"url":"https://s3.example.test/assets/file?uploadId=session&partNumber=1","method":"PUT","headers":{"Content-Length":"3"},"expires_at":"2026-10-04T00:00:00Z"}`)
			return
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/demo/buckets/bucket/multipart-uploads/session/parts":
			_, _ = io.WriteString(w, `{"items":[{"part_number":1,"etag":"part","size_bytes":3,"last_modified":"2026-10-04T00:00:00Z"}]}`)
			return
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/demo/buckets/bucket/multipart-uploads/session/complete":
			var in faas.CompleteObjectMultipartUploadRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || len(in.Parts) != 1 || in.Parts[0].ETag != "part" {
				t.Error("completion parts were not serialized")
			}
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			t.Errorf("unexpected multipart route %s %s", r.Method, r.URL)
		}
		_, _ = io.WriteString(w, `{"id":"session","key":"file","state":"active","encryption":{"algorithm":"aws:kms","key_id":"`+ownedKey+`"}}`)
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	u, err := c.CreateObjectMultipartUpload(ctx, "demo", "bucket", faas.CreateObjectMultipartUploadRequest{Key: "file", SizeBytes: 3, Encryption: &faas.ObjectEncryption{Algorithm: "aws:kms", KeyID: ownedKey}})
	if err != nil || u.Encryption == nil || u.Encryption.KeyID != ownedKey {
		t.Fatal(u, err)
	}
	signed, err := c.SignObjectMultipartPart(ctx, "demo", "bucket", u.ID, 1, faas.ObjectMultipartPartSignRequest{})
	if err != nil || signed.Headers["Content-Length"] != "3" {
		t.Fatal(signed, err)
	}
	parts, err := c.ListObjectMultipartParts(ctx, "demo", "bucket", u.ID, 0, 1)
	if err != nil || len(parts.Items) != 1 {
		t.Fatal(parts, err)
	}
	_, err = c.CompleteObjectMultipartUpload(ctx, "demo", "bucket", u.ID, faas.CompleteObjectMultipartUploadRequest{Parts: []faas.ObjectMultipartCompletedPart{{PartNumber: 1, ETag: parts.Items[0].ETag}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = c.AbortObjectMultipartUpload(ctx, "demo", "bucket", u.ID); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 5 {
		t.Fatal("unexpected management retries", requests.Load())
	}
}

func TestBucketCatalogTransferDiscovery(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/v1/apps/demo%20app/buckets" || r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("unexpected catalog request %s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"items":[],"enabled":true,"regions":["us-east-1"],"default_region":"us-east-1","max_upload_bytes":5497558138880,"max_buckets_per_app":10,"max_single_put_bytes":536870912,"max_part_bytes":536870912,"transfer_timeout_seconds":7200,"upload_profile":"direct"}`)
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	got, err := c.ListObjectBuckets(context.Background(), "demo app")
	if err != nil || got.MaxUploadBytes != 5<<40 || got.MaxSinglePutBytes != 512<<20 || got.MaxPartBytes != 512<<20 || got.TransferTimeoutSeconds != 7200 || got.UploadProfile != "direct" {
		t.Fatalf("transfer discovery: %+v %v", got, err)
	}
}

func TestBrandedSignedObjectRequest(t *testing.T) {
	size := int64(3)
	key := "arn:gregale:kms:us-east-1:11111111-1111-4111-8111-111111111111:key/22222222-2222-4222-8222-222222222222"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req faas.ObjectSignRequest
		if r.Method != "POST" || r.URL.EscapedPath() != "/v1/apps/demo%20app/buckets/bucket/signed-url" || r.Header.Get("Authorization") != "Bearer token" || json.NewDecoder(r.Body).Decode(&req) != nil || req.Encryption == nil || req.Encryption.KeyID != key || req.SizeBytes == nil || *req.SizeBytes != 3 {
			t.Error("signed request contract")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"url":"https://s3.gregale.dev/assets/file","method":"PUT","headers":{"X-Amz-Server-Side-Encryption":"aws:kms"},"expires_at":"2026-10-04T10:00:00Z","upload_id":"33333333-3333-4333-8333-333333333333"}`)
	}))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.SignBucketObject(context.Background(), "demo app", "bucket", faas.ObjectSignRequest{Method: "PUT", Key: "file", SizeBytes: &size, Encryption: &faas.ObjectEncryption{Algorithm: "aws:kms", KeyID: key}})
	if err != nil || out.UploadID == "" || out.Headers["X-Amz-Server-Side-Encryption"] != "aws:kms" {
		t.Fatal(out, err)
	}
}

func TestSignedObjectIssuanceIsNotAutomaticallyRetried(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(503) }))
	defer srv.Close()
	c, err := faas.NewClient(srv.URL, "token", faas.WithRetry(2, time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.SignBucketObject(context.Background(), "demo", "bucket", faas.ObjectSignRequest{Method: "GET", Key: "file"})
	if err == nil || calls.Load() != 1 {
		t.Fatal("URL issuer was automatically replayed", calls.Load(), err)
	}
}
