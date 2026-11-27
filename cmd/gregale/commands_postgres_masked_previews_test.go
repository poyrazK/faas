package main_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestPostgresPreviewsSetMaskedSendsPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(path, []byte(`{"tables":[{"name":"people","columns":[{"name":"email","action":"email"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/capabilities" {
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
			return
		}
		if r.URL.Path == "/v1/postgres/databases" && r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(api.ManagedPostgresDatabaseList{Items: []api.ManagedPostgresDatabase{{ID: restoreSwitchDB, Name: "orders"}}})
			return
		}
		if r.URL.Path != "/v1/postgres/databases/"+restoreSwitchDB+"/previews" || r.Method != http.MethodPut {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var req api.SetManagedPostgresPreviewsRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Previews != "masked" || req.MaskingPolicy == nil ||
			len(req.MaskingPolicy.Tables) != 1 || len(req.MaskingPolicy.Tables[0].Columns) != 1 || req.MaskingPolicy.Tables[0].Columns[0].Action != "email" {
			t.Errorf("wrong preview request: %+v, %v", req, err)
		}
		_ = json.NewEncoder(w).Encode(api.ManagedPostgresDatabase{ID: restoreSwitchDB, Name: "orders", Previews: "masked"})
	})
	code, out, stderr := runSwitchCLI(t, handler, "postgres", "previews", "set", "orders", "masked", "--policy", path)
	if code != 0 || !strings.Contains(out, "validated masked database") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
}
