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
	for _, args := range [][]string{{"get"}, {"apply", "--file", file}, {"history", "--before-version", "5"}, {"inspect", "--key", "export", "--customer-id", "customer"}, {"rollback", "--version", "1", "--expected-version", "2"}, {"requests", "--key", "export", "--value", "true", "--used", "true"}} {
		args = append(args, "--project", "exports")
		if code := cmdFlags(args); code != 0 {
			t.Fatalf("%v: %d", args, code)
		}
	}
	if len(requests) != 6 || requests[1] != "PUT /v1/projects/exports/environments/production/flags" || update["expected_version"] != float64(0) {
		t.Fatalf("requests=%v update=%v", requests, update)
	}
	if code := cmdFlags([]string{"rollback", "--project", "exports", "--version", "1"}); code == 0 {
		t.Fatal("rollback accepted missing current version")
	}
}
