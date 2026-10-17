package datapipeline

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func ensureTableReceipt(ctx context.Context, target receiptSQL) error {
	var present bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='gregale_pipeline.mirrors'::regclass AND attname='table_stage' AND NOT attisdropped)`).Scan(&present); err != nil {
		return err
	}
	if present {
		return nil
	}
	_, err := target.Exec(ctx, `ALTER TABLE gregale_pipeline.mirrors ADD COLUMN table_stage text NOT NULL DEFAULT '' CHECK(table_stage IN ('','prepared','published','copying','refreshing','complete'))`)
	return err
}

func createMirrorTable(ctx context.Context, p Pipeline, tx pgx.Tx, owner string, r relation) error {
	parts, keys := []string{}, []string{}
	for _, c := range r.Columns {
		declaration := ident(c.Name) + " " + c.Type
		if c.NotNull {
			declaration += " NOT NULL"
		}
		parts = append(parts, declaration)
		if c.Key {
			keys = append(keys, ident(c.Name))
		}
	}
	parts = append(parts, "PRIMARY KEY ("+strings.Join(keys, ",")+")")
	for _, sql := range []string{
		"CREATE TABLE " + tableIdent(r.Name) + " (" + strings.Join(parts, ",") + ")",
		"ALTER TABLE " + tableIdent(r.Name) + " OWNER TO " + ident(owner),
		"ALTER TABLE " + tableIdent(r.Name) + " ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY gregale_pipeline_read ON " + tableIdent(r.Name) + " FOR SELECT USING (false)",
		"COMMENT ON TABLE " + tableIdent(r.Name) + " IS " + literal(marker(p)),
	} {
		if _, err := tx.Exec(ctx, sql); err != nil {
			return err
		}
	}
	return nil
}

func prepareAddedTables(ctx context.Context, p Pipeline, source *pgxpool.Pool, target *pgx.Conn, owner string, plan SchemaChange, current func(context.Context) error) error {
	locked := p
	locked.Tables = plan.RequestedTables
	src, err := lockSchemaSource(ctx, locked, source)
	if err != nil {
		return err
	}
	defer func() { _ = src.Rollback(ctx) }()
	actual, _, err := inspectSource(ctx, locked, source)
	if err != nil {
		return err
	}
	if tablePlan(p, plan.Before, actual).ID != plan.ID || p.SchemaApproval != plan.ID {
		return ErrConflict
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	var saved, phase string
	if err = tx.QueryRow(ctx, `SELECT schema_hash,schema_phase FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring AND readable FOR UPDATE`, p.ID, p.Generation).Scan(&saved, &phase); err != nil {
		return err
	}
	if saved != plan.BeforeHash || phase != "" {
		return ErrConflict
	}
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+ident(owner)); err != nil {
		return err
	}
	for _, r := range plan.After {
		if containsTable(plan.AddedTables, r.Name) {
			if err = createMirrorTable(ctx, p, tx, owner, r); err != nil {
				return err
			}
		}
	}
	if _, err = tx.Exec(ctx, "SET LOCAL ROLE NONE"); err != nil {
		return err
	}
	encoded, _ := json.Marshal(plan.After)
	if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET schema_hash=$3,schema_spec=$4,schema_change=$5,schema_phase='verifying',table_stage='prepared',tables=$6 WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, plan.AfterHash, encoded, plan.ID, plan.RequestedTables); err != nil {
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

func saveTableStage(ctx context.Context, p Pipeline, target *pgx.Conn, stage string, current func(context.Context) error) error {
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	tag, err := tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET table_stage=$4 WHERE pipeline_id=$1 AND generation=$2 AND schema_change=$3 AND NOT retiring`, p.ID, p.Generation, p.SchemaApproval, stage)
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
