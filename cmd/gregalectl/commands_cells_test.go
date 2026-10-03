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

func TestCellsCLISendsOperatorPolicyAndRejectsIncompleteOrigin(t *testing.T) {
	id := uuid.NewString()
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPut || r.URL.Path != "/v1/global-cells/"+id || r.Header.Get("Idempotency-Key") == "" || r.Header.Get("Authorization") != "" {
			t.Error("wrong operator request")
		}
		if cookie, err := r.Cookie("faas_sid"); err != nil || cookie.Value != "operator-session" {
			t.Error("operator cookie missing")
		}
		var req api.PutGlobalCellRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Enabled == nil || *req.Enabled || req.Region != "eu-fsn" || req.APIBase != "https://api.fsn.example.com" {
			t.Error("wrong registry policy")
		}
		_ = json.NewEncoder(w).Encode(api.GlobalCellResponse{ID: id, Region: req.Region, APIBase: req.APIBase, Enabled: *req.Enabled, Generation: 1})
	}))
	defer srv.Close()
	installTestOperatorSession(t, srv.URL, "operator-session")
	oldOut := osStdout
	osStdout = &bytes.Buffer{}
	t.Cleanup(func() { osStdout = oldOut })
	args := []string{"set", "--cell-id", id, "--region", "eu-fsn", "--api-base", "https://api.fsn.example.com"}
	if code := cmdCellsDispatch(append(args, "--enabled=false")); code != 0 || !called {
		t.Fatalf("exit=%d called=%t", code, called)
	}
	called = false
	if code := cmdCellsDispatch(args); code != 2 || called {
		t.Fatal("missing policy accepted")
	}
	args[6] += "/path"
	if code := cmdCellsDispatch(append(args, "--enabled=true")); code != 2 || called {
		t.Fatal("non-origin endpoint accepted")
	}
}
