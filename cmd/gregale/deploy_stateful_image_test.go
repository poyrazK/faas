package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// prod 2026-11-15: `deploy --image redis` was refused only after the app
// existed, leaving an empty app that counted toward the app quota. The CLI
// refuses a datastore image before it creates anything.
func TestDeployStatefulImageCreatesNoApp(t *testing.T) {
	var writes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet {
			writes = append(writes, r.Method+" "+r.URL.Path)
		}
		switch r.URL.Path {
		case "/v1/account":
			_ = json.NewEncoder(w).Encode(api.AccountResponse{ID: "acct", Plan: "scale"})
		default:
			http.Error(w, `{"status":404,"code":"not_found","title":"Not found"}`, http.StatusNotFound)
		}
	}))
	defer srv.Close()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", main.TestAPIKey('a'))

	_, stderr, restore := main.SwapIO(t)
	code := main.Run([]string{"--non-interactive", "deploy", "--image", "docker.io/library/redis@sha256:" + strings.Repeat("a", 64), "--name", "cache", "--no-wait"})
	restore()
	if code == 0 {
		t.Fatal("deploy of a redis image succeeded")
	}
	if len(writes) != 0 {
		t.Fatalf("refused deploy still wrote to the API: %v", writes)
	}
	if !strings.Contains(stderr(), "Nothing was created or deployed") {
		t.Fatalf("stderr = %q", stderr())
	}
}
