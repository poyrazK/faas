package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCmdFlagsLifecycle(t *testing.T) {
	var requests []string
	var update map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		if r.Method == "PUT" {
			if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
				t.Fatal(err)
			}
		}
		if r.URL.Query().Get("value") != "" && r.URL.Query().Get("value") != "true" {
			t.Fatal(r.URL)
		}
		if r.URL.Query().Get("variant") != "" && r.URL.Query().Get("variant") != "new" {
			t.Fatal(r.URL)
		}
		if r.URL.Path == "/v1/projects/exports/environments/production/flags/export/outcomes" && (r.URL.Query().Get("since") != "6h" || r.URL.Query().Get("customer_id") != "11111111-1111-4111-8111-111111111111") {
			t.Errorf("outcomes query=%v", r.URL.Query())
		}
		_, _ = w.Write([]byte(`{"version":1,"flags":[]}`))
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")
	oldOut, oldJSON := osStdout, jsonOutput
	var out bytes.Buffer
	osStdout, jsonOutput = &out, true
	t.Cleanup(func() { osStdout, jsonOutput = oldOut, oldJSON })
	file := filepath.Join(t.TempDir(), "flags.json")
	if err := os.WriteFile(file, []byte(`{"expected_version":0,"config":{"flags":[],"groups":{}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"get"}, {"apply", "--file", file}, {"history", "--before-version", "5"}, {"inspect", "--key", "export", "--customer-id", "customer", "--fallback-variant", "legacy"}, {"rollback", "--version", "1", "--expected-version", "2"}, {"requests", "--key", "export", "--value", "true", "--used", "true"}, {"requests", "--key", "checkout", "--variant", "new", "--used", "true"}, {"outcomes", "--key", "export", "--since", "6h", "--customer-id", "11111111-1111-4111-8111-111111111111"}} {
		args = append(args, "--project", "exports")
		if code := cmdFlags(args); code != 0 {
			t.Fatalf("%v: %d", args, code)
		}
	}
	if len(requests) != 8 || requests[1] != "PUT /v1/projects/exports/environments/production/flags" || requests[7] != "GET /v1/projects/exports/environments/production/flags/export/outcomes" || update["expected_version"] != float64(0) {
		t.Fatalf("requests=%v update=%v", requests, update)
	}
	if code := cmdFlags([]string{"rollback", "--project", "exports", "--version", "1"}); code == 0 {
		t.Fatal("rollback accepted missing current version")
	}
	if code := cmdFlags([]string{"requests", "--project", "exports", "--key", "checkout", "--variant", "new", "--value", "true"}); code == 0 {
		t.Fatal("variant and boolean evidence filters should be mutually exclusive")
	}
}
