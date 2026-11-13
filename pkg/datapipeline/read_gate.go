package datapipeline

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/pipelineread"
)

func (e *Engine) refreshPipelineReadGate(ctx context.Context, p Pipeline, target *pgx.Conn, current func(context.Context) error) error {
	if e.RefreshReadGate == nil {
		return nil
	}
	var present bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.mirrors') IS NOT NULL`).Scan(&present); err != nil || !present {
		return err
	}
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring)`, p.ID, p.Generation).Scan(&present); err != nil || !present {
		return err
	}
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_attribute WHERE attrelid='gregale_pipeline.mirrors'::regclass AND attname='read_gate_refresh' AND NOT attisdropped)`).Scan(&present); err != nil {
		return err
	}
	if !present {
		if _, err := target.Exec(ctx, `ALTER TABLE gregale_pipeline.mirrors ADD COLUMN read_gate_refresh text NOT NULL DEFAULT ''`); err != nil {
			return err
		}
	}
	var saved string
	if err := target.QueryRow(ctx, `SELECT read_gate_refresh FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&saved); err != nil {
		return err
	}
	if saved == pipelineread.Version {
		return nil
	}
	if err := e.RefreshReadGate(ctx, p, pipelineread.Version); err != nil {
		return &blocked{"read_api_refresh_failed", false}
	}
	if err := current(ctx); err != nil {
		return err
	}
	_, err := target.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET read_gate_refresh=$3 WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring`, p.ID, p.Generation, pipelineread.Version)
	return err
}

// The worker owns installation and grants. Serving logins gain no access to
// private receipts; their only new privilege is the bounded availability check.
func ensurePipelineReadGate(ctx context.Context, p Pipeline, target *pgx.Conn) error {
	var present bool
	if err := target.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.mirrors') IS NOT NULL`).Scan(&present); err != nil || !present {
		return err
	}
	var owned bool
	if err := target.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring)`, p.ID, p.Generation).Scan(&owned); err != nil || !owned {
		return err
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if err = pipelineread.Lock(ctx, tx); err != nil {
		return err
	}
	if _, _, _, err = readerAuthority(ctx, p, tx, false); err != nil {
		return err
	}
	definition, version := pipelineread.Definition, pipelineread.Version
	var installed bool
	if err = tx.QueryRow(ctx, `SELECT coalesce(bool_and(p.proowner=(SELECT nspowner FROM pg_namespace WHERE nspname='gregale_pipeline')),true)
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname='gregale_data_api' AND p.proname IN ('check_pipeline_read','check_pipeline_request') AND p.pronargs=0`).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return ErrConflict
	}
	if err = tx.QueryRow(ctx, `SELECT count(*)=2 AND coalesce(bool_and(obj_description(p.oid,'pg_proc')=$1
 AND p.prosecdef=(p.proname='check_pipeline_read') AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND encode(sha256(convert_to(p.prosrc,'UTF8')),'hex')=CASE p.proname WHEN 'check_pipeline_read' THEN $2 ELSE $3 END),false)
 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace
 WHERE n.nspname='gregale_data_api' AND p.proname IN ('check_pipeline_read','check_pipeline_request') AND p.pronargs=0`, version, pipelineread.ReaderBodySHA256, pipelineread.RequestBodySHA256).Scan(&installed); err != nil {
		return err
	}
	if !installed {
		if _, err = tx.Exec(ctx, definition); err != nil {
			return err
		}
		for _, name := range []string{"check_pipeline_read", "check_pipeline_request"} {
			if _, err = tx.Exec(ctx, "COMMENT ON FUNCTION gregale_data_api."+name+"() IS "+literal(version)); err != nil {
				return err
			}
		}
	}
	rows, err := tx.Query(ctx, `SELECT r.rolname FROM pg_roles r WHERE r.rolcanlogin AND NOT(r.rolsuper OR r.rolbypassrls OR r.rolinherit)
 AND EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='public' AND c.relname=ANY($1::text[]) AND obj_description(c.oid,'pg_class')=$2 AND has_table_privilege(r.oid,c.oid,'SELECT'))
 AND (NOT has_schema_privilege(r.oid,'gregale_data_api','USAGE')
 OR NOT has_function_privilege(r.oid,'gregale_data_api.check_pipeline_request()','EXECUTE')
 OR NOT has_function_privilege(r.oid,'gregale_data_api.check_pipeline_read()','EXECUTE'))`, p.Tables, marker(p))
	if err != nil {
		return err
	}
	var roles []string
	for rows.Next() {
		var role string
		if err = rows.Scan(&role); err != nil {
			break
		}
		roles = append(roles, role)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, role := range roles {
		if _, err = tx.Exec(ctx, "GRANT USAGE ON SCHEMA gregale_data_api TO "+ident(role)+"; GRANT EXECUTE ON FUNCTION gregale_data_api.check_pipeline_request(),gregale_data_api.check_pipeline_read() TO "+ident(role)); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}
