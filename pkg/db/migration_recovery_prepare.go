package db

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/db/migrationsqlc"
	"github.com/pressly/goose/v3"
)

const migrationRecoveryAuditSource = "20261002015945515_application_standard_ledger_recovery_audit.sql"

// Prepare applies only the additive audit migration through Goose. Its normal
// ledger event is not a repair of any historical gap. This explicit step makes
// reviewed recovery available when an earlier frozen migration blocks Up.
func PrepareApplicationStandardLedgerRecovery(ctx context.Context, pool *pgxpool.Pool) error {
	if pool != nil && !migrationRecoveryLocalConfig(DirectPool(pool).Config().ConnConfig) {
		return ErrMigrationRecoveryTransport
	}
	release, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		return err
	}
	defer func() { _ = release(context.WithoutCancel(ctx)) }()
	sources, _, err := migrationRecoverySources()
	if err != nil {
		return err
	}
	q := migrationsqlc.New()
	rows, err := q.ReadMigrationRecoveryLedger(ctx, DirectPool(pool))
	if err != nil {
		return err
	}
	if _, err := migrationRecoveryHistory(rows, sources); err != nil {
		return err
	}
	if _, err := migrationRecoveryTarget(ctx, DirectPool(pool)); err != nil {
		return err
	}
	writers, err := q.CheckMigrationRecoveryWriters(ctx, DirectPool(pool))
	if err != nil {
		return err
	}
	if !writers.Valid || !writers.Bool {
		return ErrMigrationRecoverySchema
	}
	return migrationRecoveryPrepareAudit(ctx, pool, sources)
}

func migrationRecoveryPrepareAudit(ctx context.Context, pool *pgxpool.Pool, sources []migrations.Source) error {
	exclude := make([]int64, 0, len(sources)-1)
	found := false
	for _, source := range sources {
		if source.Filename == migrationRecoveryAuditSource {
			found = true
		} else {
			exclude = append(exclude, source.Version)
		}
	}
	if !found {
		return ErrMigrationRecoverySource
	}
	connString := stdlib.RegisterConnConfig(DirectPool(pool).Config().ConnConfig)
	defer stdlib.UnregisterConnConfig(connString)
	sqlDB, err := sql.Open("pgx", connString)
	if err != nil {
		return fmt.Errorf("prepare ledger recovery: %w", err)
	}
	defer func() { _ = sqlDB.Close() }()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS,
		goose.WithExcludeVersions(exclude), goose.WithAllowOutofOrder(true), goose.WithDisableGlobalRegistry(true))
	if err != nil {
		return err
	}
	_, err = provider.Up(ctx)
	return err
}
