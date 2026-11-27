//go:build metal

package runlifecycle_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestSourceScopedReportFileMetal stages a customer CSV that exceeds the RAP
// envelope, then replays the admitted tool call after revocation and restart.
func TestSourceScopedReportFileMetal(t *testing.T) {
	t.Setenv("FAAS_RUN_TASK_QUALIFICATION", "1")
	f := newRunFixture(t)
	ctx, s := t.Context(), state.NewPgStore(f.h.Pool)
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: f.accountID, Slug: "scoped-report-file", Status: state.AppActive})
	check(err)
	ep, err := s.CreateManagedRealtimeEndpointIfUnderQuota(ctx, state.ManagedRealtimeEndpoint{
		ID: uuid.NewString(), AccountID: f.accountID, AppID: app.ID, CallbackURL: "https://example.test/report",
		ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect",
		CallbackAuthTokenSealed: []byte("fixture"), AuthTokenSealed: []byte("fixture"), Enabled: true,
	}, 10, 50)
	check(err)
	node := os.Getenv("FAAS_TEST_REPORT_AGENT_NODE")
	out, err := exec.CommandContext(ctx, node, "../../../examples/gregale-run/test/hosted-worker.mjs", "fixtures", ep.ID).Output()
	check(err)
	var fixtures struct {
		Tasks []api.PublishExecutionTaskRequest
	}
	check(json.Unmarshal(out, &fixtures))
	if len(fixtures.Tasks) != 2 {
		t.Fatal("missing Report Agent sales task")
	}
	def := fixtures.Tasks[0]
	// Run deadlines start at admission, including the deliberate daemon outage.
	def.Definition.Limits.TimeoutMS = 20_000
	_, err = f.client.PublishExecutionTask(ctx, def)
	check(err)

	var csv strings.Builder
	csv.WriteString("order_id,region,quantity,unit_price_cents\n")
	const orders = 1024
	for i := 0; i < orders; i++ {
		fmt.Fprintf(&csv, "fixture-order-%d,north,2,123\n", i)
	}
	contents := csv.String()
	digest := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(contents)))
	file, err := f.client.PrepareExecutionFile(ctx, api.CreateExecutionFileRequest{SizeBytes: int64(len(contents)), SHA256: digest})
	check(err)
	_, err = f.client.UploadExecutionFile(ctx, file.ID, strings.NewReader(contents), int64(len(contents)), digest)
	check(err)
	const thread, call = "report-file-thread", "assistant:uploaded-1"
	grant, err := f.client.CreateAgentExecutionFileGrant(ctx, file.ID, api.CreateAgentExecutionFileGrantRequest{
		EndpointID: ep.ID, ThreadID: thread, ToolCallID: call, TaskName: def.Name, Version: def.Version, Path: "orders.csv",
	})
	check(err)
	var callback api.AgentCallbackResponse
	check(json.Unmarshal(taskHTTP(t, f, "POST", "/v1/agent-callbacks",
		api.CreateAgentCallbackRequest{EndpointID: ep.ID, ThreadID: thread, Kinds: []string{api.AgentCallbackKindToolResult}}, 201), &callback))
	invocation := map[string]any{"operation": "gregale_task", "id": call, "group_id": thread, "callback_url": callback.URL,
		"arguments": map[string]any{"name": def.Name, "version": def.Version, "input": map[string]string{"csv_file": "orders.csv"},
			"file_grants": []string{grant.GrantToken}}}

	check(f.h.KillSchedd())
	taskHTTP(t, f, "POST", "/v1/agent-tools/invoke", invocation, 200)
	runs, err := s.ListExecutions(ctx, f.accountID, 10, 0)
	check(err)
	if len(runs) != 1 {
		t.Fatalf("expected one admitted file-based Run, found %d", len(runs))
	}
	id := runs[0].ID
	f.runs[id] = true
	check(f.client.DeleteExecutionFile(ctx, file.ID))
	check(f.h.RestartAPID())
	taskHTTP(t, f, "POST", "/v1/agent-tools/invoke", invocation, 200)
	check(f.h.RestartSchedd())
	result := f.wait(t, id, api.ExecutionStatusSucceeded)
	var summary struct {
		Orders  int `json:"orders"`
		Revenue int `json:"revenue_cents"`
	}
	check(json.Unmarshal(result.Result, &summary))
	if result.ContractFailed() || summary.Orders != orders || summary.Revenue != orders*246 || len(result.Artifacts) != 2 {
		t.Fatal("Run did not process the exact uploaded CSV")
	}
	f.assertSettled(t)
	t.Logf("scoped Report file witness: bytes=%d orders=%d runs=1 apid_restart=true upload_revoked_before_boot=true", len(contents), summary.Orders)
}
