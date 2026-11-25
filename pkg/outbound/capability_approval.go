package outbound

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// Run approvals (ADR-20261027090016 amendment 2): a call to a tool in
// approve mode is held here until the seller decides through apid. The held
// call is a silent relay connection, so the run's VM waits in the
// model-wait pause (ADR-20261027090012), unbilled.

// Approval outcomes, as agent_approvals_total labels them.
const (
	ApprovalApproved  = "approved"
	ApprovalDenied    = "denied"
	ApprovalExpired   = "expired"
	ApprovalAbandoned = "abandoned"
	// ApprovalRefused: the run could not ask (too many pending, or too
	// close to its deadline).
	ApprovalRefused = "refused"
)

// approvalGrace is how long after expiry outboundd still reads a decision:
// apid accepts one while now() < expires_at by its own transaction clock, so
// a decision can commit a moment after expiry.
const approvalGrace = 5 * time.Second

// ToolApprovalRequest is one held call, as the seller sees it.
type ToolApprovalRequest struct {
	Tool             string
	ArgumentsSHA256  string
	ArgumentsSummary string
}

// ToolApproval is a written request.
type ToolApproval struct {
	ID        string
	ExpiresAt time.Time
}

// ErrApprovalRefused is RequestApproval's answer when the run may not ask:
// it already has AgentRunApprovalsPendingMax waiting, or its deadline is too
// close for a decision to arrive.
var ErrApprovalRefused = errors.New("approval refused")

// AgentApprovalStore keeps a run's approval requests. outboundd writes them;
// apid writes the decision.
type AgentApprovalStore interface {
	// RequestApproval writes the request and queues its webhook, or returns
	// ErrApprovalRefused.
	RequestApproval(ctx context.Context, run AgentRunIdentity, req ToolApprovalRequest) (ToolApproval, error)
	// ApprovalDecision returns the decision ("approve" or "deny"), or "" while
	// there is none.
	ApprovalDecision(ctx context.Context, run AgentRunIdentity, approvalID string) (string, error)
	// ReleaseApproval records that the call stopped waiting, which ends the
	// wait's deadline credit (ADR-20261027090016 amendment 3).
	ReleaseApproval(ctx context.Context, run AgentRunIdentity, approvalID string) error
}

// approvalReleaseTimeout bounds the write that ends a wait; it runs after
// the call's own context may be gone.
const approvalReleaseTimeout = 5 * time.Second

func newApprovalCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(prometheus.CounterOpts{Name: "agent_approvals_total",
		Help: "Agent run tool calls held for the seller's approval, by outcome (ADR-20261027090016 amendment 2)."},
		[]string{"outcome"})
}

func (m *CapabilityMetrics) approval(outcome string) {
	if m != nil && m.approvals != nil {
		m.approvals.WithLabelValues(outcome).Inc()
	}
}

// approvalRequest describes a call for the seller: the arguments' digest and
// as much of them as fits the summary bound, cut on a rune boundary.
// Arguments that are not valid UTF-8 get no summary; the digest still names
// them.
func approvalRequest(tool string, args []byte) ToolApprovalRequest {
	sum := sha256.Sum256(args)
	var summary string
	if utf8.Valid(args) {
		summary = safetext.Truncate(string(args), api.AgentRunApprovalSummaryMaxBytes)
	}
	return ToolApprovalRequest{Tool: tool, ArgumentsSHA256: hex.EncodeToString(sum[:]), ArgumentsSummary: summary}
}

// awaitApproval holds the call until the seller decides, the request
// expires or the run goes away. It returns nil when the call may run, or the
// error result the agent sees.
func (g *CapabilityGateway) awaitApproval(ctx context.Context, cr capabilityRun, tool string, args []byte) (*mcp.CallToolResult, error) {
	store, ok := g.Store.(AgentApprovalStore)
	if !ok {
		return nil, errors.New("capability gateway: the store keeps no approvals")
	}
	approval, err := store.RequestApproval(ctx, cr.run, approvalRequest(tool, args))
	if errors.Is(err, ErrApprovalRefused) {
		g.Metrics.approval(ApprovalRefused)
		return errorResult(fmt.Sprintf("%s needs the seller's approval, and this run cannot ask now: it has %d calls waiting or too little time left",
			tool, api.AgentRunApprovalsPendingMax)), nil
	}
	if err != nil {
		return nil, fmt.Errorf("capability gateway: request approval: %w", err)
	}
	defer func() {
		// A failed write only lets the credit run on to the decision or the
		// expiry, which the cap bounds.
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), approvalReleaseTimeout)
		defer cancel()
		_ = store.ReleaseApproval(rctx, cr.run, approval.ID)
	}()
	poll := g.ApprovalPoll
	if poll <= 0 {
		poll = time.Duration(api.AgentRunApprovalPollMillis) * time.Millisecond
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		decision, err := store.ApprovalDecision(ctx, cr.run, approval.ID)
		switch {
		case err != nil && ctx.Err() == nil:
			return nil, fmt.Errorf("capability gateway: read approval: %w", err)
		case decision == api.AgentRunApprovalDecisionApprove:
			g.Metrics.approval(ApprovalApproved)
			return nil, nil
		case decision == api.AgentRunApprovalDecisionDeny:
			g.Metrics.approval(ApprovalDenied)
			return errorResult(fmt.Sprintf("the seller denied this %s call (approval %s)", tool, approval.ID)), nil
		case g.now().After(approval.ExpiresAt.Add(approvalGrace)):
			g.Metrics.approval(ApprovalExpired)
			return errorResult(fmt.Sprintf("no one approved this %s call before %s (approval %s)", tool,
				approval.ExpiresAt.UTC().Format(time.RFC3339), approval.ID)), nil
		}
		select {
		case <-ctx.Done():
			g.Metrics.approval(ApprovalAbandoned)
			return nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (g *CapabilityGateway) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

// approvalWait is how long a held request may take: the longest approval
// wait plus the tool's own run.
func (g *CapabilityGateway) approvalWait() time.Duration {
	return time.Duration(api.AgentRunApprovalTimeoutSec)*time.Second + approvalGrace + g.toolTimeout() + time.Minute
}

// holdsApprovals reports whether the run's policy puts any tool in approve
// mode.
func holdsApprovals(policy *api.AgentCapabilityPolicy) bool {
	if policy == nil {
		return false
	}
	for _, t := range policy.Tools {
		if t.Mode == api.AgentCapabilityApprove {
			return true
		}
	}
	return false
}
