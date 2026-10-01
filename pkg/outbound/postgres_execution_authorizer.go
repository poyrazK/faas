package outbound

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PostgresExecutionAuthorizer checks the current Run lease and outbound grant
// in the control-plane database for each gateway request. Keeping this check
// online makes grant revocation, cancellation, completion, and lease takeover
// take effect without waiting for the signed assertion to expire.
type PostgresExecutionAuthorizer struct{ pool *pgxpool.Pool }

func NewPostgresExecutionAuthorizer(pool *pgxpool.Pool) (*PostgresExecutionAuthorizer, error) {
	if pool == nil {
		return nil, fmt.Errorf("outbound execution authorizer requires a database pool")
	}
	return &PostgresExecutionAuthorizer{pool: pool}, nil
}

func (a *PostgresExecutionAuthorizer) AuthorizeExecution(ctx context.Context, identity ExecutionIdentity, integrationID string) (bool, error) {
	if a == nil || a.pool == nil {
		return false, fmt.Errorf("outbound execution authorizer is unavailable")
	}
	if !validExecutionAuthorizationUUIDs(identity, integrationID) {
		return false, nil
	}
	accountID, _ := uuid.Parse(identity.AccountID)
	executionID, _ := uuid.Parse(identity.ExecutionID)
	leaseToken, _ := uuid.Parse(identity.LeaseToken)
	outboundIntegrationID, _ := uuid.Parse(integrationID)
	var allowed bool
	err := a.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			  FROM executions execution
			  JOIN execution_outbound_integrations requested
			    ON requested.execution_id = execution.id
			   AND requested.integration_id = $4
			  JOIN outbound_integrations integration
			    ON integration.id = requested.integration_id
			   AND integration.account_id = execution.account_id
			 WHERE execution.id = $1
			   AND execution.account_id = $2
			   AND execution.lease_token = $3
			   AND execution.status = 'running'
			   AND execution.lease_expires_at > clock_timestamp()
			   AND execution.deadline_at > clock_timestamp()
			   AND execution.cancel_requested_at IS NULL
			   AND integration.enabled
			   AND integration.runs_enabled
			   AND integration.owner_kind = 'customer'
			   AND integration.provider_auth_mode = 'managed'
			   AND integration.credential_source = 'customer_sealed'
		)`, executionID, accountID, leaseToken, outboundIntegrationID).Scan(&allowed)
	if err != nil {
		return false, err
	}
	return allowed, nil
}

func validExecutionAuthorizationUUIDs(identity ExecutionIdentity, integrationID string) bool {
	_, accountErr := uuid.Parse(identity.AccountID)
	_, executionErr := uuid.Parse(identity.ExecutionID)
	_, leaseErr := uuid.Parse(identity.LeaseToken)
	_, integrationErr := uuid.Parse(integrationID)
	return accountErr == nil && executionErr == nil && leaseErr == nil && integrationErr == nil
}

var _ ExecutionAuthorizer = (*PostgresExecutionAuthorizer)(nil)
