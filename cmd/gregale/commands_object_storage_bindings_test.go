package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCmdBindingsObjectStorageListJSONIsSafeAndResolvesBucketName(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/buckets":
			_, _ = w.Write([]byte(`{"items":[{"id":"bucket-1","name":"assets","scope":"production","state":"ready"}]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/buckets/bucket-1/compute-bindings":
			_, _ = w.Write([]byte(`{"items":[{"id":"binding-1","bucket_id":"bucket-1","scope":"production","prefix":"GREGALE_S3_ASSETS","credential":{"id":"credential-1","bucket_id":"bucket-1","access_key_id":"AKIA_SECRET_SENTINEL","permission":"read_write","status":"ready"},"secret_keys":{"access_key_id":"ACCESS_KEY_NAME_SENTINEL","secret_access_key":"SECRET_KEY_NAME_SENTINEL"},"rotation_pending":true}]}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })

	if code := run([]string{"--json", "bindings", "object-storage", "list", "api", "assets"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	var got objectStorageBindingCLIList
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.App != "api" || len(got.Items) != 1 {
		t.Fatalf("inventory = %+v", got)
	}
	binding := got.Items[0]
	if binding.ID != "binding-1" || binding.BucketID != "bucket-1" || binding.Bucket != "assets" ||
		binding.Permission != "read_write" || !binding.RotationPending {
		t.Fatalf("binding = %+v", binding)
	}
	for _, secret := range []string{"AKIA_SECRET_SENTINEL", "ACCESS_KEY_NAME_SENTINEL", "SECRET_KEY_NAME_SENTINEL"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("CLI output exposed credential material %q: %s", secret, out.String())
		}
	}
}

func TestCmdBindingsObjectStorageRotateReportsPendingWithoutSecrets(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/buckets":
			_, _ = w.Write([]byte(`{"items":[{"id":"bucket-1","name":"assets","scope":"production","state":"ready"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/buckets/bucket-1/compute-bindings/binding-1/rotate":
			_, _ = w.Write([]byte(`{"id":"binding-1","bucket_id":"bucket-1","scope":"production","prefix":"GREGALE_S3_ASSETS","credential":{"id":"credential-2","bucket_id":"bucket-1","access_key_id":"AKIA_SECRET_SENTINEL","permission":"read_write","status":"ready"},"secret_keys":{"access_key_id":"ACCESS_KEY_NAME_SENTINEL","secret_access_key":"SECRET_KEY_NAME_SENTINEL"},"rotation_pending":true}`))
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })

	if code := cmdBindingsObjectStorageRotate([]string{"api", "assets", "binding-1"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if !strings.Contains(out.String(), "rotation_pending:  true") || !strings.Contains(out.String(), "binding-1") {
		t.Fatalf("output = %s", out.String())
	}
	for _, secret := range []string{"AKIA_SECRET_SENTINEL", "ACCESS_KEY_NAME_SENTINEL", "SECRET_KEY_NAME_SENTINEL"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("CLI output exposed credential material %q: %s", secret, out.String())
		}
	}
}

func TestCmdBindingsObjectStorageRotateWaitReturnsCompletedBindingWithoutSecrets(t *testing.T) {
	var reads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/buckets":
			_, _ = w.Write([]byte(`{"items":[{"id":"bucket-1","name":"assets","scope":"production","state":"ready"}]}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v1/apps/api/buckets/bucket-1/compute-bindings/binding-1/rotate":
			_, _ = w.Write([]byte(`{"id":"binding-1","bucket_id":"bucket-1","scope":"production","prefix":"GREGALE_S3_ASSETS","credential":{"id":"credential-2","bucket_id":"bucket-1","access_key_id":"AKIA_SECRET_SENTINEL","permission":"read_write","status":"ready"},"secret_keys":{"access_key_id":"ACCESS_KEY_NAME_SENTINEL","secret_access_key":"SECRET_KEY_NAME_SENTINEL"},"rotation_pending":true}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/buckets/bucket-1/compute-bindings":
			reads++
			pending := reads == 1
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{map[string]any{
				"id": "binding-1", "bucket_id": "bucket-1", "scope": "production", "prefix": "GREGALE_S3_ASSETS",
				"credential":       map[string]any{"id": "credential-2", "bucket_id": "bucket-1", "access_key_id": "AKIA_SECRET_SENTINEL", "permission": "read_write", "status": "ready"},
				"secret_keys":      map[string]any{"access_key_id": "ACCESS_KEY_NAME_SENTINEL", "secret_access_key": "SECRET_KEY_NAME_SENTINEL"},
				"rotation_pending": pending,
			}}})
		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })

	args := []string{"api", "assets", "binding-1", "--wait", "--wait-timeout=1s", "--poll-interval=1ms"}
	if code := cmdBindingsObjectStorageRotate(args); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	if reads != 2 || !strings.Contains(out.String(), "rotation_pending:  false") {
		t.Fatalf("status reads = %d, output = %s", reads, out.String())
	}
	for _, secret := range []string{"AKIA_SECRET_SENTINEL", "ACCESS_KEY_NAME_SENTINEL", "SECRET_KEY_NAME_SENTINEL"} {
		if strings.Contains(out.String(), secret) {
			t.Fatalf("CLI output exposed credential material %q: %s", secret, out.String())
		}
	}
}

func TestCmdBindingsObjectStorageRevokeJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/v1/apps/api/buckets" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"items":[{"id":"bucket-1","name":"assets","scope":"production","state":"ready"}]}`))
			return
		}
		if r.Method != http.MethodDelete || r.URL.Path != "/v1/apps/api/buckets/bucket-1/compute-bindings/binding-1" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_test")
	var out bytes.Buffer
	previousOut, previousJSON := osStdout, jsonOutput
	osStdout, jsonOutput = &out, false
	t.Cleanup(func() { osStdout, jsonOutput = previousOut, previousJSON })

	if code := run([]string{"--json", "bindings", "object-storage", "revoke", "api", "bucket-1", "binding-1"}); code != 0 {
		t.Fatalf("exit = %d, output = %s", code, out.String())
	}
	var got objectStorageBindingCLIRevokeResult
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatalf("decode output: %v\n%s", err, out.String())
	}
	if got.App != "api" || got.BindingID != "binding-1" || got.BucketID != "bucket-1" || got.State != "revoked" {
		t.Fatalf("result = %+v", got)
	}
}
