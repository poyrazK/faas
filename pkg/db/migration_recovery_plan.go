package db

import (
	"bytes"
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	faasschema "github.com/onebox-faas/faas"
	"github.com/onebox-faas/faas/pkg/db/migrationsqlc"
)

// Preview writes no target state. It binds the exact ledger events and the
// complete expanded canonical schema to the reviewed migration source set.
func PreviewApplicationStandardLedgerRecovery(ctx context.Context, pool *pgxpool.Pool) (MigrationLedgerRecoveryPlan, error) {
	if pool != nil && !migrationRecoveryLocalConfig(DirectPool(pool).Config().ConnConfig) {
		return MigrationLedgerRecoveryPlan{}, ErrMigrationRecoveryTransport
	}
	release, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		return MigrationLedgerRecoveryPlan{}, err
	}
	defer release(context.WithoutCancel(ctx))
	tx, err := DirectPool(pool).BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return MigrationLedgerRecoveryPlan{}, err
	}
	defer tx.Rollback(ctx)
	plan, err := migrationRecoveryPlan(ctx, tx, DirectPool(pool).Config().ConnConfig)
	if err != nil {
		return plan, err
	}
	return plan, tx.Commit(ctx)
}

func migrationRecoveryPlan(ctx context.Context, tx pgx.Tx, cfg *pgx.ConnConfig) (MigrationLedgerRecoveryPlan, error) {
	plan := MigrationLedgerRecoveryPlan{Format: "gregale.application-standard-ledger-recovery.v1"}
	sources, digest, err := migrationRecoverySources()
	if err != nil {
		return plan, err
	}
	plan.SourceHash = digest
	plan.TargetHash, err = migrationRecoveryTarget(ctx, tx)
	if err != nil {
		return plan, err
	}
	rows, err := migrationsqlc.New().ReadMigrationRecoveryLedger(ctx, tx)
	if err != nil {
		return plan, err
	}
	latest, err := migrationRecoveryHistory(rows, sources)
	if err != nil {
		return plan, err
	}
	if err := migrationRecoveryMissing(&plan, latest, sources); err != nil {
		return plan, err
	}
	plan.LedgerHash, err = migrationRecoveryDigest(rows)
	if err != nil {
		return plan, err
	}
	if err := migrationRecoveryVerifySchema(ctx, tx, cfg, &plan); err != nil {
		return plan, err
	}
	plan.ApprovalHash, err = migrationRecoveryDigest(plan)
	return plan, err
}

func migrationRecoveryVerifySchema(ctx context.Context, tx pgx.Tx, cfg *pgx.ConnConfig, plan *MigrationLedgerRecoveryPlan) error {
	q := migrationsqlc.New()
	writers, err := q.CheckMigrationRecoveryWriters(ctx, tx)
	if err != nil {
		return err
	}
	if !writers.Valid || !writers.Bool {
		return ErrMigrationRecoverySchema
	}
	backfill, err := q.CheckMigrationRecoveryBackfills(ctx, tx)
	if err != nil {
		return err
	}
	if !backfill.Valid.Valid || !backfill.Valid.Bool {
		return ErrMigrationRecoveryBackfill
	}
	plan.ApplicationCount = backfill.ApplicationCount
	snapshot, err := q.ExportMigrationRecoverySnapshot(ctx, tx)
	if err != nil {
		return err
	}
	raw, err := migrationRecoverySchema(ctx, cfg, snapshot)
	if err != nil {
		return err
	}
	if !bytes.Equal(raw, []byte(faasschema.CanonicalSQL())) {
		return ErrMigrationRecoverySchema
	}
	plan.SchemaHash = migrationRecoveryHash(raw)
	return nil
}
