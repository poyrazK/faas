package agents

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/agents/sqlc"
)

// Run approvals (ADR-20261027090016 amendment 2). outboundd writes the
// requests; apid lists them and writes the seller's decision, once.

// ErrApprovalClosed refuses a decision on an approval that already has one
// or has expired.
var ErrApprovalClosed = errors.New("agents: approval already decided or expired")

// runApprovalsMax bounds one listing: a run holds at most
// AgentRunApprovalsPendingMax undecided approvals at a time, and a run
// rarely asks for more than a few in its life.
const runApprovalsMax = 500

// Approval is one approval with its decision, if any.
type Approval struct {
	ID, RunID, Tool                   string
	ArgumentsSHA256, ArgumentsSummary string
	Decision, Note                    string
	CreatedAt, ExpiresAt              time.Time
	DecidedAt                         *time.Time
}

// State is the approval's state at now.
func (a Approval) State(now time.Time) string {
	switch {
	case a.Decision == api.AgentRunApprovalDecisionApprove:
		return api.AgentRunApprovalApproved
	case a.Decision == api.AgentRunApprovalDecisionDeny:
		return api.AgentRunApprovalDenied
	case !now.Before(a.ExpiresAt):
		return api.AgentRunApprovalExpired
	}
	return api.AgentRunApprovalPending
}

// RunApprovals lists the run's approvals, oldest first.
func (s *Store) RunApprovals(ctx context.Context, accountID, runID string) ([]Approval, error) {
	if !isUUID(runID) {
		return nil, ErrNotFound
	}
	if _, err := s.Run(ctx, accountID, runID); err != nil {
		return nil, err
	}
	rows, err := s.q.ListAgentRunApprovals(ctx, s.pool, sqlc.ListAgentRunApprovalsParams{RunID: runID, AccountID: accountID,
		MaxRows: runApprovalsMax})
	if err != nil {
		return nil, fmt.Errorf("list agent run approvals: %w", err)
	}
	out := make([]Approval, 0, len(rows))
	for _, r := range rows {
		out = append(out, approvalFrom(sqlc.GetAgentRunApprovalRow(r)))
	}
	return out, nil
}

// DecideRunApproval records the seller's decision on a pending approval.
// It returns the approval as decided, ErrNotFound for an approval the run
// does not have, or ErrApprovalClosed when it already has a decision or has
// expired.
func (s *Store) DecideRunApproval(ctx context.Context, accountID, runID, approvalID, decision, note, decidedBy string) (Approval, error) {
	if !isUUID(runID) || !isUUID(approvalID) {
		return Approval{}, ErrNotFound
	}
	if decision != api.AgentRunApprovalDecisionApprove && decision != api.AgentRunApprovalDecisionDeny {
		return Approval{}, fmt.Errorf("decide agent run approval: unknown decision %q", decision)
	}
	n, err := s.q.InsertAgentRunApprovalDecision(ctx, s.pool, sqlc.InsertAgentRunApprovalDecisionParams{Decision: decision, Note: note,
		DecidedBy: decidedBy, ApprovalID: approvalID, RunID: runID, AccountID: accountID})
	if err != nil {
		return Approval{}, fmt.Errorf("decide agent run approval: %w", err)
	}
	row, err := s.q.GetAgentRunApproval(ctx, s.pool, sqlc.GetAgentRunApprovalParams{ApprovalID: approvalID, RunID: runID, AccountID: accountID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Approval{}, ErrNotFound
	}
	if err != nil {
		return Approval{}, fmt.Errorf("read agent run approval: %w", err)
	}
	approval := approvalFrom(row)
	if n == 0 || approval.Decision != decision {
		return approval, ErrApprovalClosed
	}
	return approval, nil
}

func approvalFrom(r sqlc.GetAgentRunApprovalRow) Approval {
	a := Approval{ID: r.ID, RunID: r.RunID, Tool: r.Tool, ArgumentsSHA256: r.ArgumentsSha256, ArgumentsSummary: r.ArgumentsSummary,
		Decision: r.Decision, Note: r.Note, CreatedAt: r.CreatedAt.Time, ExpiresAt: r.ExpiresAt.Time}
	if r.DecidedAt.Valid {
		t := r.DecidedAt.Time
		a.DecidedAt = &t
	}
	return a
}
