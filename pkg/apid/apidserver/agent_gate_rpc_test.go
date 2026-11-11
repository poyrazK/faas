// adr: 20261111150000
package apidserver_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A gateway's debugger tunnel is gated by apid: an agent key's first attach
// waits for a person, and one approval then covers that tunnel for
// api.DevDebugApprovalWindow instead of one connection. Another app's
// tunnel, an ungated route and an ordinary key are not affected, and the
// gateway's activity rows land in apid's log for the account's own key only.
func TestAgentGateForGateways_DebuggerApprovalCoversAWindow(t *testing.T) {
	srv, store := apidserver.NewCliAuthTestServer(t)
	ctx := t.Context()
	const email = "debug-approver@example.com"
	acct, err := store.CreateAccount(ctx, email, api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	mint := func(label string, scopes ...string) state.APIKey {
		_, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		key, err := store.CreateAPIKey(ctx, acct.ID, hash, label, scopes)
		if err != nil {
			t.Fatalf("CreateAPIKey %s: %v", label, err)
		}
		return key
	}
	agentKey := mint("debug-agent", api.ScopeAgent, api.ScopeAppsRead, api.ScopeDeployWrite)
	agents := apidserver.AgentRequestsForTest(t, srv)
	const pattern = "GET /v1/apps/{slug}/debug"
	tunnel := func(key state.APIKey, path string) state.AgentApprovalBinding {
		return state.AgentApprovalBinding{KeyID: key.ID, Method: http.MethodGet, Path: path}
	}

	allow, _, problem, err := agents.DecideAgentRequest(ctx, acct.ID, agentKey.ID, pattern, tunnel(agentKey, "/v1/apps/dev-svc/debug"), "attach debugger")
	if err != nil || allow || problem == nil || problem.Status != http.StatusPreconditionRequired || problem.AgentApproval == nil ||
		problem.AgentApproval.Category != api.AgentApprovalCredentials || !strings.Contains(problem.Detail, api.DevDebugApprovalWindow.String()) {
		t.Fatalf("first attach = %v, %+v, %v; want 428 credentials approval naming the window", allow, problem, err)
	}
	approvalID := problem.AgentApproval.ID
	_, cookie, form := apidserver.RenderAgentApprovalForTestAs(t, srv, approvalID, email)
	if approved := apidserver.DecideAgentApprovalForTest(t, srv, approvalID, cookie, form, "approve"); approved.Code != http.StatusOK {
		t.Fatalf("approve = %d %s", approved.Code, approved.Body)
	}
	for attach := range 3 {
		allow, usedID, problem, err := agents.DecideAgentRequest(ctx, acct.ID, agentKey.ID, pattern, tunnel(agentKey, "/v1/apps/dev-svc/debug"), "")
		if err != nil || !allow || usedID != approvalID {
			t.Fatalf("attach %d after approval = %v %q %+v %v; want allowed on %s", attach, allow, usedID, problem, err, approvalID)
		}
	}
	if allow, _, problem, err := agents.DecideAgentRequest(ctx, acct.ID, agentKey.ID, pattern, tunnel(agentKey, "/v1/apps/other-svc/debug"), ""); err != nil || allow ||
		problem == nil || problem.Status != http.StatusPreconditionRequired || problem.AgentApproval.ID == approvalID {
		t.Fatalf("another app's tunnel = %v %+v %v; want its own approval", allow, problem, err)
	}
	if allow, _, _, err := agents.DecideAgentRequest(ctx, acct.ID, agentKey.ID, "GET /v1/apps/{slug}", tunnel(agentKey, "/v1/apps/dev-svc"), ""); err != nil || !allow {
		t.Fatalf("an ungated read = %v %v; want allowed", allow, err)
	}
	admin := mint("admin", api.ScopeAdmin)
	if allow, _, _, err := agents.DecideAgentRequest(ctx, acct.ID, admin.ID, pattern, tunnel(admin, "/v1/apps/dev-svc/debug"), ""); err != nil || !allow {
		t.Fatalf("an ordinary key = %v %v; want allowed", allow, err)
	}
	if _, _, _, err := agents.DecideAgentRequest(ctx, "00000000-0000-0000-0000-000000000000", agentKey.ID, pattern, tunnel(agentKey, "/v1/apps/dev-svc/debug"), ""); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another account's key = %v; want ErrNotFound", err)
	}

	// The window is the first use plus api.DevDebugApprovalWindow: past it,
	// nothing covers the tunnel any more.
	if _, open, err := store.AgentApprovalUsedWithin(ctx, tunnel(agentKey, "/v1/apps/dev-svc/debug"), time.Nanosecond); err != nil || open {
		t.Fatalf("a window that has closed = %v %v; want closed", open, err)
	}

	row := state.AgentActivity{KeyID: agentKey.ID, AccountID: acct.ID, Method: http.MethodGet, Path: "/v1/apps/dev-svc/debug",
		Status: http.StatusSwitchingProtocols, ApprovalID: approvalID, CreatedAt: time.Now().UTC()}
	if err := agents.RecordAgentActivity(ctx, row); err != nil {
		t.Fatalf("RecordAgentActivity: %v", err)
	}
	rows, err := store.ListAgentActivity(ctx, agentKey.ID, time.Now().Add(-time.Hour), state.AgentActivityCursor{}, 10)
	if err != nil || len(rows) != 1 || rows[0].Status != http.StatusSwitchingProtocols || rows[0].ApprovalID != approvalID {
		t.Fatalf("activity = %+v %v; want the attach on its approval", rows, err)
	}
	row.AccountID = "00000000-0000-0000-0000-000000000000"
	if err := agents.RecordAgentActivity(ctx, row); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("a row for another account's key = %v; want ErrNotFound", err)
	}
}
