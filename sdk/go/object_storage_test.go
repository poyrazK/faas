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
