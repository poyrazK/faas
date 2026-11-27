// adr: 20261117215858
package mcpmanagement

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// taskFixture serves app tasks on top of the operation fixture.
type taskFixture struct {
	*operationFixture
	tmu      sync.Mutex
	created  []api.CreateAppTaskRequest
	keys     []string
	approval bool
	stdout   string
}

func newTaskFixture(t *testing.T) (*management, *taskFixture) {
	t.Helper()
	f := &taskFixture{operationFixture: &operationFixture{base: newLocalFixture(t, true, "release-base"), revision: 1, values: map[string]string{}}}
	backend := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(backend.Close)
	_, m, err := newManagementServer(api.NewClient(backend.URL, testToken), Options{APIBase: backend.URL, Token: testToken, AllowWrites: true, agentKey: true})
	if err != nil {
		t.Fatal(err)
	}
	return m, f
}

func (f *taskFixture) serve(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == "/v1/apps/alpha/tasks" && r.Method == http.MethodPost:
		f.tmu.Lock()
		defer f.tmu.Unlock()
		if f.approval {
			p := api.NewProblem(http.StatusPreconditionRequired, api.CodeApprovalRequired, "Approval required", "ask a person")
			p.AgentApproval = &api.AgentApprovalPendingInfo{ID: "approval-1", Category: "production", ReviewURL: "https://example.test/review"}
			api.WriteProblem(w, p)
			return
		}
		var req api.CreateAppTaskRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.created = append(f.created, req)
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task-1", AppID: f.base.app.ID, DeploymentID: "dep-1", Status: api.AppTaskStatusQueued, Command: req.Command})
	case r.URL.Path == "/v1/apps/alpha/tasks/task-1":
		f.tmu.Lock()
		defer f.tmu.Unlock()
		exit := 0
		_ = json.NewEncoder(w).Encode(api.AppTaskResponse{ID: "task-1", AppID: f.base.app.ID, DeploymentID: "dep-1", Status: api.AppTaskStatusSucceeded, ExitCode: &exit, StdoutTail: f.stdout})
	default:
		f.serveHTTP(w, r)
	}
}

func TestRunCommandPlanAdmitsOneTaskPerPlan(t *testing.T) {
	m, f := newTaskFixture(t)
	plan, err := m.planRunCommand(t.Context(), planRunCommandArgs{App: "alpha", Command: []string{"python", "manage.py", "migrate"}, Reason: "apply migrations"})
	if err != nil {
		t.Fatal(err)
	}
	p := plan.(releasePlan)
	if p.Action != "operation_run_command" || len(f.created) != 0 {
		t.Fatalf("plan action=%s created=%d", p.Action, len(f.created))
	}
	for range 2 {
		out, err := m.applyOperation(t.Context(), applyArgs{PlanID: p.ID})
		if err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(out)
		if !strings.Contains(string(raw), `"task_id":"task-1"`) {
			t.Fatalf("receipt = %s", raw)
		}
	}
	if len(f.created) != 1 || f.keys[0] != "mcp-operation-"+p.ID {
		t.Fatalf("tasks created=%d keys=%v", len(f.created), f.keys)
	}
	got := f.created[0]
	if got.Interactive || got.TTY || got.TimeoutSeconds != runCommandDefaultTimeout || got.MaxOutputBytes != runCommandDefaultOutput || strings.Join(got.Command, " ") != "python manage.py migrate" {
		t.Fatalf("task request = %+v", got)
	}
}

func TestRunCommandApprovalLeavesThePlanRetryable(t *testing.T) {
	m, f := newTaskFixture(t)
	f.approval = true
	plan, err := m.planRunCommand(t.Context(), planRunCommandArgs{App: "alpha", Shell: "rake db:migrate", Reason: "migrate"})
	if err != nil {
		t.Fatal(err)
	}
	id := plan.(releasePlan).ID
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err == nil {
		t.Fatal("approval_required hidden")
	}
	f.tmu.Lock()
	f.approval = false
	f.tmu.Unlock()
	if _, err := m.applyOperation(t.Context(), applyArgs{PlanID: id}); err != nil {
		t.Fatalf("approved retry refused: %v", err)
	}
	if len(f.created) != 1 || !f.created[0].CommandShell {
		t.Fatalf("created = %+v", f.created)
	}
}

func TestRunCommandPlanValidatesInput(t *testing.T) {
	m, _ := newTaskFixture(t)
	for _, a := range []planRunCommandArgs{
		{App: "alpha", Reason: "none"},
		{App: "alpha", Command: []string{"ls"}, Shell: "ls", Reason: "both"},
		{App: "alpha", Command: []string{""}, Reason: "empty program"},
		{App: "alpha", Command: []string{"ls"}, TimeoutSeconds: 901, Reason: "too long"},
		{App: "alpha", Command: []string{"ls"}, MaxOutputBytes: 512, Reason: "too small"},
		{App: "alpha", Shell: "echo \x00", Reason: "nul"},
		{App: "alpha", Command: []string{"ls"}},
	} {
		if _, err := m.planRunCommand(t.Context(), a); err == nil {
			t.Errorf("accepted %+v", a)
		}
	}
}

func TestCommandRunRedactsCredentialLines(t *testing.T) {
	m, f := newTaskFixture(t)
	f.stdout = "PATH=/usr/bin\nSTRIPE_SECRET_KEY=sk_" + "live_" + strings.Repeat("a", 32) + "\ndone"
	out, err := m.getCommandRun(t.Context(), commandRunArgs{App: "alpha", TaskID: "task-1"})
	if err != nil {
		t.Fatal(err)
	}
	view := out.(map[string]any)
	stdout := view["stdout_tail"].(string)
	if strings.Contains(stdout, "sk_live") || !strings.Contains(stdout, "PATH=/usr/bin") || !strings.Contains(stdout, "[redacted: ") || view["redacted_lines"] != 1 || view["exit_code"] != 0 {
		t.Fatalf("view = %+v", view)
	}
}
