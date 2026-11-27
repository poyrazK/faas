package outbound

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// AgentRunCapabilities reads the run's version policy and plan in the same
// query that checks the run's lease, so a cancelled or finished run loses
// its tools on the next request (ADR-20261027090016 amendment 1).
func (a *PostgresAgentRuns) AgentRunCapabilities(ctx context.Context, run AgentRunIdentity) (AgentRunCapabilities, bool, error) {
	if !validAgentRunIDs(run, run.RunID) {
		return AgentRunCapabilities{}, false, nil
	}
	var policy []byte
	var plan string
	err := a.pool.QueryRow(ctx, `
		SELECT version.capability_policy, account.plan
		  FROM agent_runs run
		  JOIN agent_versions version
		    ON version.agent_id = run.agent_id AND version.version = run.agent_version
		  JOIN accounts account ON account.id = run.account_id
		 WHERE run.id = $1::uuid
		   AND run.account_id = $2::uuid
		   AND run.lease_token = $3
		   AND run.agent_id = $4::uuid
		   AND run.agent_version = $5
		   AND coalesce(run.tenant_id::text, '') = $6
		   AND run.state = 'running'
		   AND run.lease_expires_at > clock_timestamp()
		   AND run.cancel_requested_at IS NULL`,
		run.RunID, run.AccountID, run.LeaseToken, run.AgentID, run.AgentVersion, run.TenantID,
	).Scan(&policy, &plan)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRunCapabilities{}, false, nil
	}
	if err != nil {
		return AgentRunCapabilities{}, false, fmt.Errorf("read agent run capabilities: %w", err)
	}
	caps := AgentRunCapabilities{Plan: api.Plan(plan)}
	if len(policy) > 0 {
		caps.Policy = &api.AgentCapabilityPolicy{}
		if err := json.Unmarshal(policy, caps.Policy); err != nil {
			return AgentRunCapabilities{}, false, fmt.Errorf("decode capability policy: %w", err)
		}
	}
	return caps, true, nil
}

// ReserveToolCall admits one call when the run has made fewer than budget.
// The conditional upsert serializes a run's calls on its summary row, so
// concurrent calls cannot pass the budget together.
func (a *PostgresAgentRuns) ReserveToolCall(ctx context.Context, run AgentRunIdentity, budget int) (bool, error) {
	if budget < 1 {
		return false, nil
	}
	var calls int
	err := a.pool.QueryRow(ctx, `
		INSERT INTO agent_run_capability_summaries AS s (run_id, account_id, tool_calls)
		VALUES ($1::uuid, $2::uuid, 1)
		ON CONFLICT (run_id) DO UPDATE SET tool_calls = s.tool_calls + 1, last_call_at = now()
		 WHERE s.tool_calls < $3
		RETURNING tool_calls`, run.RunID, run.AccountID, budget).Scan(&calls)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("reserve agent run tool call: %w", err)
	}
	return true, nil
}

// RecordDecision counts a refused call on the run's summary and writes its
// decision row, together.
func (a *PostgresAgentRuns) RecordDecision(ctx context.Context, run AgentRunIdentity, tool, decision string) error {
	if !slices.Contains([]string{CapabilityDenied, CapabilityBudget, CapabilityDisabled}, decision) {
		return fmt.Errorf("record capability decision: unknown decision %q", decision)
	}
	return pgx.BeginFunc(ctx, a.pool, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_run_capability_summaries AS s (run_id, account_id, refused_calls)
			VALUES ($1::uuid, $2::uuid, 1)
			ON CONFLICT (run_id) DO UPDATE SET refused_calls = s.refused_calls + 1, last_call_at = now()`,
			run.RunID, run.AccountID); err != nil {
			return fmt.Errorf("count refused capability call: %w", err)
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_run_capability_decisions (run_id, account_id, tool, decision)
			VALUES ($1::uuid, $2::uuid, $3, $4)`, run.RunID, run.AccountID, tool, decision); err != nil {
			return fmt.Errorf("record capability decision: %w", err)
		}
		return nil
	})
}

var _ AgentCapabilityStore = (*PostgresAgentRuns)(nil)
