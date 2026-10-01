package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestRegionsSetSendsExplicitPolicyAndIdempotency(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if r.Method != http.MethodPut || r.URL.Path != "/v1/regions/eu-fsn" || r.Header.Get("Idempotency-Key") == "" {
			t.Errorf("invalid request %s %s", r.Method, r.URL.Path)
		}
		var req api.UpdateComputeRegionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.AcceptingNewApps == nil || *req.AcceptingNewApps || req.DisplayName != "Falkenstein" {
			t.Errorf("policy %+v %v", req, err)
		}
		_ = json.NewEncoder(w).Encode(api.ComputeRegionResponse{ID: "eu-fsn", DisplayName: req.DisplayName})
	}))
	t.Cleanup(srv.Close)
	installTestOperatorSession(t, srv.URL, "operator-session")
	oldOut := osStdout
	osStdout = &bytes.Buffer{}
	t.Cleanup(func() { osStdout = oldOut })
	if code := cmdRegionsDispatch([]string{"set", "--region", "eu-fsn", "--display-name", "Falkenstein", "--accepting-new-apps=false"}); code != 0 || !called {
		t.Fatalf("exit=%d called=%t", code, called)
	}
	called = false
	if code := cmdRegionsDispatch([]string{"set", "--region", "eu-fsn", "--display-name", "Falkenstein"}); code != 2 || called {
		t.Fatalf("omitted policy exit=%d called=%t", code, called)
	}
}
