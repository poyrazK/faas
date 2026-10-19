package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func ensureTableCancellationReceipt(ctx context.Context, target receiptSQL) error {
	_, err := target.Exec(ctx, `CREATE TABLE IF NOT EXISTS gregale_pipeline.table_cancellations(
 pipeline_id uuid PRIMARY KEY REFERENCES gregale_pipeline.mirrors(pipeline_id) ON DELETE CASCADE,
 generation bigint NOT NULL,change_id text NOT NULL,cancellation_id text NOT NULL,intent jsonb NOT NULL,
 stage text NOT NULL CHECK(stage IN ('retiring','detached','archived','complete')),result jsonb NOT NULL DEFAULT '{}')`)
	return err
}

func loadTableCancellation(ctx context.Context, p Pipeline, target receiptSQL) (string, *SchemaChange, error) {
	var generation int64
	var change, id, stage string
	var intent, result []byte
	err := target.QueryRow(ctx, `SELECT generation,change_id,cancellation_id,intent,stage,result FROM gregale_pipeline.table_cancellations WHERE pipeline_id=$1`, p.ID).Scan(&generation, &change, &id, &intent, &stage, &result)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, err
	}
	if generation != p.Generation {
		return "", nil, ErrConflict
	}
	if id != p.SchemaChange.Cancellation.ID {
		if stage == "complete" {
			return "", nil, nil
		}
		return "", nil, ErrConflict
	}
	var c TableCancellation
	if json.Unmarshal(intent, &c) != nil || change != p.SchemaApproval || tableCancellationID(p, c) != id || tableCancellationID(p, *p.SchemaChange.Cancellation) != id {
		return "", nil, ErrConflict
	}
	if stage != "complete" {
		return stage, nil, nil
	}
	var plan SchemaChange
	expected := cancellationResult(p)
	if json.Unmarshal(result, &plan) != nil || plan.ID != expected.ID || !slices.Equal(plan.RequestedTables, expected.RequestedTables) || plan.Cancellation == nil || plan.Cancellation.ID != id || plan.Cancellation.State != "complete" {
		return "", nil, ErrConflict
	}
	return stage, &plan, nil
}

func cancellationMirrorLock(ctx context.Context, p Pipeline, tx pgx.Tx) error {
	var generation int64
	return tx.QueryRow(ctx, `SELECT generation FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring AND readable
 AND ((schema_change=$3 AND schema_phase<>'') OR (schema_phase='' AND schema_hash=$4)) FOR UPDATE`, p.ID, p.Generation, p.SchemaApproval, p.SchemaChange.BeforeHash).Scan(&generation)
}

func cancellationGate(ctx context.Context, p Pipeline, target receiptSQL) error {
	c := p.SchemaChange.Cancellation
	if c.TableOID == 0 {
		var absent bool
		if err := target.QueryRow(ctx, `SELECT to_regclass($1) IS NULL`, tableIdent(c.Table)).Scan(&absent); err != nil {
			return err
		}
		if !absent {
			return ErrConflict
		}
		return nil
	}
	scope, plan := p, *p.SchemaChange
	plan.Recovery = &TableRecovery{Kind: "copy", Table: c.Table}
	scope.SchemaChange = &plan
	return gateRecoveredTable(ctx, scope, target)
}

func beginTableCancellation(ctx context.Context, p Pipeline, target *pgx.Conn, current func(context.Context) error) error {
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if err = cancellationMirrorLock(ctx, p, tx); err != nil {
		return err
	}
	if p.SchemaChange.Cancellation.Kind == "live" {
		if _, err = tx.Exec(ctx, "LOCK TABLE "+tableIdent(p.SchemaChange.Cancellation.Table)+" IN ACCESS EXCLUSIVE MODE"); err != nil {
			return err
		}
		if err = validateLiveRemovalTable(ctx, p, tx); err != nil {
			return err
		}
		scope := p
		scope.Tables = []string{p.SchemaChange.Cancellation.Table}
		if err = updateTableReads(ctx, scope, tx, false); err != nil {
			return err
		}
	} else {
		if err = cancellationGate(ctx, p, tx); err != nil {
			return err
		}
	}
	c := p.SchemaChange.Cancellation
	if c.TableOID != 0 {
		if err = cancellationRetentionCapacity(ctx, tx); err != nil {
			return err
		}
	}
	encoded, _ := json.Marshal(c)
	tag, err := tx.Exec(ctx, `INSERT INTO gregale_pipeline.table_cancellations(pipeline_id,generation,change_id,cancellation_id,intent,stage)
 VALUES($1,$2,$3,$4,$5,'retiring') ON CONFLICT(pipeline_id) DO UPDATE SET generation=excluded.generation,change_id=excluded.change_id,
 cancellation_id=excluded.cancellation_id,intent=excluded.intent,stage=excluded.stage,result='{}'
 WHERE table_cancellations.generation=excluded.generation AND table_cancellations.stage='complete' AND table_cancellations.cancellation_id<>excluded.cancellation_id`, p.ID, p.Generation, p.SchemaApproval, c.ID, encoded)
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

func saveCancellationStage(ctx context.Context, p Pipeline, target *pgx.Conn, stage string, current func(context.Context) error) error {
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = cancellationMirrorLock(ctx, p, tx); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `UPDATE gregale_pipeline.table_cancellations SET stage=$4 WHERE pipeline_id=$1 AND generation=$2 AND cancellation_id=$3 AND stage<>'complete'`, p.ID, p.Generation, p.SchemaChange.Cancellation.ID, stage)
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

func retainedTableName(c TableCancellation) string {
	return "retained_" + strconv.FormatUint(uint64(c.TableOID), 10) + "_" + c.ID[:12]
}

// Move and stage commit together, after native retirement. Never TRUNCATE or
// DROP customer rows. The private schema has no app-reader USAGE grants.
func archiveCancelledTable(ctx context.Context, p Pipeline, target *pgx.Conn, current func(context.Context) error) error {
	c := p.SchemaChange.Cancellation
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if err = cancellationMirrorLock(ctx, p, tx); err != nil {
		return err
	}
	var stage string
	if err = tx.QueryRow(ctx, `SELECT stage FROM gregale_pipeline.table_cancellations WHERE pipeline_id=$1 AND generation=$2 AND cancellation_id=$3 FOR UPDATE`, p.ID, p.Generation, c.ID).Scan(&stage); err != nil {
		return err
	}
	if stage != "detached" {
		return ErrConflict
	}
	if err = cancellationGate(ctx, p, tx); err != nil {
		return err
	}
	if c.TableOID != 0 {
		if _, err = tx.Exec(ctx, "LOCK TABLE "+tableIdent(c.Table)+" IN ACCESS EXCLUSIVE MODE"); err != nil {
			return err
		}
		var valid bool
		if err = tx.QueryRow(ctx, `SELECT to_regclass($1)::oid=$2 AND coalesce(obj_description(to_regclass($1),'pg_class'),'')=$3
 AND NOT EXISTS(SELECT 1 FROM pg_subscription_rel WHERE srsubid=$4 AND srrelid=$2)
 AND NOT EXISTS(SELECT 1 FROM pg_stat_subscription WHERE subid=$4 AND relid=$2 AND pid IS NOT NULL)
 AND NOT EXISTS(SELECT 1 FROM pg_namespace n,aclexplode(n.nspacl) a WHERE n.nspname='gregale_pipeline' AND a.privilege_type='USAGE' AND a.grantee<>n.nspowner)`, tableIdent(c.Table), c.TableOID, marker(p), c.SubscriptionOID).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return ErrConflict
		}
		if err = cancellationRetentionCapacity(ctx, tx); err != nil {
			return err
		}
	}
	if err = removeVerification(ctx, p, tx, p.SchemaChange.After); err != nil {
		return err
	}
	if c.TableOID != 0 {
		name := retainedTableName(*c)
		if err = renameRetainedIndexes(ctx, tx, c.TableOID, name); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "ALTER TABLE "+tableIdent(c.Table)+" RENAME TO "+ident(name)); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "ALTER TABLE "+tableIdent(name)+" SET SCHEMA gregale_pipeline"); err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, "COMMENT ON TABLE gregale_pipeline."+ident(name)+" IS "+literal(marker(p)+":retained:"+c.Table+":"+c.ID)); err != nil {
			return err
		}
	}
	var recovery bool
	if err = tx.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.table_recoveries') IS NOT NULL`).Scan(&recovery); err != nil {
		return err
	}
	if recovery {
		if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.table_recoveries SET stage='complete' WHERE pipeline_id=$1 AND generation=$2 AND change_id=$3`, p.ID, p.Generation, p.SchemaApproval); err != nil {
			return err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.table_cancellations SET stage='archived' WHERE pipeline_id=$1 AND cancellation_id=$2`, p.ID, c.ID); err != nil {
		return err
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func renameRetainedIndexes(ctx context.Context, tx pgx.Tx, table uint32, name string) error {
	rows, err := tx.Query(ctx, `SELECT c.oid,c.relname FROM pg_index i JOIN pg_class c ON c.oid=i.indexrelid WHERE i.indrelid=$1 ORDER BY c.oid`, table)
	if err != nil {
		return err
	}
	type index struct {
		oid  uint32
		name string
	}
	indexes, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (index, error) {
		var i index
		err := row.Scan(&i.oid, &i.name)
		return i, err
	})
	if err != nil {
		return err
	}
	for _, i := range indexes {
		if _, err = tx.Exec(ctx, "ALTER INDEX "+tableIdent(i.name)+" RENAME TO "+ident(name+"_i"+strconv.FormatUint(uint64(i.oid), 10))); err != nil {
			return err
		}
	}
	return nil
}

func cancellationRetentionCapacity(ctx context.Context, target receiptSQL) error {
	var count int
	if err := target.QueryRow(ctx, `SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='gregale_pipeline' AND c.relkind='r' AND starts_with(c.relname,'retained_')`).Scan(&count); err != nil {
		return err
	}
	if count >= api.DataPipelineRetainedTablesMax {
		return &blocked{"table_cancellation_retention_limit", false}
	}
	return nil
}

func completeTableCancellation(ctx context.Context, p Pipeline, target *pgx.Conn, result *SchemaChange, stage string, current func(context.Context) error) error {
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if err = cancellationMirrorLock(ctx, p, tx); err != nil {
		return err
	}
	var phase string
	if err = tx.QueryRow(ctx, `SELECT schema_phase FROM gregale_pipeline.mirrors WHERE pipeline_id=$1`, p.ID).Scan(&phase); err != nil {
		return err
	}
	if phase != "" || len(result.AddedTables) == 0 {
		copies, err := loadTableCopies(ctx, p, tx)
		if err != nil {
			return err
		}
		result.Copies = slices.DeleteFunc(copies, func(c TableCopyProgress) bool { return c.Table == p.SchemaChange.Cancellation.Table })
		phase = "verifying"
		if len(result.AddedTables) == 0 {
			phase = ""
			stage = "complete"
		}
		spec, _ := json.Marshal(result.After)
		if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_hash=$3,schema_spec=$4,schema_change=$5,schema_phase=$6,table_stage=$7,tables=$8 WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, result.AfterHash, spec, result.ID, phase, stage, result.RequestedTables); err != nil {
			return err
		}
		next := p
		next.SchemaApproval, next.SchemaChange = result.ID, result
		if err = saveTableCopies(ctx, next, tx, result.Copies); err != nil {
			return err
		}
	}
	encoded, _ := json.Marshal(result)
	tag, err := tx.Exec(ctx, `UPDATE gregale_pipeline.table_cancellations SET stage='complete',result=$4 WHERE pipeline_id=$1 AND generation=$2 AND cancellation_id=$3 AND stage='archived'`, p.ID, p.Generation, p.SchemaChange.Cancellation.ID, encoded)
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
