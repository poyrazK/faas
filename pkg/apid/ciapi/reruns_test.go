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

func TestCIRerunAPIRequiresFreshGatesAndPreservesAuditIdentity(t *testing.T) {
	f := newAutomationRecoveryAPI(t)
	store := ci.NewPostgresStore(f.pool)
	profiles, err := store.ExecutionProfiles(t.Context(), f.account)
	if err != nil || len(profiles) != 1 {
		t.Fatal("profile fixture missing")
	}
	cfg := f.handler.Config
	cfg.DispatchEnabled, cfg.WorkerEnabled, cfg.TransportEnabled = true, true, true
	parent, _, err := ci.AdmitJobs(t.Context(), cfg, store, f.handler.VerifyGitHub, f.account, "original", ci.JobsSubmitRequest{ProfileID: profiles[0].ID, SubmitRequest: ci.SubmitRequest{CommitSHA: strings.Repeat("a", 40)}})
	if err != nil {
		t.Fatal(err)
	}
	parent, err = store.CancelExecution(t.Context(), f.account, parent.ID)
	if err != nil {
		t.Fatal(err)
	}
	verified, audited := 0, 0
	f.handler.VerifyGitHub = func(context.Context, string, int64, int64, string) error { verified++; return nil }
	f.handler.Audit = func(_ context.Context, kind string, account *string, data map[string]any) {
		if kind != "ci.execution.rerun_admitted" || account == nil || *account != f.account || data["parent_execution_id"] != parent.ID || data["root_execution_id"] != parent.ID || len(data) != 3 {
			t.Fatal("rerun audit lost owned lineage or retained private request data")
		}
		audited++
	}
	for _, gate := range []string{"internal", "github", "dispatch", "worker", "transport", "readiness"} {
		f.handler.Config, f.handler.ExecutionReady = cfg, true
		switch gate {
		case "internal":
			f.handler.Config.Enabled = false
		case "github":
			f.handler.Config.GitHubEnabled = false
		case "dispatch":
			f.handler.Config.DispatchEnabled = false
		case "worker":
			f.handler.Config.WorkerEnabled = false
		case "transport":
			f.handler.Config.TransportEnabled = false
		case "readiness":
			f.handler.ExecutionReady = false
		}
		if w := f.call(t, ciapi.Handler.RerunExecution, f.account, "", parent.ID, "rerun", struct{}{}); w.Code != http.StatusNotFound || verified != 0 || audited != 0 {
			t.Fatalf("%s gate admitted rerun: %d", gate, w.Code)
		}
	}
	f.handler.Config, f.handler.ExecutionReady = cfg, true
	for _, body := range []any{nil, map[string]any{"commit_sha": strings.Repeat("b", 40)}, map[string]any{"profile_id": profiles[0].ID}, map[string]any{"templates": map[string]string{}}, map[string]any{"rerun": map[string]string{"root_execution_id": uuid.NewString()}}} {
		if w := f.call(t, ciapi.Handler.RerunExecution, f.account, "", parent.ID, "rerun", body); w.Code != http.StatusBadRequest {
			t.Fatalf("rerun accepted client configuration: %d", w.Code)
		}
	}
	if w := f.call(t, ciapi.Handler.RerunExecution, f.account, "", parent.ID, "", struct{}{}); w.Code == http.StatusAccepted {
		t.Fatal("rerun accepted missing request identity")
	}
	if w := f.call(t, ciapi.Handler.RerunExecution, f.other, "", parent.ID, "foreign", struct{}{}); w.Code != http.StatusNotFound || verified != 0 {
		t.Fatal("foreign parent exposed source authority")
	}
	if w := f.call(t, ciapi.Handler.RerunExecution, f.account, "", "../invalid", "invalid", struct{}{}); w.Code != http.StatusNotFound {
		t.Fatal("invalid parent identity accepted")
	}
	var first ci.Execution
	for i := range 2 {
		w := f.call(t, ciapi.Handler.RerunExecution, f.account, "", parent.ID, "rerun", struct{}{})
		var child ci.Execution
		if w.Code != http.StatusAccepted || json.Unmarshal(w.Body.Bytes(), &child) != nil || child.Rerun == nil || child.Rerun.ParentExecutionID != parent.ID || child.Compute != nil {
			t.Fatalf("rerun response: %d %s", w.Code, w.Body)
		}
		if i == 0 {
			first = child
		} else if first.ID != child.ID || w.Header().Get("Idempotency-Replayed") != "true" {
			t.Fatal("rerun replay lost receipt identity")
		}
		for _, private := range []string{"private/recovery-rootfs", "installation_id", "lease_token"} {
			if strings.Contains(w.Body.String(), private) {
				t.Fatal("rerun response exposed private execution authority")
			}
		}
	}
	if verified != 1 || audited != 1 {
		t.Fatal("receipt replay repeated provider admission or audit")
	}
	if w := f.call(t, ciapi.Handler.RerunExecution, f.account, "", first.ID, "active-rerun", struct{}{}); w.Code != http.StatusConflict || !strings.Contains(w.Body.String(), "ci_rerun_not_ready") {
		t.Fatal("active execution was rerun")
	}
	var executions, runs int
	if err := f.pool.QueryRow(t.Context(), "SELECT (SELECT count(*) FROM ci_executions),(SELECT count(*) FROM job_runs)").Scan(&executions, &runs); err != nil || executions != 2 || runs != 0 {
		t.Fatal("rerun request created native work or duplicate executions")
	}
	f.handler.Config.DispatchEnabled, f.handler.Config.WorkerEnabled, f.handler.Config.TransportEnabled, f.handler.Config.GitHubEnabled = false, false, false, false
	for _, action := range []ciapi.Action{ciapi.Handler.Execution, ciapi.Handler.ListExecutions} {
		w := f.call(t, action, f.account, "", first.ID, "", nil)
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"parent_execution_id":"`+parent.ID+`"`) {
			t.Fatal("disabled compute hid rerun history")
		}
	}
	if w := f.call(t, ciapi.Handler.RerunExecution, f.account, "", parent.ID, "another", struct{}{}); w.Code != http.StatusNotFound {
		t.Fatal("disabled compute admitted rerun")
	}
}
