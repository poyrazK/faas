package ciapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/apid/ciapi"
	"github.com/onebox-faas/faas/pkg/ci"
)

func TestCIAutomationPathsPolicyValidationAndDecisionHistory(t *testing.T) {
	f := newAutomationRecoveryAPI(t)
	store := ci.NewPostgresStore(f.pool)
	profiles, err := store.ExecutionProfiles(t.Context(), f.account)
	if err != nil || len(profiles) != 1 {
		t.Fatal("profile missing")
	}
	req := ci.AutomationRequest{ProfileID: profiles[0].ID, PushBranches: []string{"main"}, IncludePaths: []string{"src/**", "go.mod"}, ExcludePaths: []string{"src/docs/**"}}
	var policy ci.AutomationPolicy
	w := f.call(t, ciapi.Handler.PutAutomationPolicy, f.account, f.binding, "", "", req)
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &policy) != nil || policy.IncludePaths[0] != "go.mod" || len(policy.ExcludePaths) != 1 {
		t.Fatalf("path policy: %d %s", w.Code, w.Body)
	}
	if f.handler.Config.DispatchEnabled || f.handler.Config.WorkerEnabled || f.handler.Config.TransportEnabled {
		t.Fatal("policy setup enabled compute")
	}
	if w := f.call(t, ciapi.Handler.PutAutomationPolicy, f.other, f.binding, "", "", req); w.Code != http.StatusNotFound {
		t.Fatal("foreign account saved path policy")
	}
	for _, patterns := range [][]string{{"../src/**"}, {"src/**", "src/**"}, {"[ab]"}} {
		bad := req
		bad.IncludePaths = patterns
		if w := f.call(t, ciapi.Handler.PutAutomationPolicy, f.account, f.binding, "", "", bad); w.Code != http.StatusBadRequest {
			t.Fatal("invalid path policy accepted")
		}
	}
	cfg := f.handler.Config
	cfg.DispatchEnabled, cfg.WorkerEnabled, cfg.TransportEnabled = true, true, true
	base, head := strings.Repeat("a", 40), strings.Repeat("c", 40)
	raw, _ := json.Marshal(map[string]any{"ref": "refs/heads/main", "before": base, "after": head, "installation": map[string]any{"id": 12}, "repository": map[string]any{"id": 34, "full_name": "private/automation-recovery"}})
	if err := ci.RecordVerifiedGitHubDelivery(t.Context(), cfg, store, uuid.NewString(), "push", raw); err != nil {
		t.Fatal(err)
	}
	if _, err := ci.AdmitAutomationWithResolvers(t.Context(), cfg, store, f.handler.VerifyGitHub, ci.AutomationResolvers{Changes: func(context.Context, ci.GitHubBinding, string, string) (ci.ChangedFiles, error) {
		return ci.ChangedFiles{BaseSHA: base, HeadSHA: head, MergeBaseSHA: base, Complete: true, Files: []ci.ChangedFile{{Path: "docs/private-name", Status: "modified"}}}, nil
	}}); err != nil {
		t.Fatal(err)
	}
	w = f.call(t, ciapi.Handler.ListAutomationEvents, f.account, f.binding, "", "", nil)
	var out struct {
		Events []ci.AutomationEventSummary `json:"events"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatal("history unavailable with compute gates off")
	}
	var event ci.AutomationEventSummary
	for _, row := range out.Events {
		if row.CommitSHA == head {
			event = row
		}
	}
	if event.PathDecision == nil || event.PathDecision.Outcome != "skip" || event.PathDecision.FileCount != 1 || event.ExecutionID != "" || event.Retryable || strings.Contains(w.Body.String(), "private-name") {
		t.Fatalf("history lost skip decision or exposed filenames: %s", w.Body)
	}
	if w := f.call(t, ciapi.Handler.AutomationEventStatus, f.other, f.binding, event.ID, "", nil); w.Code != http.StatusNotFound {
		t.Fatal("foreign account read path evidence")
	}
	var executions, runs int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM ci_executions),(SELECT count(*) FROM job_runs)").Scan(&executions, &runs); err != nil || executions != 0 || runs != 0 {
		t.Fatal("path skip created compute")
	}
}
