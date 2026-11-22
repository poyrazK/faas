package journeys

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// Agent budgets, budget previews and `gregale mcp connection status`, as
// steps of the hosted MCP journey (row 160, ADR-20261111114059).
//
// An agent key with a zero monthly budget asks to give the shared probe app
// one more warm instance. A budget preview prices the change without making
// it; the real request waits for a person's budget approval, which the
// journey never decides, so the probe keeps its floor and nothing is spent.
// The agent reads its own budget through gregale_agent_budget, and the
// key's owner sees the budget and the waiting request in
// `gregale mcp connection status`.

func (j *hostedMCPJourney) budgetSteps(s *story) {
	s.step("budget_key", j.budgetKey)
	s.check("budget_preview_prices_floor", j.budgetPreviewPricesFloor)
	s.check("over_budget_waits_for_approval", j.overBudgetWaitsForApproval)
	s.check("budget_tool_reports", j.budgetToolReports)
	s.check("connection_status_shows_budget", j.connectionStatusShowsBudget)
}

// budgetKey mints an agent key that may deploy, needs approval only to
// delete, and has a zero monthly budget for always-on capacity.
func (j *hostedMCPJourney) budgetKey(t *testing.T) {
	if j.probe == "" {
		t.Skip("no probe app")
	}
	res := j.tg.ok(t, time.Minute, "keys", "add", j.tg.slug("mcp-budget"), "--scopes", "agent,deploy:write", "--approvals", "delete")
	j.budgetKeyValue = apiKeyRe.FindString(string(res.stdout))
	id := str(object(t, res.stdout), "id")
	if j.budgetKeyValue == "" || id == "" {
		t.Fatal("key create printed no key or id")
	}
	j.root.Cleanup(func() { j.tg.run(j.root, time.Minute, "key", "delete", id) })
	j.budgetKeyID = id
	budget := object(t, j.tg.ok(t, time.Minute, "mcp", "connection", "budget", id, "--set", "0").stdout)
	if !isZeroNum(budget, "monthly_cents") {
		t.Fatalf("budget after --set 0 = %s", compact(budget))
	}
}

// probeFloor reads the probe app's min_instances through the admin key. The
// API omits a zero floor (min_instances is omitempty), the state of a
// scale-to-zero app, so a missing field is 0.
func (j *hostedMCPJourney) probeFloor(t *testing.T) int {
	t.Helper()
	got := jsonBody(t, j.tg.apiCall(http.MethodGet, "/v1/apps/"+j.probe, nil, j.adminKey(t)))
	raw, present := got["min_instances"]
	if !present {
		return 0
	}
	// jsonBody decodes numbers as json.Number.
	n, ok := raw.(json.Number)
	floor, err := n.Int64()
	if !ok || err != nil {
		t.Fatalf("GET /v1/apps/%s has a non-numeric min_instances: %s", j.probe, compact(got))
	}
	return int(floor)
}

// raiseFloor sends the agent's PATCH adding one warm instance to the probe,
// as a budget preview or for real.
func (j *hostedMCPJourney) raiseFloor(t *testing.T, floor int, preview bool) httpResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"min_instances": floor + 1})
	header := map[string]string{
		"Authorization": "Bearer " + j.budgetKeyValue,
		"Content-Type":  "application/json",
		"User-Agent":    "gregale-e2e-journeys",
	}
	if preview {
		header["Gregale-Agent-Preview"] = "budget"
	}
	return send(http.MethodPatch, j.tg.api+"/v1/apps/"+j.probe, header, body)
}

// budgetPreviewPricesFloor: a budget preview of the extra warm instance
// answers its monthly cost, past the zero budget, and that a person would
// be asked; the probe keeps its floor.
func (j *hostedMCPJourney) budgetPreviewPricesFloor(t *testing.T) {
	if j.budgetKeyValue == "" {
		t.Skip("no budget key")
	}
	floor := j.probeFloor(t)
	r := j.raiseFloor(t, floor, true)
	if r.err != nil || r.status != http.StatusOK {
		t.Fatalf("budget preview PATCH /v1/apps/%s = %s: %s", j.probe, r, tail(string(r.body), 400))
	}
	out := jsonBody(t, r)
	if field(out, "preview") != true || field(out, "charged") != true || num(out, "added_cents") <= 0 ||
		field(out, "needs_approval") != true || str(out, "approval_category") != "budget" || !isZeroNum(out, "budget_cents") {
		t.Fatalf("budget preview = %s; want a charged cost past a zero budget, needing a budget approval", compact(out))
	}
	if got := j.probeFloor(t); got != floor {
		t.Fatalf("probe min_instances after a preview = %d, want %d", got, floor)
	}
}

// overBudgetWaitsForApproval: sent for real, the change waits for a
// person's budget approval and the probe keeps its floor.
func (j *hostedMCPJourney) overBudgetWaitsForApproval(t *testing.T) {
	if j.budgetKeyValue == "" {
		t.Skip("no budget key")
	}
	floor := j.probeFloor(t)
	r := j.raiseFloor(t, floor, false)
	if r.status != http.StatusPreconditionRequired {
		t.Fatalf("agent PATCH min_instances on a zero budget = %s, want 428: %s", r, tail(string(r.body), 400))
	}
	problem := jsonBody(t, r)
	if str(problem, "code") != "approval_required" || str(problem, "agent_approval.category") != "budget" || str(problem, "agent_approval.id") == "" {
		t.Fatalf("428 names no budget approval: %s", compact(problem))
	}
	j.budgetApprovalID = str(problem, "agent_approval.id")
	if got := j.probeFloor(t); got != floor {
		t.Fatalf("probe min_instances after the refused change = %d, want %d", got, floor)
	}
}

// budgetToolReports: the agent's core catalog lists gregale_agent_budget,
// which reports its zero budget and nothing spent.
func (j *hostedMCPJourney) budgetToolReports(t *testing.T) {
	if j.budgetKeyValue == "" {
		t.Skip("no budget key")
	}
	session := openMCP(t, j.endpoint, j.budgetKeyValue)
	if !session.toolNames(t)["gregale_agent_budget"] {
		t.Fatal("the agent's core catalog lists no gregale_agent_budget")
	}
	result := session.call(t, "tools/call", map[string]any{"name": "gregale_agent_budget", "arguments": map[string]any{}})
	sc, _ := result["structuredContent"].(map[string]any)
	if field(result, "isError") == true || sc == nil || !isZeroNum(sc, "monthly_cents") || !isZeroNum(sc, "spent_cents") {
		t.Fatalf("gregale_agent_budget = %s; want a zero budget, nothing spent", compact(result))
	}
}

// connectionStatusShowsBudget: the key's owner sees its zero budget, and
// the budget approval it waits for, in `gregale mcp connection status`.
func (j *hostedMCPJourney) connectionStatusShowsBudget(t *testing.T) {
	if j.budgetKeyID == "" {
		t.Skip("no budget key")
	}
	out := object(t, j.tg.ok(t, time.Minute, "mcp", "connection", "status", j.budgetKeyID).stdout)
	items := asList(out["items"])
	if len(items) != 1 || !isZeroNum(items[0], "budget.monthly_cents") {
		t.Fatalf("mcp connection status = %s; want the key with a zero budget", compact(out))
	}
	if j.budgetApprovalID == "" {
		return
	}
	for _, a := range asList(field(items[0], "pending_approvals")) {
		if str(a, "id") == j.budgetApprovalID && str(a, "category") == "budget" {
			return
		}
	}
	t.Fatalf("mcp connection status lists no pending budget approval %s: %s", j.budgetApprovalID, compact(out))
}
