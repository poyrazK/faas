package journeys

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
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
// on it. Given a signed-in session (GREGALE_E2E_SESSION) it also approves the
// request on the review page and checks the agent's retry goes through once. The key's own writes appear in its activity log
// (ADR-20261107220000), and `gregale mcp connect` writes a Codex config
// (ADR-20261109121411).

func (j *hostedMCPJourney) approvalSteps(s *story) {
	s.step("approval_key", j.approvalKey)
	s.step("delete_waits_for_approval", j.deleteWaitsForApproval)
	s.check("approval_pending_listed", j.approvalPendingListed)
	s.check("cli_waits_for_approval", j.cliWaitsForApproval)
	s.check("mcp_waits_for_approval", j.mcpWaitsForApproval)
	s.check("approve_and_retry", j.approveAndRetry)
	s.check("activity_recorded", j.activityRecorded)
	s.check("undo_restores_setting", j.undoRestoresSetting)
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
	j.approvalKeyID = id
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
	j.reviewURL = str(problem, "agent_approval.review_url")
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

var csrfFieldRe = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)

// approveAndRetry: a person approves the waiting delete on its review page,
// the request reads approved, and the agent's retry passes the gate once:
// the app never existed, so the delete itself answers 404, and the approval
// is consumed. Approving needs a signed-in session, which only a person has:
// GREGALE_E2E_SESSION is the dashboard's __Host-faas_sid cookie.
func (j *hostedMCPJourney) approveAndRetry(t *testing.T) {
	if j.tg.session == "" {
		t.Skip("approving needs a signed-in session: set GREGALE_E2E_SESSION to the dashboard's __Host-faas_sid cookie")
	}
	review, err := url.Parse(j.reviewURL)
	if err != nil || review.Host == "" {
		t.Fatalf("the 428 gave no usable review link: %q", j.reviewURL)
	}
	page := fetch(http.MethodGet, j.reviewURL, map[string]string{"Cookie": "__Host-faas_sid=" + j.tg.session})
	match := csrfFieldRe.FindSubmatch(page.body)
	if page.err != nil || page.status != http.StatusOK || match == nil {
		t.Fatalf("review page = %s; want 200 with the approve form (is GREGALE_E2E_SESSION a live session of the key's owner?): %s", page, tail(string(page.body), 400))
	}
	csrf := string(match[1])
	form := url.Values{"agent_approval": {j.approvalID}, "csrf_token": {csrf}, "decision": {"approve"}}
	origin := review.Scheme + "://" + review.Host
	decided := send(http.MethodPost, origin+review.Path, map[string]string{
		"Cookie":       "__Host-faas_sid=" + j.tg.session + "; faas_csrf=" + csrf,
		"Content-Type": "application/x-www-form-urlencoded",
		"Origin":       origin,
		"Referer":      j.reviewURL,
	}, []byte(form.Encode()))
	if decided.err != nil || decided.status != http.StatusOK || !strings.Contains(string(decided.body), "You approved this request") {
		t.Fatalf("approve = %s: %s", decided, tail(string(decided.body), 400))
	}
	j.approvalStatusIs(t, "approved")
	retry := j.tg.apiCall(http.MethodDelete, "/v1/apps/"+j.ghost, nil, j.approvalKeyValue)
	if retry.err != nil || retry.status != http.StatusNotFound {
		t.Fatalf("agent retry DELETE /v1/apps/%s = %s, want 404 past the approval gate", j.ghost, retry)
	}
	j.approvalStatusIs(t, "consumed")
	again := j.tg.apiCall(http.MethodDelete, "/v1/apps/"+j.ghost, nil, j.approvalKeyValue)
	if again.status != http.StatusPreconditionRequired {
		t.Fatalf("a second DELETE after the approval was used = %s, want a new 428", again)
	}
}

// approvalStatusIs reads the request through the admin key.
func (j *hostedMCPJourney) approvalStatusIs(t *testing.T, want string) {
	t.Helper()
	got := jsonBody(t, j.tg.apiCall(http.MethodGet, "/v1/agent-approvals/"+j.approvalID, nil, j.adminKey(t)))
	if str(got, "status") != want {
		t.Fatalf("approval %s is %q, want %q: %s", j.approvalID, str(got, "status"), want, compact(got))
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

// undoRestoresSetting: the agent changes the probe app's idle timeout, and
// `gregale mcp connection undo --entry` puts it back (ADR-20261112080601).
func (j *hostedMCPJourney) undoRestoresSetting(t *testing.T) {
	if j.probe == "" {
		t.Skip("no probe app")
	}
	read := func() int {
		t.Helper()
		got := jsonBody(t, j.tg.apiCall(http.MethodGet, "/v1/apps/"+j.probe, nil, j.adminKey(t)))
		n, _ := got["idle_timeout_s"].(float64)
		return int(n)
	}
	before := read()
	changed := 120
	if before == changed {
		changed = 180
	}
	patch := j.tg.apiCall(http.MethodPatch, "/v1/apps/"+j.probe, map[string]any{"idle_timeout_s": changed}, j.approvalKeyValue)
	if patch.err != nil || patch.status != http.StatusOK {
		t.Fatalf("agent PATCH idle_timeout_s = %s", patch)
	}
	t.Cleanup(func() {
		if read() != before {
			j.tg.apiCall(http.MethodPatch, "/v1/apps/"+j.probe, map[string]any{"idle_timeout_s": before}, j.adminKey(t))
		}
	})
	var entry string
	eventually(t, "the agent's change in its activity log", time.Minute, 5*time.Second, func() (bool, string) {
		res := j.tg.runAs(t, j.approvalKeyValue, time.Minute, "mcp", "connection", "activity", "--self", "--limit", "20")
		var page struct {
			Activity []struct {
				ID, Method, Path string
			} `json:"activity"`
		}
		if res.exit != 0 || json.Unmarshal(res.stdout, &page) != nil {
			return false, res.problem()
		}
		for _, row := range page.Activity {
			if row.Method == http.MethodPatch && row.Path == "/v1/apps/"+j.probe {
				entry = row.ID
				return true, ""
			}
		}
		return false, string(res.stdout)
	})
	res := j.tg.ok(t, time.Minute, "mcp", "connection", "undo", j.approvalKeyID, "--entry", entry, "--yes")
	var out struct {
		Undone int `json:"undone"`
		Failed int `json:"failed"`
	}
	if json.Unmarshal(res.stdout, &out) != nil || out.Undone != 1 || out.Failed != 0 {
		t.Fatalf("undo = %s; want one change undone", tail(string(res.stdout), 400))
	}
	if got := read(); got != before {
		t.Fatalf("idle_timeout_s after undo = %d, want %d", got, before)
	}
}
