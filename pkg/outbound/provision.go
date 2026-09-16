package outbound

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// IntegrationRecord contains the account-owned metadata needed when an
// operator provisions a policy. Request admission itself only needs the
// Integration policy; this record is used by the first outboundd config path.
type IntegrationRecord struct {
	AccountID uuid.UUID
	Name      string
	Policy    Integration
}

// EnsureIntegration atomically upserts an integration policy and its app
// bindings. It is intentionally kept in this package so outboundd remains the
// sole writer for these tables; apid can call the same owner through a future
// API adapter rather than writing the tables directly.
func EnsureIntegration(ctx context.Context, pool *pgxpool.Pool, record IntegrationRecord) error {
	if pool == nil {
		return fmt.Errorf("outbound postgres pool is required")
	}
	if record.AccountID == uuid.Nil || record.Name == "" {
		return fmt.Errorf("outbound integration account and name are required")
	}
	if err := record.Policy.Validate(); err != nil {
		return err
	}
	integrationID, err := uuid.Parse(record.Policy.ID)
	if err != nil {
		return fmt.Errorf("%w: integration id must be a UUID", ErrInvalidIntegration)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	_, err = tx.Exec(ctx, `
		INSERT INTO outbound_integrations
		    (id, account_id, name, origin, token_hash, rate_per_second, burst,
		     max_in_flight, request_timeout_ms, enabled)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (id) DO UPDATE SET
		    account_id = EXCLUDED.account_id, name = EXCLUDED.name,
		    origin = EXCLUDED.origin, token_hash = EXCLUDED.token_hash,
		    rate_per_second = EXCLUDED.rate_per_second, burst = EXCLUDED.burst,
		    max_in_flight = EXCLUDED.max_in_flight,
		    request_timeout_ms = EXCLUDED.request_timeout_ms,
		    enabled = EXCLUDED.enabled, updated_at = now()`,
		integrationID, record.AccountID, record.Name, record.Policy.Origin.String(),
		record.Policy.TokenHash[:], record.Policy.RatePerSecond, record.Policy.Burst,
		record.Policy.MaxInFlight, record.Policy.RequestTimeout.Milliseconds(), record.Policy.Enabled)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM outbound_integration_apps WHERE integration_id = $1`, integrationID); err != nil {
		return err
	}
	for appID := range record.Policy.AppIDs {
		appUUID, err := uuid.Parse(appID)
		if err != nil {
			return fmt.Errorf("%w: app id %q must be a UUID", ErrInvalidIntegration, appID)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO outbound_integration_apps (integration_id, app_id) VALUES ($1,$2)`, integrationID, appUUID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
