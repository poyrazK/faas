package datapipeline

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ensureTableRecoveryReceipt(ctx context.Context, target receiptSQL) error {
	_, err := target.Exec(ctx, `CREATE TABLE IF NOT EXISTS gregale_pipeline.table_recoveries(
 pipeline_id uuid PRIMARY KEY REFERENCES gregale_pipeline.mirrors(pipeline_id) ON DELETE CASCADE,
 generation bigint NOT NULL,change_id text NOT NULL,recovery_id text NOT NULL,table_name text NOT NULL,table_oid oid NOT NULL,
 subscription_oid oid NOT NULL,publication_oid oid NOT NULL,attempt integer NOT NULL CHECK(attempt>0),
 stage text NOT NULL CHECK(stage IN ('retiring','detached','cleared','published','copying','complete')));
 ALTER TABLE gregale_pipeline.table_recoveries ADD COLUMN IF NOT EXISTS recovery_kind text NOT NULL DEFAULT '';
 DO $$ BEGIN
 IF NOT EXISTS(SELECT 1 FROM pg_constraint WHERE conrelid='gregale_pipeline.table_recoveries'::regclass AND conname='table_recoveries_recovery_kind_check' AND pg_get_constraintdef(oid) LIKE '%live%') THEN
 ALTER TABLE gregale_pipeline.table_recoveries DROP CONSTRAINT IF EXISTS table_recoveries_recovery_kind_check;
 ALTER TABLE gregale_pipeline.table_recoveries ADD CONSTRAINT table_recoveries_recovery_kind_check CHECK(recovery_kind IN ('','copy','live'));
 END IF; END $$`)
	return err
}

func loadTableRecovery(ctx context.Context, p Pipeline, target receiptSQL) (string, error) {
	r := p.SchemaChange.Recovery
	var saved TableRecovery
	var generation int64
	var change string
	err := target.QueryRow(ctx, `SELECT generation,change_id,recovery_id,table_name,table_oid,subscription_oid,publication_oid,attempt,stage,recovery_kind
 FROM gregale_pipeline.table_recoveries WHERE pipeline_id=$1`, p.ID).Scan(&generation, &change, &saved.ID, &saved.Table, &saved.TableOID, &saved.SubscriptionOID, &saved.PublicationOID, &saved.Attempt, &saved.Stage, &saved.Kind)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if generation != p.Generation {
		return "", ErrConflict
	}
	if change != p.SchemaApproval {
		if saved.Stage == "complete" {
			return "", nil
		}
		return "", ErrConflict
	}
	if saved.ID != r.ID {
		if saved.Attempt < r.Attempt {
			return "", nil
		}
		return "", ErrConflict
	}
	if tableRecoveryID(p, saved) != r.ID {
		return "", ErrConflict
	}
	return saved.Stage, nil
}

func saveTableRecoveryStage(ctx context.Context, p Pipeline, target *pgx.Conn, stage string, current func(context.Context) error) error {
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE gregale_pipeline.table_recoveries r SET stage=$4 FROM gregale_pipeline.mirrors m
 WHERE r.pipeline_id=$1 AND r.generation=$2 AND r.recovery_id=$3
 AND m.pipeline_id=r.pipeline_id AND m.generation=r.generation AND NOT m.retiring AND m.schema_change=$5`, p.ID, p.Generation, p.SchemaChange.Recovery.ID, stage, p.SchemaApproval)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Gate only the affected table and retire the previous proof atomically with the
// durable intent. The shared receipt and healthy table policies stay readable.
func beginTableRecovery(ctx context.Context, p Pipeline, target *pgx.Conn, current func(context.Context) error) error {
	r := p.SchemaChange.Recovery
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	var generation int64
	if err = tx.QueryRow(ctx, `SELECT generation FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring AND readable AND schema_change=$3 AND schema_phase<>'' FOR UPDATE`, p.ID, p.Generation, p.SchemaApproval).Scan(&generation); err != nil {
		return err
	}
	if err = gateRecoveredTable(ctx, p, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO gregale_pipeline.table_recoveries(pipeline_id,generation,recovery_id,table_name,table_oid,subscription_oid,publication_oid,attempt,change_id,recovery_kind,stage)
 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'retiring') ON CONFLICT(pipeline_id) DO UPDATE SET
 generation=excluded.generation,change_id=excluded.change_id,recovery_id=excluded.recovery_id,table_name=excluded.table_name,table_oid=excluded.table_oid,
 subscription_oid=excluded.subscription_oid,publication_oid=excluded.publication_oid,attempt=excluded.attempt,stage=excluded.stage,recovery_kind=excluded.recovery_kind
 WHERE table_recoveries.generation=excluded.generation AND
 ((table_recoveries.change_id=excluded.change_id AND table_recoveries.attempt<excluded.attempt)
 OR (table_recoveries.change_id<>excluded.change_id AND table_recoveries.stage='complete'))`, p.ID, p.Generation, r.ID, r.Table, r.TableOID, r.SubscriptionOID, r.PublicationOID, r.Attempt, p.SchemaApproval, r.Kind)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// TRUNCATE and its receipt share a transaction. Native relation removal must be
// acknowledged first. RESTRICT preserves unrelated tables and dependencies.
func clearRecoveredTable(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, current func(context.Context) error) error {
	r := p.SchemaChange.Recovery
	src, err := lockSchemaSource(ctx, p, source)
	if err != nil {
		return err
	}
	defer func() { _ = src.Rollback(ctx) }()
	actual, _, err := inspectSource(ctx, p, source)
	if err != nil {
		return err
	}
	if tablePlan(p, p.SchemaChange.Before, actual).ID != p.SchemaApproval {
		return &blocked{"table_addition_source_changed", false}
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "LOCK TABLE "+tableIdent(r.Table)+" IN ACCESS EXCLUSIVE MODE"); err != nil {
		return err
	}
	var valid bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass($1)::oid=$2 AND coalesce(obj_description(to_regclass($1),'pg_class'),'')=$3
 AND NOT EXISTS(SELECT 1 FROM pg_subscription_rel WHERE srsubid=$4 AND srrelid=$2)
 AND NOT EXISTS(SELECT 1 FROM pg_stat_subscription WHERE subid=$4 AND relid=$2 AND pid IS NOT NULL)
 AND EXISTS(SELECT 1 FROM gregale_pipeline.mirrors WHERE pipeline_id=$5 AND generation=$6 AND NOT retiring AND schema_change=$7)`, tableIdent(r.Table), r.TableOID, marker(p), r.SubscriptionOID, p.ID, p.Generation, p.SchemaApproval).Scan(&valid); err != nil {
		return err
	}
	if !valid {
		return ErrConflict
	}
	var stage string
	if err = tx.QueryRow(ctx, `SELECT stage FROM gregale_pipeline.table_recoveries WHERE pipeline_id=$1 AND generation=$2 AND recovery_id=$3 FOR UPDATE`, p.ID, p.Generation, r.ID).Scan(&stage); err != nil {
		return err
	}
	if stage != "detached" {
		return ErrConflict
	}
	if err = removeVerification(ctx, p, tx, p.SchemaChange.After); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, "TRUNCATE ONLY "+tableIdent(r.Table)+" CONTINUE IDENTITY RESTRICT"); err != nil {
		return err
	}
	if err = resetTableCopy(ctx, p, tx, r.Table); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.table_recoveries SET stage='cleared' WHERE pipeline_id=$1 AND recovery_id=$2`, p.ID, r.ID); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	var held int
	if err = src.QueryRow(ctx, "SELECT 1").Scan(&held); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// Initial-copy tables already have a false reader policy. Reissuing ALTER
// POLICY would wait behind COPY's relation lock and prevent retirement.
func gateRecoveredTable(ctx context.Context, p Pipeline, tx receiptSQL) error {
	r := p.SchemaChange.Recovery
	var gated bool
	if err := tx.QueryRow(ctx, `SELECT coalesce((SELECT c.relrowsecurity AND coalesce(obj_description(c.oid,'pg_class'),'')=$2
 AND EXISTS(SELECT 1 FROM pg_policy pol WHERE pol.polrelid=c.oid AND pol.polname='gregale_pipeline_read' AND pol.polcmd='r' AND pg_get_expr(pol.polqual,pol.polrelid)='false')
 AND NOT EXISTS(SELECT 1 FROM pg_policy pol WHERE pol.polrelid=c.oid AND pol.polname<>'gregale_pipeline_read' AND pol.polcmd IN ('r','*'))
 FROM pg_class c WHERE c.oid=to_regclass($1)),false)`, tableIdent(r.Table), marker(p)).Scan(&gated); err != nil {
		return err
	}
	if gated {
		return nil
	}
	if r.Kind == "copy" || r.Kind == "live" {
		return &blocked{"table_recovery_conflict", false}
	}
	scope := p
	scope.Tables = []string{r.Table}
	return updateTableReads(ctx, scope, tx, false)
}
