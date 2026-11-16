package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/tasktoolrelease"
)

func TestMCPTaskRestoreCLINoApprovalNoWriteAndLostFilesReplay(t *testing.T) {
	stamp := time.Now().UTC()
	q := &api.TaskToolRecoveryQualification{HandoverID: strings.Repeat("3", 64), OperationID: strings.Repeat("2", 64), Revision: 1, Action: "restore", TargetDeploymentID: uuid.NewString(), ExpectedServingDeploymentID: uuid.NewString(), ObservedAt: stamp, ExpiresAt: stamp.Add(time.Minute)}
	r := api.TaskToolReleaseIntentRecord{ID: strings.Repeat("1", 64), AccountID: uuid.NewString(), Execution: &api.TaskToolReleaseExecution{OperationID: q.OperationID, Recovery: &api.TaskToolRecoveryHandover{ID: q.HandoverID, OperationID: q.OperationID, Revision: 1, State: "settled", Qualification: q}}}
	posts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != "GET" {
			posts++
			w.WriteHeader(500)
			return
		}
		_ = json.NewEncoder(w).Encode(r)
	}))
	defer server.Close()
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "fixture")
	t.Setenv("PATH", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_CAPABILITY_FLAGS_RELEASE_ALL", "1")
	t.Cleanup(main.ResetCapabilityView)
	digest := tasktoolrelease.QualificationDigest(*q)
	argv := []string{"--json", "mcp", "server", "tasks", "execution", "restore", "--id", r.ID, "--operation-id", q.OperationID, "--handover-id", q.HandoverID, "--qualification-sha256", digest, "--allow-tool-execution"}
	if code, _, _ := runCLI(t, argv...); code == 0 || posts != 0 {
		t.Fatal("missing owner approval dispatched restore")
	}
	r.Execution.Recovery.Decision = "approved"
	if code, _, _ := runCLI(t, argv...); code == 0 || posts != 0 {
		t.Fatal("missing private profile dispatched restore")
	}
	r.Execution.Recovery.Result = &api.TaskToolRecoveryResult{IntentID: r.ID, OperationID: q.OperationID, HandoverID: q.HandoverID, QualificationSHA256: digest, Revision: 1, Action: "restore", TargetDeploymentID: q.TargetDeploymentID, PreviousServingDeploymentID: q.ExpectedServingDeploymentID, EvidenceSHA256: strings.Repeat("4", 64), CheckedAt: stamp, CommittedAt: stamp}
	r.Execution.Recovery.Decision = "revoked"
	code, out, stderr := runCLI(t, argv...)
	if code != 0 || posts != 0 || !strings.Contains(out, "committed_at") {
		t.Fatalf("retained replay required local files: %d %s %s", code, out, stderr)
	}
}
