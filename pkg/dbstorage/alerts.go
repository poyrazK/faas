package dbstorage

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Alert struct {
	ID           string
	DatabaseID   string
	DatabaseName string
	IncidentID   string
	State        string
	SampledAt    time.Time
	TotalBytes   int64
	FreeBytes    int64
	GrowthReason string
}

// ReadAlerts returns recent primary transitions, newest first. The last
// transition remains visible if the database is parked or the sample is stale.
func ReadAlerts(ctx context.Context, pool *pgxpool.Pool, ids []pgtype.UUID) ([]Alert, error) {
	rows, err := pool.Query(ctx, `
		SELECT v.database_id::text, a.incident_id::text, a.state, a.sampled_at, a.total_bytes, a.free_bytes, a.growth_reason
		FROM database_vms v CROSS JOIN LATERAL (
			SELECT incident_id, state, sampled_at, total_bytes, free_bytes, growth_reason
			FROM native_postgres_storage_alerts WHERE database_id = v.database_id
			ORDER BY sampled_at DESC LIMIT 10
		) a WHERE v.database_id = ANY($1::uuid[])
		ORDER BY v.database_id, a.sampled_at DESC`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var alerts []Alert
	for rows.Next() {
		var a Alert
		if err := rows.Scan(&a.DatabaseID, &a.IncidentID, &a.State, &a.SampledAt, &a.TotalBytes, &a.FreeBytes, &a.GrowthReason); err != nil {
			return nil, err
		}
		alerts = append(alerts, a)
	}
	return alerts, rows.Err()
}

type AlertRecipient struct {
	AccountID string
	OrgID     string
	Plan      string
	Email     string
}

type alertDelivery struct {
	Alert
	lease    uuid.UUID
	attempts int16
}

const (
	alertMaxAttempts = 6
	alertLifetime    = 24 * time.Hour
)

// DeliverAlerts drains at most 32 events. Each database's transitions are
// delivered in observation order, with independent leases across workers.
// A sender must use Alert.ID to deduplicate an ambiguous submission.
func DeliverAlerts(ctx context.Context, pool *pgxpool.Pool, send func(context.Context, Alert, AlertRecipient) error) error {
	var deliveryFailed bool
	for range 32 {
		a, err := claimAlert(ctx, pool)
		if errors.Is(err, pgx.ErrNoRows) {
			break
		}
		if err != nil {
			return err
		}
		var sendErr error
		if a.attempts <= alertMaxAttempts && time.Since(a.SampledAt) < alertLifetime {
			sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			sendErr = deliverToAlertOwner(sendCtx, pool, a, send)
			cancel()
		}
		deliveryFailed = deliveryFailed || sendErr != nil
		settleCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err = settleAlert(settleCtx, pool, a, sendErr == nil || a.attempts >= alertMaxAttempts)
		cancel()
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if deliveryFailed {
		return errors.New("native postgres storage alert email failed")
	}
	return nil
}

func claimAlert(ctx context.Context, pool *pgxpool.Pool) (alertDelivery, error) {
	a := alertDelivery{lease: uuid.New()}
	err := pool.QueryRow(ctx, `
		WITH candidate AS (
			SELECT event.id FROM native_postgres_storage_alerts event
			WHERE event.finished_at IS NULL AND event.available_at <= statement_timestamp()
				AND NOT EXISTS (SELECT 1 FROM native_postgres_storage_alerts older
					WHERE older.database_id = event.database_id AND older.sampled_at < event.sampled_at
						AND older.finished_at IS NULL)
			ORDER BY event.available_at, event.sampled_at, event.id
			LIMIT 1 FOR UPDATE OF event SKIP LOCKED
		)
		UPDATE native_postgres_storage_alerts event
		SET lease_token = $1, available_at = statement_timestamp() + interval '1 minute', attempts = event.attempts + 1
		FROM candidate WHERE event.id = candidate.id
		RETURNING event.id::text, event.database_id::text, event.incident_id::text, event.state, event.sampled_at,
			event.total_bytes, event.free_bytes, event.growth_reason, event.database_name, event.attempts`, a.lease).
		Scan(&a.ID, &a.DatabaseID, &a.IncidentID, &a.State, &a.SampledAt, &a.TotalBytes, &a.FreeBytes, &a.GrowthReason, &a.DatabaseName, &a.attempts)
	return a, err
}

func deliverToAlertOwner(ctx context.Context, pool *pgxpool.Pool, a alertDelivery, send func(context.Context, Alert, AlertRecipient) error) error {
	var owner AlertRecipient
	err := pool.QueryRow(ctx, `
		SELECT a.id::text, CASE WHEN o.personal_org THEN '' ELSE o.id::text END, a.plan, a.email
		FROM managed_postgres_databases d JOIN orgs o ON o.id = d.org_id
		JOIN accounts a ON a.id = CASE WHEN o.personal_org THEN o.personal_owner_account_id ELSE (
			SELECT m.account_id FROM org_memberships m
			WHERE m.org_id = o.id AND m.role = 'owner' AND m.removed_at IS NULL
		) END
		WHERE d.id = $1::uuid AND d.provider_resource_id = d.id::text AND d.state IN ('ready', 'updating')
			AND d.clone_resource_role = 'target' AND NOT o.deleted_pending AND o.deletion_requested_at IS NULL
			AND a.status IN ('active', 'past_due') AND a.deletion_requested_at IS NULL AND a.email_verified_at IS NOT NULL`, a.DatabaseID).
		Scan(&owner.AccountID, &owner.OrgID, &owner.Plan, &owner.Email)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return send(ctx, a.Alert, owner)
}

func settleAlert(ctx context.Context, pool *pgxpool.Pool, a alertDelivery, finished bool) error {
	_, err := pool.Exec(ctx, `UPDATE native_postgres_storage_alerts
		SET lease_token = NULL, finished_at = CASE WHEN $3 THEN statement_timestamp() END,
			available_at = statement_timestamp() + least(attempts, 5) * interval '1 minute'
		WHERE id = $1::uuid AND lease_token = $2`, a.ID, a.lease, finished)
	return err
}

// PruneAlerts keeps the last transition for each database even when it has
// been parked for longer than the history retention period.
func PruneAlerts(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, `DELETE FROM native_postgres_storage_alerts WHERE id IN (
		SELECT event.id FROM native_postgres_storage_alerts event
		WHERE event.finished_at IS NOT NULL AND event.sampled_at < statement_timestamp() - interval '30 days'
			AND EXISTS (SELECT 1 FROM native_postgres_storage_alerts newer
				WHERE newer.database_id = event.database_id AND newer.sampled_at > event.sampled_at)
		ORDER BY event.sampled_at LIMIT 256 FOR UPDATE OF event SKIP LOCKED
	)`)
	return err
}
