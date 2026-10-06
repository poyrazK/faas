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

func TestBucketTagsCLI(t *testing.T) {
	bucket, version := uuid.NewString(), uuid.NewString()
	key := "目录 /+%.txt"
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/apps/demo/buckets/"+bucket+"/objects/tags" || r.URL.Query().Get("key") != key || r.URL.Query().Get("version_id") != version {
			t.Error(r.URL)
		}
		if r.Method == "PUT" {
			var input api.ObjectTaggingRequest
			if json.NewDecoder(r.Body).Decode(&input) != nil || input.Tags["team"] != "core & friends" {
				t.Error(input)
			}
		}
		_ = json.NewEncoder(w).Encode(api.ObjectTaggingResult{VersionID: version, Tags: map[string]string{}})
	}))
	t.Cleanup(srv.Close)
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "token")
	var out, stderr bytes.Buffer
	oldOut, oldErr, oldJSON := osStdout, osStderr, jsonOutput
	osStdout, osStderr, jsonOutput = &out, &stderr, true
	t.Cleanup(func() { osStdout, osStderr, jsonOutput = oldOut, oldErr, oldJSON })
	for _, action := range []string{"get", "set", "clear"} {
		args := []string{"tags", action, "demo", bucket, key}
		if action == "set" {
			args = append(args, "team=core+%26+friends")
		}
		args = append(args, version)
		if code := cmdBucket(args); code != 0 {
			t.Fatal(action, code, stderr.String())
		}
	}
	for _, raw := range []string{"duplicate=1&duplicate=2", "missing", "a=b&missing", "=empty", "a=%xx"} {
		if code := cmdBucket([]string{"tags", "set", "demo", bucket, key, raw, version}); code != 1 || calls != 3 {
			t.Fatal("invalid tags dispatched", raw, code, calls)
		}
	}
	if code := cmdBucket([]string{"tags", "get", "demo", bucket, key, "provider-private-id"}); code != 1 || calls != 3 {
		t.Fatal("private selector dispatched", code, calls)
	}
}
