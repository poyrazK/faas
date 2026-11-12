package journeys

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Agent approvals, the agent activity log and `gregale mcp connect`, as
// steps of the hosted MCP journey (row 160).
//
// An agent key that needs approval to delete asks to delete an app that does
// not exist: the approval gate answers before any handler runs
// (ADR-20261104140058), so the journey never deletes anything. Deciding the
// request needs a person signed in to the dashboard, which a headless run has
// not; the journey checks everything up to that decision: the 428 and its
// review link, the request waiting among the account's approvals, the CLI's
// --wait-for-approval and the MCP gregale_wait_for_approval tool both waiting
// on it. The key's own writes appear in its activity log
// (ADR-20261107220000), and `gregale mcp connect` writes a Codex config
// (ADR-20261109121411).

func (j *hostedMCPJourney) approvalSteps(s *story) {
	s.step("approval_key", j.approvalKey)
	s.step("delete_waits_for_approval", j.deleteWaitsForApproval)
	s.check("approval_pending_listed", j.approvalPendingListed)
	s.check("cli_waits_for_approval", j.cliWaitsForApproval)
	s.check("mcp_waits_for_approval", j.mcpWaitsForApproval)
	s.check("activity_recorded", j.activityRecorded)
	s.check("mcp_connect_codex", j.mcpConnectCodex)
}

// approvalKey mints an agent key that may deploy and needs a person's
// approval only to delete.
func (j *hostedMCPJourney) approvalKey(t *testing.T) {
	res := j.tg.ok(t, time.Minute, "keys", "add", j.tg.slug("mcp-approvals"), "--scopes", "agent,deploy:write", "--approvals", "delete")
	j.approvalKeyValue = apiKeyRe.FindString(string(res.stdout))
	id := str(object(t, res.stdout), "id")
	if j.approvalKeyValue == "" || id == "" {
		t.Fatal("key create printed no key or id")
	}
	j.root.Cleanup(func() { j.tg.run(j.root, time.Minute, "key", "delete", id) })
	j.ghost = j.tg.slug("mcp-ghost")
}

// deleteWaitsForApproval: the delete is answered 428 approval_required with
// the request's id and review link.
func (j *hostedMCPJourney) deleteWaitsForApproval(t *testing.T) {
	r := j.tg.apiCall(http.MethodDelete, "/v1/apps/"+j.ghost, nil, j.approvalKeyValue)
	if r.status != http.StatusPreconditionRequired {
		t.Fatalf("agent DELETE /v1/apps/%s = %s, want 428", j.ghost, r)
	}
	problem := jsonBody(t, r)
	if str(problem, "code") != "approval_required" {
		t.Fatalf("problem code %q, want approval_required: %s", str(problem, "code"), compact(problem))
	}
	j.approvalID = str(problem, "agent_approval.id")
	if j.approvalID == "" || !strings.Contains(str(problem, "agent_approval.review_url"), j.approvalID) || str(problem, "agent_approval.category") != "delete" {
		t.Fatalf("428 names no delete approval with a review link: %s", compact(problem))
	}
}

// approvalPendingListed: the request waits among the account's approvals.
func (j *hostedMCPJourney) approvalPendingListed(t *testing.T) {
	admin := j.adminKey(t)
	list := jsonBody(t, j.tg.apiCall(http.MethodGet, "/v1/agent-approvals", nil, admin))
	for _, item := range asList(list["items"]) {
		if str(item, "id") == j.approvalID {
			if str(item, "status") != "pending" {
				t.Fatalf("approval %s is %q, want pending", j.approvalID, str(item, "status"))
			}
			return
		}
	}
	t.Fatalf("GET /v1/agent-approvals does not list %s: %s", j.approvalID, compact(list))
}

// cliWaitsForApproval: with --wait-for-approval the CLI prints the review
// link and waits for the decision instead of failing; the journey stops it.
func (j *hostedMCPJourney) cliWaitsForApproval(t *testing.T) {
	res := j.tg.runAs(t, j.approvalKeyValue, 20*time.Second, "--wait-for-approval", "app", "delete", j.ghost, "--yes")
	stderr := string(res.stderr)
	if !strings.Contains(stderr, "Waiting for approval (delete)") || !strings.Contains(stderr, "Review: http") {
		t.Fatalf("gregale --wait-for-approval app delete printed no wait and review link (exit %d): %s", res.exit, tail(stderr, 400))
	}
	if res.elapsed < 15*time.Second {
		t.Fatalf("the CLI returned after %s (exit %d) instead of waiting: %s", res.elapsed, res.exit, tail(stderr, 400))
	}
}

// mcpWaitsForApproval: the agent key's MCP session lists
// gregale_wait_for_approval, which reports the request still pending and
// tells the agent to call it again.
func (j *hostedMCPJourney) mcpWaitsForApproval(t *testing.T) {
	session := openMCP(t, j.endpoint, j.approvalKeyValue)
	if !session.toolNames(t)["gregale_wait_for_approval"] {
		t.Fatal("the agent's core catalog lists no gregale_wait_for_approval")
	}
	var out struct {
		Status   string `json:"status"`
		Decided  bool   `json:"decided"`
		Recovery struct {
			Action string `json:"action"`
			Tool   string `json:"tool"`
		} `json:"recovery"`
	}
	result := session.call(t, "tools/call", map[string]any{"name": "gregale_wait_for_approval", "arguments": map[string]any{"approval_id": j.approvalID, "wait_seconds": 1}})
	raw, _ := json.Marshal(result["structuredContent"])
	if field(result, "isError") == true || json.Unmarshal(raw, &out) != nil {
		t.Fatalf("gregale_wait_for_approval = %s", compact(result))
	}
	if out.Status != "pending" || out.Decided || out.Recovery.Action != "await_approval" || out.Recovery.Tool != "gregale_wait_for_approval" {
		t.Fatalf("gregale_wait_for_approval = %+v, want pending with await_approval", out)
	}
}

// activityRecorded: a write the key needs no approval for reaches the API
// and appears in the key's own activity log; the gated delete does not.
func (j *hostedMCPJourney) activityRecorded(t *testing.T) {
	patch := j.tg.apiCall(http.MethodPatch, "/v1/apps/"+j.ghost, map[string]any{}, j.approvalKeyValue)
	if patch.err != nil || patch.status == http.StatusPreconditionRequired {
		t.Fatalf("agent PATCH /v1/apps/%s = %s; want it past the approval gate", j.ghost, patch)
	}
	want := "/v1/apps/" + j.ghost
	eventually(t, "the agent's PATCH in its activity log", time.Minute, 5*time.Second, func() (bool, string) {
		res := j.tg.runAs(t, j.approvalKeyValue, time.Minute, "mcp", "connection", "activity", "--self", "--limit", "20")
		if res.exit != 0 {
			return false, res.problem()
		}
		var page struct {
			Activity []struct {
				Method, Path string
				Status       int
			} `json:"activity"`
		}
		if err := json.Unmarshal(res.stdout, &page); err != nil {
			return false, err.Error()
		}
		for _, a := range page.Activity {
			if a.Path == want && a.Method == http.MethodDelete {
				t.Fatalf("the activity log records the delete that waits for approval: %+v", a)
			}
			if a.Path == want && a.Method == http.MethodPatch && a.Status == patch.status {
				return true, ""
			}
		}
		return false, string(res.stdout)
	})
}

// mcpConnectCodex: `gregale mcp connect --client codex` writes the hosted
// server into a Codex config, checks the endpoint asks the agent to sign in,
// and a second run finds it already connected.
func (j *hostedMCPJourney) mcpConnectCodex(t *testing.T) {
	config := filepath.Join(j.tg.home, ".codex", "config.toml")
	t.Cleanup(func() { _ = os.Remove(config) })
	connect := func() map[string]any {
		t.Helper()
		res := j.tg.runAs(t, j.approvalKeyValue, time.Minute, "mcp", "connect", "--client", "codex", "--toolsets", "core,operate")
		if res.exit != 0 {
			t.Fatalf("gregale mcp connect = exit %d: %s", res.exit, res.problem())
		}
		return object(t, res.stdout)
	}
	first := connect()
	clients := asList(first["clients"])
	if len(clients) != 1 || str(clients[0], "status") != "connected" || !strings.HasPrefix(str(first, "endpoint_check"), "reachable") {
		t.Fatalf("mcp connect = %s, want codex connected and the endpoint reachable", compact(first))
	}
	data, err := os.ReadFile(config)
	if err != nil {
		t.Fatalf("read %s: %v", config, err)
	}
	if !strings.Contains(string(data), "[mcp_servers.gregale]") || !strings.Contains(string(data), `url = "`+j.endpoint+`"`) || !strings.Contains(string(data), "core,operate") {
		t.Fatalf("%s =\n%s", config, data)
	}
	if again := asList(connect()["clients"]); len(again) != 1 || str(again[0], "status") != "already_connected" {
		t.Fatalf("second mcp connect = %v, want already_connected", again)
	}
}

// adminKey is the journey's admin key for account-wide reads, minted once.
func (j *hostedMCPJourney) adminKey(t *testing.T) string {
	if j.admin == "" {
		j.admin, _ = j.tg.scopedKeyWithID(t, j.root, "mcp-admin", "admin")
	}
	return j.admin
}
