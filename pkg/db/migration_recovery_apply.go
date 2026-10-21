package db

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/migrationsqlc"
)

type migrationRecoveryAudit struct {
	Plan   MigrationLedgerRecoveryPlan    `json:"approval_plan"`
	Events []migrationsqlc.GooseDbVersion `json:"ledger_events"`
}

// Repair is an explicit reviewed operation. It appends current recovery events,
// never fabricated historical clocks, and commits its immutable receipt in the
// same transaction. Daemon startup does not invoke this operation.
func ApplyApplicationStandardLedgerRecovery(ctx context.Context, pool *pgxpool.Pool, approval string) (MigrationLedgerRecoveryReceipt, error) {
	if pool != nil && !migrationRecoveryLocalConfig(DirectPool(pool).Config().ConnConfig) {
		return MigrationLedgerRecoveryReceipt{}, ErrMigrationRecoveryTransport
	}
	raw, err := hex.DecodeString(approval)
	if err != nil || len(raw) != 32 || approval != migrationRecoveryHex(raw) {
		return MigrationLedgerRecoveryReceipt{}, ErrMigrationRecoveryStale
	}
	release, err := AcquireMigrationLock(ctx, pool)
	if err != nil {
		return MigrationLedgerRecoveryReceipt{}, err
	}
	defer func() { _ = release(context.WithoutCancel(ctx)) }()
	tx, err := DirectPool(pool).BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return MigrationLedgerRecoveryReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	receipt, err := migrationRecoveryApply(ctx, tx, DirectPool(pool).Config().ConnConfig, approval)
	if err != nil {
		return MigrationLedgerRecoveryReceipt{}, err
	}
	return receipt, tx.Commit(ctx)
}

func migrationRecoveryApply(ctx context.Context, tx pgx.Tx, cfg *pgx.ConnConfig, approval string) (MigrationLedgerRecoveryReceipt, error) {
	q := migrationsqlc.New()
	if err := q.LockMigrationRecoveryLedger(ctx, tx); err != nil {
		return MigrationLedgerRecoveryReceipt{}, err
	}
	if err := q.LockMigrationRecoveryBackfills(ctx, tx); err != nil {
		return MigrationLedgerRecoveryReceipt{}, err
	}
	old, err := q.GetMigrationRecoveryReceipt(ctx, tx, approval)
	if err == nil {
		if err := migrationRecoveryRetry(ctx, tx, old); err != nil {
			return MigrationLedgerRecoveryReceipt{}, err
		}
		return migrationRecoveryReceipt(old), nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return MigrationLedgerRecoveryReceipt{}, err
	}
	plan, err := migrationRecoveryPlan(ctx, tx, cfg)
	if err != nil {
		return MigrationLedgerRecoveryReceipt{}, err
	}
	if plan.ApprovalHash != approval {
		return MigrationLedgerRecoveryReceipt{}, ErrMigrationRecoveryStale
	}
	row, err := migrationRecoveryRecord(ctx, tx, plan)
	if err != nil {
		return MigrationLedgerRecoveryReceipt{}, err
	}
	return migrationRecoveryReceipt(row), nil
}

func migrationRecoveryHex(raw []byte) string { return hex.EncodeToString(raw) }

func migrationRecoveryRecord(ctx context.Context, tx pgx.Tx, plan MigrationLedgerRecoveryPlan) (migrationsqlc.ApplicationStandardLedgerRecovery, error) {
	audit := migrationRecoveryAudit{Plan: plan}
	versions := make([]int64, 0, len(plan.Repair))
	q := migrationsqlc.New()
	for _, candidate := range plan.Repair {
		row, err := q.RecordRecoveredMigrationVersion(ctx, tx, candidate.Version)
		if err != nil {
			return migrationsqlc.ApplicationStandardLedgerRecovery{}, err
		}
		audit.Events = append(audit.Events, row)
		versions = append(versions, candidate.Version)
	}
	raw, err := json.Marshal(audit)
	if err != nil {
		return migrationsqlc.ApplicationStandardLedgerRecovery{}, err
	}
	return q.RecordMigrationRecoveryReceipt(ctx, tx, migrationsqlc.RecordMigrationRecoveryReceiptParams{
		ApprovalHash: plan.ApprovalHash, TargetHash: plan.TargetHash, SchemaHash: plan.SchemaHash, SourceHash: plan.SourceHash,
		LedgerHash: plan.LedgerHash, Plan: raw, RepairedVersions: versions})
}

func migrationRecoveryRetry(ctx context.Context, tx pgx.Tx, row migrationsqlc.ApplicationStandardLedgerRecovery) error {
	var audit migrationRecoveryAudit
	if json.Unmarshal(row.Plan, &audit) != nil || !migrationRecoveryAuditMatches(audit, row) {
		return ErrMigrationRecoveryHistory
	}
	target, err := migrationRecoveryTarget(ctx, tx)
	if err != nil {
		return err
	}
	if target != row.TargetHash || audit.Plan.TargetHash != row.TargetHash {
		return ErrMigrationRecoveryStale
	}
	audit.Plan.ApprovalHash = ""
	digest, err := migrationRecoveryDigest(audit.Plan)
	if err != nil {
		return err
	}
	if digest != row.ApprovalHash {
		return ErrMigrationRecoveryHistory
	}
	return migrationRecoveryRetryEvents(ctx, tx, audit.Events)
}

func migrationRecoveryAuditMatches(audit migrationRecoveryAudit, row migrationsqlc.ApplicationStandardLedgerRecovery) bool {
	plan := audit.Plan
	if plan.ApprovalHash != row.ApprovalHash || plan.SchemaHash != row.SchemaHash || plan.SourceHash != row.SourceHash ||
		plan.LedgerHash != row.LedgerHash || plan.TargetHash != row.TargetHash || len(audit.Events) != len(row.RepairedVersions) ||
		len(plan.Repair) != len(audit.Events) || len(audit.Events) == 0 {
		return false
	}
	seen := make(map[int64]bool, len(audit.Events))
	for i, event := range audit.Events {
		if event.VersionID != row.RepairedVersions[i] || event.VersionID != plan.Repair[i].Version || seen[event.VersionID] {
			return false
		}
		seen[event.VersionID] = true
	}
	return true
}

func migrationRecoveryRetryEvents(ctx context.Context, tx pgx.Tx, events []migrationsqlc.GooseDbVersion) error {
	rows, err := migrationsqlc.New().ReadMigrationRecoveryLedger(ctx, tx)
	if err != nil {
		return err
	}
	sources, _, err := migrationRecoverySources()
	if err != nil {
		return err
	}
	latest, err := migrationRecoveryHistory(rows, sources)
	if err != nil {
		return err
	}
	for _, event := range events {
		current, ok := latest[event.VersionID]
		if !ok || !event.IsApplied || !event.Tstamp.Valid || current.ID != event.ID || current.IsApplied != event.IsApplied ||
			!current.Tstamp.Time.Equal(event.Tstamp.Time) {
			return ErrMigrationRecoveryHistory
		}
	}
	return nil
}

func migrationRecoveryReceipt(row migrationsqlc.ApplicationStandardLedgerRecovery) MigrationLedgerRecoveryReceipt {
	return MigrationLedgerRecoveryReceipt{ApprovalHash: row.ApprovalHash, TargetHash: row.TargetHash, Actor: row.Actor,
		RepairedVersions: slices.Clone(row.RepairedVersions), RecoveredAt: row.RecoveredAt.Time}
}
