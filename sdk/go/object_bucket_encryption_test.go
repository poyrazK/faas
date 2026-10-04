package faas_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestBucketDefaultEncryptionClient(t *testing.T) {
	calls := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls = append(calls, r.Method)
		if r.URL.EscapedPath() != "/v1/apps/demo%2Fx/buckets/bucket%2Fx/encryption" || r.Header.Get("Authorization") != "Bearer token" {
			t.Error(r.URL, r.Header)
		}
		if r.Method == "PUT" {
			var in faas.ObjectBucketEncryptionRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.Encryption.Algorithm != "AES256" {
				t.Error(in)
			}
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" {
			w.WriteHeader(202)
		}
		_, _ = fmt.Fprint(w, `{"bucket_id":"bucket","state":"waiting","revision":2,"desired_encryption":{"algorithm":"AES256"},"updated_at":"2026-10-04T00:00:00Z"}`)
	}))
	defer server.Close()
	c, err := faas.NewClient(server.URL, "token")
	if err != nil {
		t.Fatal(err)
	}
	created, err := c.PutObjectBucketEncryption(context.Background(), "demo/x", "bucket/x", faas.ObjectEncryption{Algorithm: "AES256"})
	if err != nil || created.State != "waiting" || created.DesiredEncryption == nil || created.DesiredEncryption.Algorithm != "AES256" {
		t.Fatal(created, err)
	}
	if _, err = c.GetObjectBucketEncryption(context.Background(), "demo/x", "bucket/x"); err != nil {
		t.Fatal(err)
	}
	if _, err = c.DeleteObjectBucketEncryption(context.Background(), "demo/x", "bucket/x"); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(calls) != "[PUT GET DELETE]" {
		t.Fatal(calls)
	}
}
