package state

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *PgStore) RevokePlatformTenantSelfConsumers(ctx context.Context, in RevokePlatformTenantSelfConsumersParams) (PlatformTenantSelfConsumerRevocationResult, error) {
	in, err := normalizePlatformTenantSelfConsumerRevocation(in)
	if err != nil {
		return PlatformTenantSelfConsumerRevocationResult{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlatformTenantSelfConsumerRevocationResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var tenantID string
	if err := tx.QueryRow(ctx, `select id::text from platform_tenants
		where id = $1::uuid and account_id = $2::uuid for update`, in.TenantID, in.AccountID).Scan(&tenantID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return PlatformTenantSelfConsumerRevocationResult{}, ErrNotFound
		}
		return PlatformTenantSelfConsumerRevocationResult{}, err
	}

	// Lock the entire requested set before changing any rows. This keeps a
	// mixed-tenant or missing-ID request all-or-nothing and serializes against
	// tenant credential issuance, which takes the same tenant/consumer locks.
	rows, err := tx.Query(ctx, `select `+apiConsumerSelectCols+` from api_consumers
		where account_id = $1::uuid and platform_tenant_id = $2::uuid and id = any($3::uuid[])
		order by id for update`, in.AccountID, in.TenantID, in.ConsumerIDs)
	if err != nil {
		return PlatformTenantSelfConsumerRevocationResult{}, err
	}
	consumers := make([]APIConsumer, 0, len(in.ConsumerIDs))
	for rows.Next() {
		consumer, err := scanAPIConsumerRow(rows)
		if err != nil {
			rows.Close()
			return PlatformTenantSelfConsumerRevocationResult{}, err
		}
		consumers = append(consumers, consumer)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PlatformTenantSelfConsumerRevocationResult{}, err
	}
	rows.Close()
	if len(consumers) != len(in.ConsumerIDs) {
		return PlatformTenantSelfConsumerRevocationResult{}, ErrNotFound
	}

	result := PlatformTenantSelfConsumerRevocationResult{Consumers: make([]APIConsumer, 0, len(consumers))}
	for _, consumer := range consumers {
		if consumer.Status != APIConsumerStatusRevoked || consumer.RevokedAt == nil {
			consumer, err = scanAPIConsumerRow(tx.QueryRow(ctx, `update api_consumers
				set status = 'revoked', revoked_at = coalesce(revoked_at, now()), updated_at = now()
				where account_id = $1::uuid and id = $2::uuid returning `+apiConsumerSelectCols,
				in.AccountID, consumer.ID))
			if err != nil {
				return PlatformTenantSelfConsumerRevocationResult{}, err
			}
			result.Changed = true
		}
		result.Consumers = append(result.Consumers, consumer)
	}

	tag, err := tx.Exec(ctx, `update consumer_keys set revoked_at = now()
		where account_id = $1::uuid and consumer_id = any($2::uuid[]) and revoked_at is null`, in.AccountID, in.ConsumerIDs)
	if err != nil {
		return PlatformTenantSelfConsumerRevocationResult{}, err
	}
	result.RevokedKeys = int(tag.RowsAffected())
	result.Changed = result.Changed || result.RevokedKeys > 0
	if err := tx.Commit(ctx); err != nil {
		return PlatformTenantSelfConsumerRevocationResult{}, err
	}
	return result, nil
}
