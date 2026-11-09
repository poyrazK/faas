package mcpmanagement

import (
	"context"
	"errors"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// An agent key's risky request waits for a person (ADR-20261104140058). The
// bridge passes the request on to the agent, which waits for the decision
// with gregale_wait_for_approval and then repeats its own call; an agent
// reads what it already changed with gregale_agent_activity
// (ADR-20261107220000).
const (
	waitForApprovalTool = "gregale_wait_for_approval"
	agentActivityTool   = "gregale_agent_activity"

	// maxApprovalWaitSeconds keeps one wait inside a tool call's time
	// budget; the agent calls again while the request is pending.
	maxApprovalWaitSeconds     = 50
	defaultApprovalWaitSeconds = 30
)

// approvalPollInterval is how often a wait asks whether a person decided.
var approvalPollInterval = 2 * time.Second

// toolApproval is the approval request behind approval_required,
// approval_denied or approval_expired.
type toolApproval struct {
	ID        string `json:"id"`
	Category  string `json:"category,omitempty"`
	ReviewURL string `json:"review_url,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	Status    string `json:"status,omitempty"`
	Note      string `json:"note,omitempty"`
}

func (m *management) toolApprovalFrom(info *api.AgentApprovalPendingInfo) *toolApproval {
	if info == nil || info.ID == "" {
		return nil
	}
	clean := func(s string) string { return m.clean(s) }
	return &toolApproval{ID: clean(info.ID), Category: clean(info.Category), ReviewURL: clean(info.ReviewURL),
		ExpiresAt: clean(info.ExpiresAt), Status: clean(info.Status), Note: clean(info.Note)}
}

// approvalRecovery tells the agent what to do about an approval: wait for
// the decision and then repeat its own call, or stop and tell the person.
func (m *management) approvalRecovery(p ToolError) Recovery {
	switch p.Code {
	case api.CodeApprovalRequired:
		r := m.recoveryTool(Recovery{Action: "await_approval"}, waitForApprovalTool, map[string]any{"approval_id": p.Approval.ID})
		r.ReuseInputs = true
		return r
	case api.CodeApprovalDenied, api.CodeApprovalExpired:
		return Recovery{Action: "stop"}
	}
	return p.Recovery
}

type waitForApprovalArgs struct {
	ApprovalID  string `json:"approval_id" jsonschema:"approval.id from the error"`
	WaitSeconds int    `json:"wait_seconds,omitempty" jsonschema:"1 to 50 (default 30)"`
}

type approvalWaitResult struct {
	SchemaVersion int      `json:"schema_version"`
	ApprovalID    string   `json:"approval_id"`
	Status        string   `json:"status"`
	Decided       bool     `json:"decided"`
	Note          string   `json:"note,omitempty"`
	ReviewURL     string   `json:"review_url,omitempty"`
	ExpiresAt     string   `json:"expires_at,omitempty"`
	Instruction   string   `json:"instruction"`
	Recovery      Recovery `json:"recovery"`
}

type agentActivityArgs struct {
	Cursor string `json:"cursor,omitempty" jsonschema:"next_before from an earlier page"`
	Limit  int    `json:"limit,omitempty" jsonschema:"1 to 200 (default 50)"`
}

func registerAgentApprovals(s *mcp.Server, m *management) {
	register(s, m, waitForApprovalTool, "Wait for a person to decide the approval request from an approval_required error. Follow recovery: retry_original repeats your call once with the same inputs; stop means tell the user.", m.waitForApproval)
	register(s, m, agentActivityTool, "List this agent key's write requests of the last 7 days, newest first: method, path, status, approval used.", m.agentActivity)
}

func (m *management) waitForApproval(ctx context.Context, args waitForApprovalArgs) (any, error) {
	if args.ApprovalID == "" {
		return nil, invalid("invalid_argument", "approval_id is required")
	}
	seconds := args.WaitSeconds
	if seconds == 0 {
		seconds = defaultApprovalWaitSeconds
	}
	if seconds < 1 || seconds > maxApprovalWaitSeconds {
		return nil, invalid("invalid_argument", "wait_seconds must be between 1 and 50")
	}
	deadline := time.Now().Add(time.Duration(seconds) * time.Second)
	for {
		got, err := m.client.GetAgentApproval(ctx, args.ApprovalID)
		if err != nil {
			return nil, err
		}
		if got.Status != api.AgentApprovalPending || !time.Now().Add(approvalPollInterval).Before(deadline) {
			return m.approvalWaitResult(got), nil
		}
		timer := time.NewTimer(approvalPollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				return m.approvalWaitResult(got), nil
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

func (m *management) approvalWaitResult(got api.AgentApprovalResponse) approvalWaitResult {
	out := approvalWaitResult{SchemaVersion: recoveryContractVersion, ApprovalID: m.clean(got.ID), Status: m.clean(got.Status),
		Note: m.clean(got.Note), ReviewURL: m.clean(got.ReviewURL), ExpiresAt: m.clean(got.ExpiresAt)}
	switch got.Status {
	case api.AgentApprovalPending:
		out.Instruction = "Still waiting for a person. Remind the user of the review link, then call this tool again."
		out.Recovery = m.recoveryTool(Recovery{Action: "await_approval"}, waitForApprovalTool, map[string]any{"approval_id": got.ID})
	case api.AgentApprovalApproved:
		out.Decided = true
		out.Instruction = "Approved. Repeat your original call once, with exactly the same inputs."
		out.Recovery = Recovery{Action: "retry_original", ReuseInputs: true}
	case api.AgentApprovalDenied:
		out.Decided = true
		out.Instruction = "Denied. Do not repeat the call; tell the user, with the person's note if there is one."
		out.Recovery = Recovery{Action: "stop"}
	case api.AgentApprovalConsumed:
		out.Decided = true
		out.Instruction = "This approval was already used by a call with the same inputs. Check gregale_agent_activity before doing it again."
		out.Recovery = Recovery{Action: "stop"}
	default:
		out.Decided = true
		out.Instruction = "Expired before anyone decided. Tell the user; ask again later only if they want you to."
		out.Recovery = Recovery{Action: "stop"}
	}
	return out
}

func (m *management) agentActivity(ctx context.Context, args agentActivityArgs) (any, error) {
	if args.Limit < 0 || args.Limit > api.MaxAgentActivityPage {
		return nil, invalid("invalid_argument", "limit must be between 1 and 200")
	}
	return m.client.AgentConnectionSelfActivity(ctx, args.Cursor, args.Limit)
}
