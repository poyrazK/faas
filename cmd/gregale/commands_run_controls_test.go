package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdRunControlsAndHistoryRouting(t *testing.T) {
	puts, gets := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/run-controls/budgets/customer/customer-42":
			puts++
			var req api.SetRunBudgetRequest
			if r.Method != http.MethodPut || json.NewDecoder(r.Body).Decode(&req) != nil || req.ExpectedRevision != 2 || req.Enabled || req.Limits.MaxConcurrent == nil || *req.Limits.MaxConcurrent != 0 {
				t.Error("policy request changed")
			}
			_ = json.NewEncoder(w).Encode(api.RunBudgetPolicy{Revision: 3})
		case "/v1/run-history":
			gets++
			if r.URL.Query().Get("customer_ref") != "customer-42" || r.URL.Query().Get("task_version") != "1" || r.URL.Query().Get("cursor") != "next-page" {
				t.Error("history filter lost")
			}
			_ = json.NewEncoder(w).Encode(api.RunHistoryResponse{Runs: []api.RunHistoryEntry{}})
		default:
			t.Error("controls/history command routed outside Source Run", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	manifest := filepath.Join(t.TempDir(), "budget.json")
	if err := os.WriteFile(manifest, []byte(`{"expected_revision":2,"enabled":false,"limits":{"max_concurrent":0}}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"run", "controls", "customer", "customer-42", "--manifest", manifest, "--json"}, {"run", "history", "--customer-ref", "customer-42", "--task-name", "report", "--task-version", "1", "--cursor", "next-page", "--json"}} {
		code, _, stderr := runSourceRunCLI(t, args...)
		if code != 0 {
			t.Fatal("command failed", args, code, stderr)
		}
	}
	if puts != 1 || gets != 1 {
		t.Fatal("wrong control requests", puts, gets)
	}
	if err := os.WriteFile(manifest, []byte(`{"expected_revision":3,"enabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	code, _, _ := runSourceRunCLI(t, "run", "controls", "customer", "customer-42", "--manifest", manifest, "--json")
	if code == 0 || puts != 1 {
		t.Fatal("incomplete replacement reached server")
	}
}
