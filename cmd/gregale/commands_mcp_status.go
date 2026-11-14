package main

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// agentStatusUndoWindow is how far back the status counts undoable changes:
// the whole time activity is kept (ADR-20261112080601).
const agentStatusUndoWindow = 7 * 24 * time.Hour

// agentConnectionStatus is one connected agent at a glance: what it changed
// last, what waits for a person, what it committed against its budget and
// what can be undone.
type agentConnectionStatus struct {
	Connection api.AgentConnectionResponse `json:"connection"`
	LastChange *api.AgentActivityEntry     `json:"last_change,omitempty"`
	// Pending are its approval requests waiting for a person.
	Pending []api.AgentApprovalResponse `json:"pending_approvals"`
	Budget  *api.AgentBudgetResponse    `json:"budget,omitempty"`
	// Undoable counts its changes of the last 7 days undo can reverse.
	Undoable *int `json:"undoable,omitempty"`
	// Unavailable names what could not be read, so a gap is not mistaken
	// for "nothing".
	Unavailable []string `json:"unavailable,omitempty"`
}

// cmdMCPConnectionStatus prints every connected agent's status, or one's in
// detail: gregale mcp connection status [<id>].
func cmdMCPConnectionStatus(args []string) int {
	if len(args) > 1 {
		return printErr("MCP connection status", errors.New("use connection status, or connection status <id>"))
	}
	client, err := authedClient()
	if err != nil {
		return printErr("Not logged in", err)
	}
	ctx := context.Background()
	connections, err := client.ListAgentConnections(ctx)
	if err != nil {
		return printErr("Could not list connected agents", err)
	}
	items := connections.Items
	if len(args) == 1 {
		items = nil
		for _, c := range connections.Items {
			if c.ID == args[0] {
				items = append(items, c)
			}
		}
		if len(items) == 0 {
			return printErr("MCP connection status", fmt.Errorf("no connected agent %q; list them with `gregale mcp connection list`", args[0]))
		}
	}
	pending := map[string][]api.AgentApprovalResponse{}
	approvalsRead := true
	if approvals, err := client.ListAgentApprovals(ctx); err != nil {
		approvalsRead = false
	} else {
		for _, a := range approvals.Items {
			if a.Status == api.AgentApprovalPending && a.ConnectionID != "" {
				pending[a.ConnectionID] = append(pending[a.ConnectionID], a)
			}
		}
	}
	statuses := make([]agentConnectionStatus, 0, len(items))
	for _, c := range items {
		statuses = append(statuses, readAgentStatus(ctx, client, c, pending[c.ID], approvalsRead))
	}
	if jsonOutput {
		return jsonOut(writeJSON(map[string]any{"items": statuses}))
	}
	if len(args) == 1 {
		printAgentStatusDetail(statuses[0])
		return 0
	}
	printAgentStatusTable(statuses)
	return 0
}

func readAgentStatus(ctx context.Context, client *Client, c api.AgentConnectionResponse, pending []api.AgentApprovalResponse, approvalsRead bool) agentConnectionStatus {
	out := agentConnectionStatus{Connection: c, Pending: pending}
	if out.Pending == nil {
		out.Pending = []api.AgentApprovalResponse{}
	}
	if !approvalsRead {
		out.Unavailable = append(out.Unavailable, "approvals")
	}
	if activity, err := client.AgentConnectionActivity(ctx, c.ID, "", 1); err != nil {
		out.Unavailable = append(out.Unavailable, "activity")
	} else if len(activity.Activity) > 0 {
		out.LastChange = &activity.Activity[0]
	}
	if budget, err := client.AgentConnectionBudget(ctx, c.ID); err != nil {
		out.Unavailable = append(out.Unavailable, "budget")
	} else {
		out.Budget = &budget
	}
	since := time.Now().Add(-agentStatusUndoWindow).UTC().Format(time.RFC3339)
	if plan, err := client.PlanAgentUndo(ctx, c.ID, api.AgentUndoRequest{Since: since}); err != nil {
		out.Unavailable = append(out.Unavailable, "undo")
	} else {
		n := plan.Undoable
		out.Undoable = &n
	}
	return out
}

func agentStatusName(c api.AgentConnectionResponse) string {
	if c.ClientName != "" {
		return c.ClientName
	}
	return c.Label
}

func agentStatusBudget(s agentConnectionStatus) string {
	if s.Budget == nil {
		return "?"
	}
	if s.Budget.MonthlyCents == nil {
		return dollars(s.Budget.SpentCents) + " / unlimited"
	}
	return dollars(s.Budget.SpentCents) + " / " + dollars(*s.Budget.MonthlyCents)
}

func agentStatusLastChange(s agentConnectionStatus) string {
	if s.LastChange == nil {
		for _, u := range s.Unavailable {
			if u == "activity" {
				return "?"
			}
		}
		return "none in 7 days"
	}
	return s.LastChange.Method + " " + s.LastChange.Path + " (" + agentStatusAgo(s.LastChange.CreatedAt) + ")"
}

func agentStatusAgo(at string) string {
	t, err := time.Parse(time.RFC3339, at)
	if err != nil {
		return at
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%d min ago", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%d h ago", int(d.Hours()))
	default:
		return fmt.Sprintf("%d days ago", int(d.Hours()/24))
	}
}

func agentStatusCount(n *int) string {
	if n == nil {
		return "?"
	}
	return fmt.Sprint(*n)
}

func printAgentStatusTable(statuses []agentConnectionStatus) {
	if len(statuses) == 0 {
		_, _ = fmt.Fprintln(osStdout, "No agents are connected to this account.")
		return
	}
	_, _ = fmt.Fprintf(osStdout, "%-36s  %-24s  %-8s  %-22s  %-8s  %s\n", "ID", "AGENT", "PENDING", "BUDGET USED", "UNDOABLE", "LAST CHANGE")
	gaps := false
	for _, s := range statuses {
		pending := fmt.Sprint(len(s.Pending))
		for _, u := range s.Unavailable {
			gaps = true
			if u == "approvals" {
				pending = "?"
			}
		}
		_, _ = fmt.Fprintf(osStdout, "%-36s  %-24s  %-8s  %-22s  %-8s  %s\n", s.Connection.ID, truncateCell(agentStatusName(s.Connection), 24), pending,
			agentStatusBudget(s), agentStatusCount(s.Undoable), agentStatusLastChange(s))
	}
	if gaps {
		_, _ = fmt.Fprintln(osStdout, "? = could not be read; see --json for which.")
	}
	_, _ = fmt.Fprintln(osStdout, "Details: gregale mcp connection status <id>. Undo: gregale mcp connection undo <id>.")
}

func printAgentStatusDetail(s agentConnectionStatus) {
	c := s.Connection
	_, _ = fmt.Fprintf(osStdout, "Agent:     %s (%s)\n", agentStatusName(c), c.ID)
	if c.LastUsedAt != "" {
		_, _ = fmt.Fprintf(osStdout, "Last used: %s\n", agentStatusAgo(c.LastUsedAt))
	}
	_, _ = fmt.Fprintf(osStdout, "Changed:   %s\n", agentStatusLastChange(s))
	_, _ = fmt.Fprintf(osStdout, "Budget:    %s this month\n", agentStatusBudget(s))
	_, _ = fmt.Fprintf(osStdout, "Undoable:  %s change(s) in the last 7 days", agentStatusCount(s.Undoable))
	if s.Undoable != nil && *s.Undoable > 0 {
		_, _ = fmt.Fprintf(osStdout, " (gregale mcp connection undo %s)", c.ID)
	}
	_, _ = fmt.Fprintln(osStdout)
	if len(s.Pending) == 0 {
		_, _ = fmt.Fprintln(osStdout, "Pending:   no approval requests")
	} else {
		_, _ = fmt.Fprintf(osStdout, "Pending:   %d approval request(s)\n", len(s.Pending))
		for _, a := range s.Pending {
			line := fmt.Sprintf("  %s  %s %s", a.Category, a.Method, a.Path)
			if a.ReviewURL != "" {
				line += "  " + a.ReviewURL
			}
			_, _ = fmt.Fprintln(osStdout, line)
		}
	}
	for _, u := range s.Unavailable {
		_, _ = fmt.Fprintf(osStdout, "Could not read the agent's %s.\n", u)
	}
}

func truncateCell(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
