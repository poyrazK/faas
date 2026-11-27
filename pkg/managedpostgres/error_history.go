package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/dbqueryerrors"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *Service) errorHistoryDatabase(ctx context.Context, account, id string) (*PostgresStore, Database, error) {
	d, err := customerDatabase(ctx, s.store, account, id)
	if err != nil {
		return nil, Database{}, err
	}
	w, ok := s.store.(*PostgresStore)
	if !ok {
		return nil, Database{}, ErrUnsupported
	}
	backend, err := s.registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		return nil, Database{}, err
	}
	if backend.Driver != "firecracker" {
		return nil, Database{}, ErrUnsupported
	}
	return w, d, nil
}

func (s *Service) SetErrorHistory(ctx context.Context, account, id string, enabled bool) (api.ManagedPostgresErrorHistory, error) {
	store, d, err := s.errorHistoryDatabase(ctx, account, id)
	if err != nil {
		return api.ManagedPostgresErrorHistory{}, err
	}
	if enabled && (d.State != StateReady || databaseDataResource(d) == "") {
		return api.ManagedPostgresErrorHistory{}, ErrConflict
	}
	dbID, err := postgresUUID(d.ID)
	if err != nil {
		return api.ManagedPostgresErrorHistory{}, err
	}
	if err := sqlc.New().SetNativePostgresErrorHistory(ctx, store.catalog, sqlc.SetNativePostgresErrorHistoryParams{
		DatabaseID: dbID, Epoch: pgtype.UUID{Bytes: uuid.New(), Valid: true}, Enabled: enabled,
	}); err != nil {
		return api.ManagedPostgresErrorHistory{}, mapPostgresError(err)
	}
	return s.readErrorHistory(ctx, store, d.ID, time.Hour, "", dbqueryerrors.DefaultResultLimit)
}

func (s *Service) ErrorHistory(ctx context.Context, account, id string, since time.Duration, sqlstate string, limit int) (api.ManagedPostgresErrorHistory, error) {
	if since < time.Second || since > dbqueryerrors.Retention ||
		limit < 1 || limit > dbqueryerrors.MaxResultLimit || sqlstate != "" && !dbqueryerrors.ValidSQLState(sqlstate) {
		return api.ManagedPostgresErrorHistory{}, ErrInvalid
	}
	store, d, err := s.errorHistoryDatabase(ctx, account, id)
	if err != nil {
		return api.ManagedPostgresErrorHistory{}, err
	}
	return s.readErrorHistory(ctx, store, d.ID, since, sqlstate, limit)
}

func (s *Service) readErrorHistory(ctx context.Context, store *PostgresStore, database string, since time.Duration, code string, limit int) (api.ManagedPostgresErrorHistory, error) {
	out := api.ManagedPostgresErrorHistory{
		DatabaseID: database, Since: s.now().UTC().Add(-since), Events: []api.ManagedPostgresQueryError{},
	}
	dbID, err := postgresUUID(database)
	if err != nil {
		return out, err
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	err = pgx.BeginTxFunc(ctx, store.catalog, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly}, func(tx pgx.Tx) error {
		q := sqlc.New()
		enabled, err := q.GetNativePostgresErrorHistory(ctx, tx, dbID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		out.Enabled = enabled
		// Callers validate the limit to 1–100 before requesting one extra row.
		rows, err := q.ListNativePostgresErrorEvents(ctx, tx, sqlc.ListNativePostgresErrorEventsParams{
			DatabaseID: dbID, Since: pgtype.Timestamptz{Time: out.Since, Valid: true}, FilterSqlstate: code, ResultLimit: int32(limit + 1),
		})
		if err != nil {
			return err
		}
		out.HasMore = len(rows) > limit
		for _, row := range rows[:min(len(rows), limit)] {
			out.Events = append(out.Events, api.ManagedPostgresQueryError{
				ObservedAt: row.ObservedAt.Time, Severity: row.Severity, SQLState: row.Sqlstate, BindingID: databaseUUID(row.BindingID),
			})
		}
		return nil
	})
	return out, mapPostgresError(err)
}
