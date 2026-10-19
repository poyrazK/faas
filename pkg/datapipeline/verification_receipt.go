package datapipeline

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

type verificationSQL interface {
	receiptSQL
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func verificationNames(p Pipeline, index int) (string, string) {
	base := "verify_" + strings.ReplaceAll(p.ID, "-", "") + "_" + strconv.FormatInt(p.Generation, 10) + "_" + strconv.Itoa(index)
	return pgx.Identifier{"gregale_pipeline", base}.Sanitize(), pgx.Identifier{"gregale_pipeline", base + "_fn"}.Sanitize()
}
func verificationMarker(p Pipeline, id string) string { return marker(p) + ":verify:" + id }
func primaryColumns(r relation) []column {
	keys := []column{}
	for _, col := range r.Columns {
		if col.Key {
			keys = append(keys, col)
		}
	}
	return keys
}
func collated(name string, c column) string {
	if c.Type == "text" || strings.HasPrefix(c.Type, "character") {
		return name + ` COLLATE "pg_catalog"."C"`
	}
	return name
}
func verificationSetting(ctx context.Context, tx pgx.Tx) error {
	_, err := tx.Exec(ctx, `SELECT set_config('lock_timeout',$1,true),set_config('statement_timeout',$2,true),set_config('idle_in_transaction_session_timeout',$3,true)`, strconv.FormatInt(api.DataPipelineVerificationLockTimeout.Milliseconds(), 10), strconv.FormatInt(api.DataPipelineVerificationQueryTimeout.Milliseconds(), 10), strconv.FormatInt(api.DataPipelineLease.Milliseconds(), 10))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `SET LOCAL timezone='UTC';SET LOCAL datestyle='ISO,YMD';SET LOCAL intervalstyle='postgres';SET LOCAL extra_float_digits=3;SET LOCAL bytea_output='hex'`)
	return err
}
func loadVerification(ctx context.Context, target receiptSQL, p Pipeline) (*verificationCheckpoint, error) {
	var present bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.verifications') IS NOT NULL`).Scan(&present); err != nil || !present {
		return nil, err
	}
	var encoded []byte
	err := target.QueryRow(ctx, `SELECT checkpoint FROM gregale_pipeline.verifications WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&encoded)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var c verificationCheckpoint
	if err = json.Unmarshal(encoded, &c); err != nil {
		return nil, ErrConflict
	}
	if c.Generation != p.Generation || len(c.Progress.ID) != 64 {
		return nil, ErrConflict
	}
	return &c, nil
}
func saveVerification(ctx context.Context, target receiptSQL, p Pipeline, c *verificationCheckpoint) error {
	c.Progress.UpdatedAt = time.Now().UTC()
	encoded, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = target.Exec(ctx, `INSERT INTO gregale_pipeline.verifications(pipeline_id,generation,checkpoint) VALUES($1,$2,$3) ON CONFLICT(pipeline_id) DO UPDATE SET generation=excluded.generation,checkpoint=excluded.checkpoint`, p.ID, p.Generation, encoded)
	return err
}
func startVerification(ctx context.Context, p Pipeline, target *pgx.Conn, relations []relation, id, intent, spec string, storage []string, restarts int, current func(context.Context) error) (*verificationCheckpoint, error) {
	tx, err := target.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS gregale_pipeline.verifications(pipeline_id uuid PRIMARY KEY,generation bigint NOT NULL CHECK(generation>0),checkpoint jsonb NOT NULL CHECK(jsonb_typeof(checkpoint)='object' AND octet_length(checkpoint::text)<=4194304))`); err != nil {
		return nil, err
	}
	if err = removeVerification(ctx, p, tx, relations); err != nil {
		return nil, err
	}
	for i, r := range relations {
		if err = createVerificationRanges(ctx, p, tx, r, i, id); err != nil {
			return nil, err
		}
	}
	now := time.Now().UTC()
	c := &verificationCheckpoint{Generation: p.Generation, Spec: spec, Storage: storage, Progress: VerificationProgress{ID: id, IntentID: intent, Restarts: restarts, State: "running", Phase: "scan", TablesTotal: len(relations), StartedAt: now, UpdatedAt: now}}
	for _, r := range relations {
		c.Tables = append(c.Tables, r.Name)
		c.Progress.Tables = append(c.Progress.Tables, r.Name)
	}
	if p.SchemaChange != nil && p.SchemaApproval == p.SchemaChange.ID && p.SchemaChange.State != "complete" && p.SchemaChange.State != "withdrawn" {
		c.Progress.Phase = "backfill"
		if len(CopiedTables(p.SchemaChange)) > 0 {
			c.Progress.Phase = "scan"
		}
	}
	if err = saveVerification(ctx, tx, p, c); err != nil {
		return nil, err
	}
	if err = current(ctx); err != nil {
		return nil, err
	}
	if err = tx.Commit(ctx); err != nil {
		return nil, err
	}
	return c, nil
}
func createVerificationRanges(ctx context.Context, p Pipeline, tx pgx.Tx, r relation, index int, id string) error {
	ranges, fn := verificationNames(p, index)
	keys := primaryColumns(r)
	if len(keys) == 0 {
		return ErrInvalid
	}
	var owned bool
	if err := tx.QueryRow(ctx, `SELECT coalesce(obj_description(to_regclass($1),'pg_class'),'')=$2`, tableIdent(r.Name), marker(p)).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return ErrConflict
	}
	cols, upper := []string{}, []string{}
	for i, c := range keys {
		key := "hi" + strconv.Itoa(i)
		cols = append(cols, collated(ident(key)+" "+c.Type, c))
		upper = append(upper, ident(key))
	}
	definition := `(id bigserial PRIMARY KEY,lower_keys jsonb NOT NULL,upper_keys jsonb NOT NULL,high_inf boolean NOT NULL,dirty boolean NOT NULL DEFAULT false,` + strings.Join(cols, ",") + ")"
	if _, err := tx.Exec(ctx, "CREATE TABLE "+ranges+definition); err != nil {
		return err
	}
	indexName := ident("upper_" + strings.ReplaceAll(p.ID, "-", "") + "_" + strconv.FormatInt(p.Generation, 10) + "_" + strconv.Itoa(index))
	if _, err := tx.Exec(ctx, "CREATE UNIQUE INDEX "+indexName+" ON "+ranges+" ("+strings.Join(upper, ",")+") WHERE NOT high_inf"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "CREATE UNIQUE INDEX "+ident("end_"+strings.ReplaceAll(p.ID, "-", "")+"_"+strconv.FormatInt(p.Generation, 10)+"_"+strconv.Itoa(index))+" ON "+ranges+" (high_inf) WHERE high_inf"); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "COMMENT ON TABLE "+ranges+" IS "+literal(verificationMarker(p, id))); err != nil {
		return err
	}
	body := `DECLARE range_id bigint; was_dirty boolean; BEGIN
 IF TG_OP='TRUNCATE' THEN UPDATE ` + ranges + ` SET dirty=true WHERE NOT dirty; RETURN NULL; END IF;`
	for _, which := range []struct{ when, record string }{{"TG_OP IN ('UPDATE','DELETE')", "OLD"}, {"TG_OP IN ('UPDATE','INSERT')", "NEW"}} {
		values := []string{}
		for _, c := range keys {
			values = append(values, collated(which.record+"."+ident(c.Name), c))
		}
		body += ` IF ` + which.when + ` THEN
   SELECT id,dirty INTO range_id,was_dirty FROM ` + ranges + ` WHERE NOT high_inf AND ROW(` + strings.Join(upper, ",") + `)>=ROW(` + strings.Join(values, ",") + `) ORDER BY ` + strings.Join(upper, ",") + ` LIMIT 1;
   IF NOT FOUND THEN SELECT id,dirty INTO range_id,was_dirty FROM ` + ranges + ` WHERE high_inf LIMIT 1; END IF;
   IF range_id IS NOT NULL AND NOT was_dirty THEN UPDATE ` + ranges + ` SET dirty=true WHERE id=range_id AND NOT dirty; END IF;
  END IF;`
	}
	body += ` RETURN NULL; END`
	if _, err := tx.Exec(ctx, "CREATE FUNCTION "+fn+`() RETURNS trigger LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS `+literal(body)); err != nil {
		return err
	}
	for _, sql := range []string{
		"COMMENT ON FUNCTION " + fn + "() IS " + literal(verificationMarker(p, id)),
		"REVOKE ALL ON FUNCTION " + fn + "() FROM PUBLIC",
		"CREATE TRIGGER gregale_verify_rows AFTER INSERT OR UPDATE OR DELETE ON " + tableIdent(r.Name) + " FOR EACH ROW EXECUTE FUNCTION " + fn + "()",
		"CREATE TRIGGER gregale_verify_truncate AFTER TRUNCATE ON " + tableIdent(r.Name) + " FOR EACH STATEMENT EXECUTE FUNCTION " + fn + "()",
		"ALTER TABLE " + tableIdent(r.Name) + " ENABLE ALWAYS TRIGGER gregale_verify_rows",
		"ALTER TABLE " + tableIdent(r.Name) + " ENABLE ALWAYS TRIGGER gregale_verify_truncate",
	} {
		if _, err := tx.Exec(ctx, sql); err != nil {
			return err
		}
	}
	return nil
}
func checkVerificationTracking(ctx context.Context, p Pipeline, target receiptSQL, relations []relation, id string) error {
	for i, r := range relations {
		ranges, fn := verificationNames(p, i)
		var valid bool
		if err := target.QueryRow(ctx, `SELECT coalesce(obj_description(to_regclass($1),'pg_class'),'')=$3 AND coalesce(obj_description(to_regprocedure($2),'pg_proc'),'')=$3
   AND (SELECT count(*)=2 AND bool_and(tgenabled='A' AND tgfoid=to_regprocedure($2)) FROM pg_trigger WHERE tgrelid=to_regclass($4) AND tgname IN ('gregale_verify_rows','gregale_verify_truncate'))`, ranges, fn+"()", verificationMarker(p, id), tableIdent(r.Name)).Scan(&valid); err != nil {
			return err
		}
		if !valid {
			return &blocked{"verification_tracking_lost", true}
		}
	}
	return nil
}

// A verification receipt owns its helpers. Never adopt or drop an unrelated object.
func removeVerification(ctx context.Context, p Pipeline, target receiptSQL, relations []relation) error {
	c, err := loadVerification(ctx, target, p)
	if err != nil || c == nil {
		return err
	}
	if len(c.Tables) > 0 {
		relations = make([]relation, len(c.Tables))
		for i, name := range c.Tables {
			relations[i].Name = name
		}
	}
	for i, r := range relations {
		ranges, fn := verificationNames(p, i)
		var hasRange, ownRange, hasFn, ownFn, hasTable, ownTable bool
		if err = target.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL,coalesce(obj_description(to_regclass($1),'pg_class'),'')=$3,
   to_regprocedure($2) IS NOT NULL,coalesce(obj_description(to_regprocedure($2),'pg_proc'),'')=$3,
   to_regclass($4) IS NOT NULL,coalesce(obj_description(to_regclass($4),'pg_class'),'')=$5`, ranges, fn+"()", verificationMarker(p, c.Progress.ID), tableIdent(r.Name), marker(p)).Scan(&hasRange, &ownRange, &hasFn, &ownFn, &hasTable, &ownTable); err != nil {
			return err
		}
		if hasRange && !ownRange || hasFn && !ownFn {
			return ErrConflict
		}
		if !hasRange && !hasFn {
			continue
		}
		if hasTable {
			if !ownTable {
				return ErrConflict
			}
			var foreign bool
			if err = target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_trigger WHERE tgrelid=to_regclass($1) AND tgname IN ('gregale_verify_rows','gregale_verify_truncate') AND tgfoid IS DISTINCT FROM to_regprocedure($2))`, tableIdent(r.Name), fn+"()").Scan(&foreign); err != nil {
				return err
			}
			if foreign {
				return ErrConflict
			}
			for _, name := range []string{"gregale_verify_rows", "gregale_verify_truncate"} {
				if _, err = target.Exec(ctx, "DROP TRIGGER IF EXISTS "+ident(name)+" ON "+tableIdent(r.Name)); err != nil {
					return err
				}
			}
		}
		if hasFn {
			if _, err = target.Exec(ctx, "DROP FUNCTION "+fn+"()"); err != nil {
				return err
			}
		}
		if hasRange {
			if _, err = target.Exec(ctx, "DROP TABLE "+ranges); err != nil {
				return err
			}
		}
	}
	_, err = target.Exec(ctx, `DELETE FROM gregale_pipeline.verifications WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation)
	return err
}
func cleanupVerification(ctx context.Context, p Pipeline, target *pgx.Conn) error {
	c, err := loadVerification(ctx, target, p)
	if err != nil || c == nil {
		return err
	}
	relations := make([]relation, len(p.Tables))
	for i, name := range p.Tables {
		relations[i].Name = name
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if err = removeVerification(ctx, p, tx, relations); err != nil {
		return err
	}
	return tx.Commit(ctx)
}
