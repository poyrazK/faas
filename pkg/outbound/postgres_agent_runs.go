package outbound

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresAgentRuns authorizes agent run calls against agentd's live lease
// and keeps their spend ledger (ADR-20261027090010 §7). Like the Runs authorizer it
// checks the database on every request, so a cancel, a finished run or a
// lost lease takes effect at once.
type PostgresAgentRuns struct{ pool *pgxpool.Pool }

func NewPostgresAgentRuns(pool *pgxpool.Pool) (*PostgresAgentRuns, error) {
	if pool == nil {
		return nil, errors.New("outbound agent run authorizer requires a database pool")
	}
	return &PostgresAgentRuns{pool: pool}, nil
}

// AuthorizeAgentRun allows the call when the run is running under the
// identity's lease, its tenant and version match, and its version names the
// integration for the integration's provider.
func (a *PostgresAgentRuns) AuthorizeAgentRun(ctx context.Context, identity AgentRunIdentity, integrationID string) (AgentRunGrant, bool, error) {
	if !validAgentRunIDs(identity, integrationID) {
		return AgentRunGrant{}, false, nil
	}
	var grant AgentRunGrant
	err := a.pool.QueryRow(ctx, `
		SELECT coalesce(run.tenant_id::text, ''), run.ai_budget_millicents
		  FROM agent_runs run
		  JOIN agent_versions version
		    ON version.agent_id = run.agent_id AND version.version = run.agent_version
		  JOIN outbound_integrations integration
		    ON integration.id = $5::uuid AND integration.account_id = run.account_id
		 WHERE run.id = $1::uuid
		   AND run.account_id = $2::uuid
		   AND run.lease_token = $3
		   AND run.agent_id = $4::uuid
		   AND run.agent_version = $6
		   AND coalesce(run.tenant_id::text, '') = $7
		   AND run.state = 'running'
		   AND run.lease_expires_at > clock_timestamp()
		   AND run.cancel_requested_at IS NULL
		   AND run.ai_budget_millicents IS NOT NULL
		   AND integration.enabled
		   AND integration.owner_kind = 'customer'
		   AND integration.ai_provider IS NOT NULL
		   AND version.model_integrations ->> integration.ai_provider = integration.id::text`,
		identity.RunID, identity.AccountID, identity.LeaseToken, identity.AgentID, integrationID, identity.AgentVersion, identity.TenantID,
	).Scan(&grant.TenantID, &grant.AIBudgetMillicents)
	if errors.Is(err, pgx.ErrNoRows) {
		return AgentRunGrant{}, false, nil
	}
	if err != nil {
		return AgentRunGrant{}, false, fmt.Errorf("authorize agent run: %w", err)
	}
	return grant, true, nil
}

// ReserveAgentRunSpend serializes the run's calls on its spend row, drops
// reservations that lapsed, and holds reserve if it fits under budget.
func (a *PostgresAgentRuns) ReserveAgentRunSpend(ctx context.Context, runID, accountID, leaseID string, budget, reserve int64, expires time.Time) (bool, error) {
	if _, err := uuid.Parse(runID); err != nil || leaseID == "" || budget < 1 || reserve < 0 {
		return false, errors.New("reserve agent run spend: invalid run, lease or amounts")
	}
	fits := false
	err := pgx.BeginFunc(ctx, a.pool, func(tx pgx.Tx) error {
		var spent int64
		if err := tx.QueryRow(ctx, `
			INSERT INTO agent_run_ai_spend (run_id, account_id) VALUES ($1::uuid, $2::uuid)
			ON CONFLICT (run_id) DO UPDATE SET updated_at = now()
			RETURNING spent_millicents`, runID, accountID).Scan(&spent); err != nil {
			return fmt.Errorf("lock agent run spend: %w", err)
		}
		var held int64
		if err := tx.QueryRow(ctx, `
			WITH lapsed AS (
				DELETE FROM agent_run_ai_reservations WHERE run_id = $1::uuid AND expires_at <= clock_timestamp()
			)
			SELECT coalesce(sum(reserved_millicents), 0)::bigint FROM agent_run_ai_reservations
			 WHERE run_id = $1::uuid AND expires_at > clock_timestamp()`, runID).Scan(&held); err != nil {
			return fmt.Errorf("read agent run reservations: %w", err)
		}
		if spent >= budget || spent+held+reserve > budget {
			return nil
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO agent_run_ai_reservations (lease_id, run_id, reserved_millicents, expires_at)
			VALUES ($1, $2::uuid, $3, $4)`, leaseID, runID, reserve, expires); err != nil {
			return fmt.Errorf("reserve agent run spend: %w", err)
		}
		fits = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return fits, nil
}

// SettleAgentRunSpend adds the call's cost and drops its reservation in one
// statement. The handler settles each call once.
func (a *PostgresAgentRuns) SettleAgentRunSpend(ctx context.Context, runID, leaseID string, cost int64) error {
	if _, err := uuid.Parse(runID); err != nil || cost < 0 {
		return fmt.Errorf("settle agent run spend: invalid run or cost")
	}
	_, err := a.pool.Exec(ctx, `
		WITH released AS (DELETE FROM agent_run_ai_reservations WHERE lease_id = $2)
		UPDATE agent_run_ai_spend
		   SET spent_millicents = spent_millicents + $3, calls = calls + 1, updated_at = now()
		 WHERE run_id = $1::uuid`, runID, leaseID, cost)
	if err != nil {
		return fmt.Errorf("settle agent run spend: %w", err)
	}
	return nil
}

// ReleaseAgentRunSpend drops a reservation; a settled one is already gone.
func (a *PostgresAgentRuns) ReleaseAgentRunSpend(ctx context.Context, leaseID string) error {
	if _, err := a.pool.Exec(ctx, `DELETE FROM agent_run_ai_reservations WHERE lease_id = $1`, leaseID); err != nil {
		return fmt.Errorf("release agent run spend: %w", err)
	}
	return nil
}

func validAgentRunIDs(identity AgentRunIdentity, integrationID string) bool {
	for _, id := range []string{identity.AccountID, identity.RunID, identity.LeaseToken, identity.AgentID, integrationID} {
		if _, err := uuid.Parse(id); err != nil {
			return false
		}
	}
	if identity.TenantID != "" {
		if _, err := uuid.Parse(identity.TenantID); err != nil {
			return false
		}
	}
	return identity.AgentVersion >= 1
}

var (
	_ AgentRunAuthorizer  = (*PostgresAgentRuns)(nil)
	_ AgentRunSpendLedger = (*PostgresAgentRuns)(nil)
)
