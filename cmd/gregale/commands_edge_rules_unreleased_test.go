package main_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261023113721 — the list marks a rule the edge does not enforce
// because a feature it uses is not yet available to the account.
func TestCmdEdgeRulesListMarksUnreleasedFeatures(t *testing.T) {
	resetJSONEnv(t)
	inert := sampleEdgeRuleResponse(edgeRuleTestID)
	inert.Kind, inert.UnreleasedFeatures = "challenge", []string{api.EdgeFeatureChallenge}
	plain := sampleEdgeRuleResponse("abcdef0123456789abcdef0123456789")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]api.EdgeRuleResponse{inert, plain})
	}))
	defer srv.Close()
	t.Setenv("FAAS_API", srv.URL)
	t.Setenv("FAAS_TOKEN", "fp_live_x")

	var stdout bytes.Buffer
	oldOut := (*main.OsStdout)
	(*main.OsStdout) = &stdout
	defer func() { (*main.OsStdout) = oldOut }()

	if code := main.CmdEdgeRulesList([]string{}); code != 0 {
		t.Fatalf("list = %d, want 0", code)
	}
	var marked []string
	for _, line := range strings.Split(stdout.String(), "\n") {
		if strings.Contains(line, "not yet available: challenge") {
			marked = append(marked, line)
		}
	}
	if len(marked) != 1 || !strings.Contains(marked[0], edgeRuleTestID) {
		t.Fatalf("marked lines = %q, want only the challenge rule:\n%s", marked, stdout.String())
	}
}
