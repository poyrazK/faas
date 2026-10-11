package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

func TestJSONOperatorCommandReceipts(t *testing.T) {
	for _, tc := range []struct {
		args                          []string
		method, path, response, field string
	}{
		{[]string{"auth", "status"}, "GET", "/v1/account", `{"id":"operator-json","email":"operator@example.test","status":"active"}`, "email"},
		{[]string{"accounts", "show", "--account-id", testAccountID}, "GET", "/v1/admin/obs/tenants/" + testAccountID, `{"account":{"account_id":"` + testAccountID + `","status":"active"}}`, "account"},
		{[]string{"audit", "trace", "--trace-id", testBuildSweepTraceID}, "GET", "/v1/admin/obs/traces/" + testBuildSweepTraceID, `{"trace_id":"` + testBuildSweepTraceID + `","intents":[],"events":[]}`, "trace_id"},
		{[]string{"builds", "sweep-stuck", "--older-than", "20m", "--reason", "test_incident", "--trace-id", testBuildSweepTraceID, "--yes"}, "POST", "/v1/admin/builds/sweep-stuck", `{"ok":true,"swept_count":3,"older_than_secs":1200}`, "swept_count"},
		{[]string{"jobs", "active", "--account-id", testOperatorJobAccountID}, "GET", "/v1/admin/ops/jobs/runs", `{"account_id":"` + testOperatorJobAccountID + `","runs":[],"limit":20}`, "account_id"},
		{[]string{"deployments", "active"}, "GET", "/v1/admin/ops/deployments", `{"deployments":[],"limit":20}`, "limit"},
		{[]string{"github", "status"}, "GET", "/v1/admin/ops/github/recovery", `{"deliveries":[],"check_updates":[]}`, "deliveries"},
		{[]string{"status", "incident", "create", "--title", "API errors", "--impact", "degraded", "--components", "api_console", "--message", "Investigating."}, "POST", "/v1/admin/status/incidents", `{"id":"status-json","title":"API errors","state":"investigating"}`, "title"},
		{[]string{"config", "show", "--key", "hsts_enabled"}, "GET", "/v1/admin/config", `{"items":[{"key":"hsts_enabled","value":true,"version":1}]}`, "key"},
	} {
		t.Run(tc.args[0], func(t *testing.T) {
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != tc.method || r.URL.Path != tc.path {
					t.Errorf("request=%s %s, want %s %s", r.Method, r.URL.Path, tc.method, tc.path)
				}
				if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "opaque-session" {
					t.Errorf("operator session cookie=%v err=%v", cookie, err)
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, tc.response)
			}))
			defer server.Close()
			installTestOperatorSession(t, server.URL, "opaque-session")
			t.Setenv("FAAS_JSON", "0")
			out, stderr, restore := captureOperatorIO()
			defer restore()
			if code := run(append([]string{"--json"}, tc.args...)); code != 0 {
				t.Fatalf("exit=%d stderr=%s", code, stderr)
			}
			if requests != 1 || stderr.Len() != 0 {
				t.Fatalf("requests=%d stderr=%s", requests, stderr)
			}
			var receipt map[string]json.RawMessage
			decoder := json.NewDecoder(out)
			if err := decoder.Decode(&receipt); err != nil {
				t.Fatalf("decode receipt: %v", err)
			}
			// The command must preserve the API's receipt rather than
			// wrapping a human summary in a JSON string.
			var expected map[string]json.RawMessage
			if tc.args[0] == "config" {
				expected = map[string]json.RawMessage{"key": json.RawMessage(`"hsts_enabled"`)}
			} else if err := json.Unmarshal([]byte(tc.response), &expected); err != nil {
				t.Fatal(err)
			}
			var gotValue, wantValue any
			if err := json.Unmarshal(receipt[tc.field], &gotValue); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(expected[tc.field], &wantValue); err != nil {
				t.Fatal(err)
			}
			if gotFields, ok := gotValue.(map[string]any); ok {
				for key, value := range wantValue.(map[string]any) {
					if !reflect.DeepEqual(gotFields[key], value) {
						t.Fatalf("%s.%s=%v, want %v", tc.field, key, gotFields[key], value)
					}
				}
			} else if !reflect.DeepEqual(gotValue, wantValue) {
				t.Fatalf("%s=%s, want %s", tc.field, receipt[tc.field], expected[tc.field])
			}
			if err := decoder.Decode(&receipt); !errors.Is(err, io.EOF) {
				t.Fatalf("trailing output: %v", err)
			}
		})
	}
}

func TestJSONArtifactLifecycleReceipt(t *testing.T) {
	out, stderr, restore := captureOperatorIO()
	defer restore()
	t.Setenv("FAAS_JSON", "0")
	root := t.TempDir()
	envFile, lifecycleFile := filepath.Join(root, "storage.env"), filepath.Join(root, "imaged.env")
	if err := os.WriteFile(envFile, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_REQUIRE_SHARED_ARTIFACTS=1\nFAAS_STORAGE_LOCAL_PREFIXES=none\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lifecycleFile, []byte("FAAS_OCI_USERNAME=lifecycle-bot\nFAAS_OCI_PASSWORD=test-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	backend := newMemoryArtifactBackend()
	previous := artifactBackendFromEnv
	artifactBackendFromEnv = func(context.Context) (storage.StorageBackend, error) { return backend, nil }
	t.Cleanup(func() { artifactBackendFromEnv = previous })
	if code := run([]string{"--json", "artifact", "lifecycle-check", "--env-file", envFile, "--lifecycle-env-file", lifecycleFile}); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr)
	}
	var receipt artifactLifecycleReport
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil || !receipt.ReadVerified || !receipt.Deleted || receipt.Bytes != 32 {
		t.Fatalf("receipt=%+v err=%v stdout=%s", receipt, err, out)
	}
	if _, err := backend.Get(context.Background(), receipt.Key); !storage.IsNotFound(err) {
		t.Fatalf("probe remains after command: %v", err)
	}
}
