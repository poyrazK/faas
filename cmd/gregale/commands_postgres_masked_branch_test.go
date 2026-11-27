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

func TestPostgresBranchMaskedUsesDedicatedLogicalCopyRoute(t *testing.T) {
	file := filepath.Join(t.TempDir(), "policy.json")
	if err := os.WriteFile(file, []byte(`{"tables":[{"name":"users","columns":[{"name":"email","action":"email"}]}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	server := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/capabilities" {
			_, _ = w.Write([]byte(`{"registry_version":1,"plan":"pro","capabilities":[],"hidden_capabilities":[]}`))
			return
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/postgres/databases/"+restoreSwitchDB+"/masked-branches" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		var req api.CreateManagedPostgresMaskedBranchRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name != "orders-masked" || req.TTLSeconds != 1800 ||
			len(req.Tables) != 1 || len(req.Tables[0].Columns) != 1 || req.Tables[0].Columns[0].Action != "email" {
			t.Errorf("invalid branch request: %+v, %v", req, err)
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(api.ManagedPostgresDatabase{ID: "branch-id", Name: req.Name, State: "provisioning", MaskedBranch: true, RestoreSourceDatabaseID: restoreSwitchDB})
	})
	code, out, stderr := runSwitchCLI(t, server, "postgres", "branch", "masked", restoreSwitchDB, "orders-masked", "--policy", file, "--ttl", "30m")
	if code != 0 || !strings.Contains(out, "masked_from:") || !strings.Contains(out, "postgres status branch-id") {
		t.Fatalf("exit=%d stdout=%q stderr=%q", code, out, stderr)
	}
	alias := filepath.Join(filepath.Dir(file), "alias.json")
	if err := os.Symlink(file, alias); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		path, message string
	}{
		{alias, "refusing to follow symlink"},
		{filepath.Dir(file), "refusing non-regular file"},
	} {
		code, _, stderr := runSwitchCLI(t, server, "postgres", "branch", "masked", restoreSwitchDB, "orders-masked", "--policy", tc.path)
		if code == 0 || !strings.Contains(stderr, tc.message) {
			t.Fatalf("unsafe policy file: exit=%d, stderr=%q", code, stderr)
		}
	}
}
