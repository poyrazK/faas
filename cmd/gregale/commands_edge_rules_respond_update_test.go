package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCmdEdgeRulesUpdate_RespondAction(t *testing.T) {
	resetJSONEnv(t)
	var gotBody api.UpdateEdgeRuleRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch {
			t.Errorf("method = %q, want PATCH", r.Method)
		}
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode request: %v", err)
		}
		_ = json.NewEncoder(w).Encode(sampleEdgeRuleResponse(edgeRuleTestID))
	}))
	defer srv.Close()

	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	if code := cmdEdgeRulesUpdate([]string{
		"--kind", "respond",
		"--respond-status", "500",
		"--respond-body", `{"error":"backend unavailable"}`,
		edgeRuleTestID,
	}); code != 0 {
		t.Fatalf("update respond = %d, want 0", code)
	}
	if gotBody.Action == nil {
		t.Fatal("action = nil, want respond action")
	}
	var action api.EdgeRuleRespondAction
	if err := json.Unmarshal(*gotBody.Action, &action); err != nil {
		t.Fatalf("decode action: %v", err)
	}
	if action.StatusCode != http.StatusInternalServerError {
		t.Errorf("status_code = %d, want %d", action.StatusCode, http.StatusInternalServerError)
	}
	if string(action.Body) != `{"error":"backend unavailable"}` {
		t.Errorf("body = %s, want fixed JSON body", action.Body)
	}
}
