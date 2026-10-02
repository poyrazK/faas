package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestBucketDeletionsCLI(t *testing.T) {
	id, bucket := uuid.NewString(), uuid.NewString()
	selector := "null"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method == "POST" {
			var in api.ObjectDeletionRequest
			if json.NewDecoder(r.Body).Decode(&in) != nil || in.ID != id || in.Key != "key" || in.VersionID != selector {
				t.Error(in)
			}
			w.WriteHeader(202)
		}
		_ = json.NewEncoder(w).Encode(api.ObjectDeletion{ID: id, BucketID: bucket, Key: "key", Selector: "null", State: "dispatched", LastErrorCode: "provider_uncertain"})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "token")
	var out, stderr bytes.Buffer
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &out, &stderr, true
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	if code := cmdBucket([]string{"deletions", "start", "demo", bucket, "key", id, "null"}); code != 0 {
		t.Fatal(code)
	}
	if code := cmdBucket([]string{"deletions", "status", "demo", bucket, id}); code != 0 || calls != 2 {
		t.Fatal(code, calls)
	}
	selector = uuid.NewString()
	if code := cmdBucket([]string{"deletions", "start", "demo", bucket, "key", id, selector}); code != 0 || calls != 3 {
		t.Fatal(code, calls)
	}
	if code := cmdBucket([]string{"deletions", "start", "demo", bucket, "key", id, "native"}); code != 1 || calls != 3 {
		t.Fatal(code, calls)
	}
}
