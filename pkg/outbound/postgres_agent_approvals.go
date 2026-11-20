package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/agentrunproto"
	"github.com/onebox-faas/faas/pkg/api"
)

// RequestApproval writes a held call's approval request and queues the
// agent_run.approval_requested webhook in one transaction
// (ADR-20261027090016 amendment 2). The run's capability summary row
// serializes its requests, so concurrent calls cannot pass the pending cap
// together. The request expires at the approval timeout or a margin before
// the run's latest deadline, whichever is sooner. A run's deadline grows by
// its approval waits up to deadline_credit_max_sec (ADR-20261027090016
// amendment 3), and a wait earns credit one second per second until that
// cap, so the cap's deadline is the last moment this wait can end.
func (a *PostgresAgentRuns) RequestApproval(ctx context.Context, run AgentRunIdentity, req ToolApprovalRequest) (ToolApproval, error) {
	if !validAgentRunIDs(run, run.RunID) {
		return ToolApproval{}, ErrApprovalRefused
	}
	var out ToolApproval
	err := pgx.BeginFunc(ctx, a.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_run_capability_summaries AS s (run_id, account_id) VALUES ($1::uuid, $2::uuid)
			ON CONFLICT (run_id) DO UPDATE SET last_call_at = now()`, run.RunID, run.AccountID); err != nil {
			return fmt.Errorf("lock agent run approvals: %w", err)
		}
		err := tx.QueryRow(ctx, `
			WITH bound AS (
				SELECT least(now() + make_interval(secs => $5::int),
				             run.running_at + make_interval(secs => least(run.timeout_sec + run.deadline_credit_max_sec, $10::int) - $6::int)) AS expires_at
				  FROM agent_runs run
				 WHERE run.id = $1::uuid AND run.account_id = $2::uuid AND run.lease_token = $7
				   AND run.lease_expires_at > clock_timestamp()
				   AND run.state = 'running' AND run.cancel_requested_at IS NULL AND run.running_at IS NOT NULL
				   AND (SELECT count(*) FROM agent_run_approvals p
				         WHERE p.run_id = run.id AND p.expires_at > now()
				           AND NOT EXISTS (SELECT 1 FROM agent_run_approval_decisions d WHERE d.approval_id = p.id)) < $8::int
			)
			INSERT INTO agent_run_approvals (run_id, account_id, tool, arguments_sha256, arguments_summary, expires_at)
			SELECT $1::uuid, $2::uuid, $3, $4, $9, bound.expires_at FROM bound
			 WHERE bound.expires_at > now() + interval '1 second'
			RETURNING id::text, expires_at`,
			run.RunID, run.AccountID, req.Tool, req.ArgumentsSHA256, api.AgentRunApprovalTimeoutSec,
			api.AgentRunApprovalDeadlineMarginSec, run.LeaseToken, api.AgentRunApprovalsPendingMax, req.ArgumentsSummary,
			agentrunproto.MaxTimeoutSec,
		).Scan(&out.ID, &out.ExpiresAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrApprovalRefused
		}
		if err != nil {
			return fmt.Errorf("request agent run approval: %w", err)
		}
		payload, err := json.Marshal(api.AgentRunApprovalRequestedWebhookPayload{ApprovalID: out.ID, RunID: run.RunID,
			AgentID: run.AgentID, Tenant: run.TenantID, Tool: req.Tool, ArgumentsSHA256: req.ArgumentsSHA256,
			ArgumentsSummary: req.ArgumentsSummary, ExpiresAt: out.ExpiresAt.UTC()})
		if err != nil {
			return fmt.Errorf("encode approval webhook: %w", err)
		}
		// ADR-224: one pending delivery per enabled account receiver
		// subscribed to the event; the ADR-076 dispatcher signs and sends it.
		if _, err := tx.Exec(ctx, `
			INSERT INTO app_webhook_deliveries (webhook_id, app_id, account_id, event, payload, source_event_id)
			SELECT w.id, NULL, w.account_id, $3, $2::jsonb, $4::uuid
			  FROM app_webhooks w
			 WHERE w.account_id = $1::uuid AND w.scope = 'account' AND w.enabled AND $3 = ANY(w.event_filter)`,
			run.AccountID, payload, api.AgentRunApprovalRequestedWebhookEvent, out.ID); err != nil {
			return fmt.Errorf("queue approval webhook: %w", err)
		}
		return nil
	})
	if err != nil {
		return ToolApproval{}, err
	}
	return out, nil
}

// ApprovalDecision reads the seller's decision on the run's approval.
func (a *PostgresAgentRuns) ApprovalDecision(ctx context.Context, run AgentRunIdentity, approvalID string) (string, error) {
	if _, err := uuid.Parse(approvalID); err != nil {
		return "", fmt.Errorf("read approval decision: invalid approval id")
	}
	var decision string
	err := a.pool.QueryRow(ctx, `
		SELECT coalesce(d.decision, '')
		  FROM agent_run_approvals p
		  LEFT JOIN agent_run_approval_decisions d ON d.approval_id = p.id
		 WHERE p.id = $1::uuid AND p.run_id = $2::uuid AND p.account_id = $3::uuid`,
		approvalID, run.RunID, run.AccountID).Scan(&decision)
	if err != nil {
		return "", fmt.Errorf("read approval decision: %w", err)
	}
	return decision, nil
}

// ReleaseApproval records when outboundd stopped holding the call. The
// first release stands.
func (a *PostgresAgentRuns) ReleaseApproval(ctx context.Context, run AgentRunIdentity, approvalID string) error {
	if _, err := uuid.Parse(approvalID); err != nil {
		return fmt.Errorf("release approval: invalid approval id")
	}
	if _, err := a.pool.Exec(ctx, `
		UPDATE agent_run_approvals SET released_at = now()
		 WHERE id = $1::uuid AND run_id = $2::uuid AND account_id = $3::uuid AND released_at IS NULL`,
		approvalID, run.RunID, run.AccountID); err != nil {
		return fmt.Errorf("release approval: %w", err)
	}
	return nil
}

var _ AgentApprovalStore = (*PostgresAgentRuns)(nil)
