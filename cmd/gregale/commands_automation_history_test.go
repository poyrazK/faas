package main_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 1270
func TestAutomationHistoryCLI(t *testing.T) {
	eventRecoveryAPI(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/apps/consumer/recovery-policy/history" || r.URL.Query().Get("workflow") != "recover" || r.URL.Query().Get("limit") != "25" {
			t.Errorf("request %s %s", r.Method, r.URL)
		}
		_ = json.NewEncoder(w).Encode(api.WorkflowRecoveryHistory{Entries: []api.WorkflowRecoveryHistoryEntry{{Kind: "retry_evaluation", Outcome: "pending", Reason: "execution_quota", ChildJobID: "child", EvidenceKind: "parent", ActorID: "operator", RequestID: "request", EvidenceURL: "/evidence/parent"}}, NextAfter: "cursor", RetainedObservations: true})
	})
	out := main.CaptureAutomationStdout(t)
	if code := main.CmdAutomationsHistory([]string{"--app", "consumer", "--workflow", "recover"}); code != 0 || !strings.Contains(out.String(), "retry_evaluation | pending | execution_quota") || !strings.Contains(out.String(), "Next cursor: cursor") || !strings.Contains(out.String(), "Evidence: parent") || !strings.Contains(out.String(), "/evidence/parent") {
		t.Fatalf("history %d %s", code, out)
	}
	if code := main.CmdAutomationsHistory([]string{"--app", "consumer", "--limit", "101"}); code != 1 {
		t.Fatal("invalid limit accepted")
	}
}
