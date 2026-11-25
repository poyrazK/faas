package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbqueryinsights"
)

func (s *Service) queryInsightsDatabase(ctx context.Context, account, id string) (*PostgresStore, Database, string, error) {
	d, err := customerDatabase(ctx, s.store, account, id)
	if err != nil {
		return nil, Database{}, "", err
	}
	p, ok := s.store.(*PostgresStore)
	if !ok || d.Spec.Availability != AvailabilitySingleZone {
		return nil, Database{}, "", ErrUnsupported
	}
	backend, err := s.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		return nil, Database{}, "", err
	}
	native, ok := backend.Provider.(interface{ QueryInsightsDatabase() string })
	if !ok || backend.Driver != "firecracker" {
		return nil, Database{}, "", ErrUnsupported
	}
	return p, d, native.QueryInsightsDatabase(), nil
}

func (s *Service) SetQueryInsights(ctx context.Context, account, id string, enabled bool) (api.ManagedPostgresQueryInsights, error) {
	store, d, name, err := s.queryInsightsDatabase(ctx, account, id)
	if err != nil {
		return api.ManagedPostgresQueryInsights{}, err
	}
	if d.State != StateReady || d.ProviderResourceID == "" {
		return api.ManagedPostgresQueryInsights{}, ErrConflict
	}
	err = pgx.BeginFunc(ctx, store.pool, func(tx pgx.Tx) error {
		var eligible bool
		err := tx.QueryRow(ctx, `
			SELECT NOT v.replicated AND v.standby_node IS NULL AND d.state='ready' AND d.availability='single_zone'
			FROM database_vms v JOIN managed_postgres_databases d ON d.provider_resource_id=v.database_id::text
			WHERE v.database_id=$1::uuid AND d.id=$2::uuid FOR UPDATE OF v
		`, d.ProviderResourceID, d.ID).Scan(&eligible)
		if errors.Is(err, pgx.ErrNoRows) || err == nil && !eligible {
			return ErrConflict
		}
		if err != nil {
			return err
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO native_postgres_query_settings (database_id, database_name, enabled, updated_at)
			VALUES ($1::uuid,$2,$3,clock_timestamp())
			ON CONFLICT (database_id) DO UPDATE SET database_name=EXCLUDED.database_name, enabled=EXCLUDED.enabled,
				updated_at=EXCLUDED.updated_at, attempted_at=NULL, sampled_at=NULL, baseline=NULL
			WHERE native_postgres_query_settings.enabled <> EXCLUDED.enabled
			   OR native_postgres_query_settings.database_name <> EXCLUDED.database_name
		`, d.ProviderResourceID, name, enabled)
		return err
	})
	if err != nil {
		return api.ManagedPostgresQueryInsights{}, mapPostgresError(err)
	}
	return s.readQueryInsights(ctx, store, d, time.Hour)
}

func (s *Service) QueryInsights(ctx context.Context, account, id string, since time.Duration) (api.ManagedPostgresQueryInsights, error) {
	if since < time.Second || since > dbqueryinsights.Retention {
		return api.ManagedPostgresQueryInsights{}, ErrInvalid
	}
	store, d, _, err := s.queryInsightsDatabase(ctx, account, id)
	if err != nil {
		return api.ManagedPostgresQueryInsights{}, err
	}
	return s.readQueryInsights(ctx, store, d, since)
}

func (s *Service) readQueryInsights(ctx context.Context, p *PostgresStore, d Database, since time.Duration) (api.ManagedPostgresQueryInsights, error) {
	now := s.now().UTC()
	out := api.ManagedPostgresQueryInsights{DatabaseID: d.ID, Since: now.Add(-since), Queries: []api.ManagedPostgresQueryInsight{}}
	if d.ProviderResourceID == "" {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err := pgx.BeginTxFunc(ctx, p.pool, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		err := tx.QueryRow(ctx, `SELECT enabled, sampled_at FROM native_postgres_query_settings WHERE database_id=$1::uuid`,
			d.ProviderResourceID).Scan(&out.Enabled, &out.LastSampleAt)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		err = tx.QueryRow(ctx, `
			SELECT max(ended_at), coalesce(sum(extract(epoch FROM ended_at-started_at)),0)::bigint
			FROM native_postgres_query_intervals WHERE database_id=$1::uuid
			  AND started_at >= $2 AND ended_at <= $3
		`, d.ProviderResourceID, out.Since, now).Scan(&out.ObservedThrough, &out.ObservedSeconds)
		if err != nil {
			return err
		}
		rows, err := tx.Query(ctx, dbqueryinsights.RankedSQL, d.ProviderResourceID, out.Since, now)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var q api.ManagedPostgresQueryInsight
			if err := rows.Scan(&q.BootAt, &q.QueryID, &q.Calls, &q.TotalExecutionMS); err != nil {
				return err
			}
			q.MeanExecutionMS = q.TotalExecutionMS / float64(q.Calls)
			out.Queries = append(out.Queries, q)
		}
		return rows.Err()
	})
	return out, mapPostgresError(err)
}
