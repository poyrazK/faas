package main

import (
	"bytes"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBucketVersionDeleteCLI(t *testing.T) {
	bucket, version := uuid.NewString(), uuid.NewString()
	key := "目录 /+%.txt"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "DELETE" || r.URL.Path != "/v1/apps/demo/buckets/"+bucket+"/objects/versions" || r.URL.Query().Get("key") != key || r.URL.Query().Get("version_id") != version {
			t.Error(r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.ObjectVersionDeleteResult{VersionID: version, DeleteMarker: true})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "token")
	var out, stderr bytes.Buffer
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &out, &stderr, true
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	if code := cmdBucket([]string{"version-delete", "demo", bucket, key, version}); code != 0 {
		t.Fatal(code, stderr.String())
	}
	var result api.ObjectVersionDeleteResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.VersionID != version || !result.DeleteMarker {
		t.Fatal(result, err)
	}
	if code := cmdBucket([]string{"version-delete", "demo", bucket, key, "null"}); code != 1 || calls != 1 {
		t.Fatal("mutable null dispatched", code, calls)
	}
}
