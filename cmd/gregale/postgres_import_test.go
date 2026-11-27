package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresImportCLI(t *testing.T) {
	const operation = "3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f"
	const secret = "source-test-credential"
	t.Setenv("PG_IMPORT_TEST_URL", "postgres://owner:"+secret+"@source.example.com:5433/source?sslmode=verify-full")
	base := "/v1/postgres/databases/" + restoreSwitchDB + "/imports"
	var calls atomic.Int32
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/capabilities" && r.Method == http.MethodGet {
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
			return
		}
		calls.Add(1)
		switch r.Method + " " + r.URL.Path {
		case "POST " + base + "/check":
			var source api.ManagedPostgresImportSource
			if err := json.NewDecoder(r.Body).Decode(&source); err != nil || source.Host != "source.example.com" ||
				source.Port != 5433 || source.Password != secret || len(source.SkipExtensions) != 1 || source.SkipExtensions[0] != "postgis" {
				t.Error("check did not send the expected source")
			}
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresImportStatus{State: "failed", ErrorCode: "extension_unavailable",
				MissingExtensions: []string{"timescaledb"}})
		case "POST " + base:
			var request api.ManagedPostgresImportRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.OperationID != operation || request.Source.Password != secret {
				t.Error("copy did not send the supplied request ID and source")
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresImportStatus{State: "running", OperationID: operation})
		case "GET " + base + "/" + operation:
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresImportStatus{State: "done", OperationID: operation})
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	})
	code, out, stderr := runSwitchCLI(t, server, "postgres", "import", restoreSwitchDB,
		"--check", "--source-env", "PG_IMPORT_TEST_URL", "--skip-extension", "postgis")
	if code != 1 || !strings.Contains(out, "timescaledb") || strings.Contains(out+stderr, secret) {
		t.Fatalf("check exit %d, output %q, stderr %q", code, out, stderr)
	}
	code, out, stderr = runSwitchCLI(t, server, "postgres", "import", restoreSwitchDB,
		"--source-env", "PG_IMPORT_TEST_URL", "--request-id", operation)
	if code != 0 || !strings.Contains(out, operation) || strings.Contains(out+stderr, secret) {
		t.Fatalf("start exit %d, output %q, stderr %q", code, out, stderr)
	}
	code, out, stderr = runSwitchCLI(t, server, "postgres", "import-status", restoreSwitchDB, operation)
	if code != 0 || !strings.Contains(out, "done") || strings.Contains(out+stderr, secret) || calls.Load() != 3 {
		t.Fatalf("status exit %d, requests %d, output %q, stderr %q", code, calls.Load(), out, stderr)
	}
}
