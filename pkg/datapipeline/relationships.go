package datapipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

func relationshipBody(p Pipeline, r api.DataPipelineRelationship, relations []relation) (string, error) {
	source := slices.IndexFunc(relations, func(t relation) bool { return t.Name == r.SourceTable })
	target := slices.IndexFunc(relations, func(t relation) bool { return t.Name == r.TargetTable })
	if source < 0 || target < 0 {
		return "", ErrConflict
	}
	keys := primaryColumns(relations[target])
	if len(keys) != len(r.TargetColumns) {
		return "", ErrInvalid
	}
	joins, filters := []string{}, []string{}
	for i, col := range r.SourceColumns {
		si := slices.IndexFunc(relations[source].Columns, func(c column) bool { return c.Name == col })
		ti := slices.IndexFunc(keys, func(c column) bool { return c.Name == r.TargetColumns[i] })
		if si < 0 || ti < 0 || relations[source].Columns[si].Type != keys[ti].Type {
			return "", ErrInvalid
		}
		joins = append(joins, "t."+ident(r.TargetColumns[i])+"=s."+ident(col))
	}
	// Re-read the authoritative source row under RLS. A direct RPC caller can
	// forge a composite argument, so its non-key attributes are never trusted.
	for _, key := range primaryColumns(relations[source]) {
		filters = append(filters, "s."+ident(key.Name)+"=(source_row)."+ident(key.Name))
	}
	if len(filters) == 0 {
		return "", ErrInvalid
	}
	if a := tenantAccess(p); a != nil {
		joins = append(joins, "t."+ident(a.Column)+`::text COLLATE "C"=s.`+ident(a.Column)+`::text COLLATE "C"`)
	}
	return "SELECT t.* FROM " + tableIdent(r.SourceTable) + " s JOIN " + tableIdent(r.TargetTable) + " t ON " + strings.Join(joins, " AND ") + " WHERE " + strings.Join(filters, " AND "), nil
}

func ensureRelationshipReceipt(ctx context.Context, tx receiptSQL) error {
	_, err := tx.Exec(ctx, `CREATE TABLE IF NOT EXISTS gregale_pipeline.relationships(
 pipeline_id uuid NOT NULL REFERENCES gregale_pipeline.mirrors(pipeline_id) ON DELETE CASCADE,
 generation bigint NOT NULL,name text NOT NULL,source_table text NOT NULL,target_table text NOT NULL,
 function_oid oid NOT NULL,body_hash text NOT NULL,PRIMARY KEY(pipeline_id,generation,name))`)
	return err
}

func dropRelationships(ctx context.Context, p Pipeline, tx pgx.Tx, tables []string) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('gregale_pipeline.relationships') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return err
	}
	rows, err := tx.Query(ctx, `SELECT r.name,p.oid::regprocedure::text,
 p.proowner=(SELECT c.relowner FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname=r.source_table)
 AND encode(sha256(convert_to(p.prosrc,'UTF8')),'hex')=r.body_hash AND NOT p.prosecdef
 FROM gregale_pipeline.relationships r LEFT JOIN pg_proc p ON p.oid=r.function_oid
 WHERE r.pipeline_id=$1 AND r.generation=$2 AND (cardinality($3::text[])=0 OR r.source_table=ANY($3) OR r.target_table=ANY($3)) ORDER BY r.name`, p.ID, p.Generation, tables)
	if err != nil {
		return err
	}
	type entry struct {
		name      string
		signature *string
		owned     *bool
	}
	var entries []entry
	for rows.Next() {
		var e entry
		if err = rows.Scan(&e.name, &e.signature, &e.owned); err != nil {
			break
		}
		entries = append(entries, e)
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.signature != nil {
			if e.owned == nil || !*e.owned {
				return ErrConflict
			}
			if _, err = tx.Exec(ctx, "DROP FUNCTION "+*e.signature); err != nil {
				return err
			}
		}
		if _, err = tx.Exec(ctx, `DELETE FROM gregale_pipeline.relationships WHERE pipeline_id=$1 AND generation=$2 AND name=$3`, p.ID, p.Generation, e.name); err != nil {
			return err
		}
	}
	return nil
}

func reconcileRelationships(ctx context.Context, p Pipeline, target *pgx.Conn, owner string, current func(context.Context) error) error {
	if p.Serving == nil {
		return nil
	}
	tx, err := target.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err = verificationSetting(ctx, tx); err != nil {
		return err
	}
	if err = ensureRelationshipReceipt(ctx, tx); err != nil {
		return err
	}
	var spec []byte
	if err = tx.QueryRow(ctx, `SELECT schema_spec FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2 AND NOT retiring FOR UPDATE`, p.ID, p.Generation).Scan(&spec); err != nil {
		return err
	}
	var relations []relation
	if json.Unmarshal(spec, &relations) != nil {
		return ErrConflict
	}
	// Drop removed declarations and replace only receipt-owned functions. This
	// transaction also serializes dependency checks with quarantine/retirement.
	if p.Evolution != nil && p.Evolution.State == "approved" && p.Evolution.Kind == "relationships" {
		var applied string
		if err = tx.QueryRow(ctx, `SELECT relationship_change FROM gregale_pipeline.mirrors WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation).Scan(&applied); err != nil {
			return err
		}
		if applied != p.Evolution.ID {
			if err = dropRelationships(ctx, p, tx, []string{}); err != nil {
				return err
			}
		}
	}
	for _, r := range p.Serving.Relationships {
		body, bodyErr := relationshipBody(p, r, relations)
		if bodyErr != nil {
			return bodyErr
		}
		sum := sha256.Sum256([]byte(body))
		hash := hex.EncodeToString(sum[:])
		var existing, owned bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_proc f JOIN pg_namespace n ON n.oid=f.pronamespace WHERE n.nspname='public' AND f.proname=$1),
 EXISTS(SELECT 1 FROM gregale_pipeline.relationships r JOIN pg_proc f ON f.oid=r.function_oid WHERE r.pipeline_id=$2 AND r.generation=$3 AND r.name=$1 AND r.body_hash=$4
 AND encode(sha256(convert_to(f.prosrc,'UTF8')),'hex')=$4 AND f.proowner=(SELECT oid FROM pg_roles WHERE rolname=$5)
 AND NOT(f.prosecdef OR f.proisstrict OR f.proleakproof) AND f.prokind='f' AND f.provolatile='s' AND f.proretset AND f.prorows=1
 AND f.prolang=(SELECT oid FROM pg_language WHERE lanname='sql') AND f.proconfig=ARRAY['search_path=pg_catalog']
 AND f.pronargs=1 AND f.pronargdefaults=0 AND f.provariadic=0 AND f.proargmodes IS NULL AND f.proargnames=ARRAY['source_row']
 AND f.proargtypes[0]=(SELECT reltype FROM pg_class WHERE oid=to_regclass($6))
 AND f.prorettype=(SELECT reltype FROM pg_class WHERE oid=to_regclass($7))
 AND (SELECT count(*) FROM pg_proc other WHERE other.pronamespace=f.pronamespace AND other.proname=f.proname)=1
 AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(f.proacl,acldefault('f',f.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')
 AND obj_description(f.oid,'pg_proc')='@gregale:rpc')`, r.Name, p.ID, p.Generation, hash, owner, tableIdent(r.SourceTable), tableIdent(r.TargetTable)).Scan(&existing, &owned); err != nil {
			return err
		}
		if existing && !owned {
			return ErrConflict
		}
		signature := tableIdent(r.Name) + "(" + tableIdent(r.SourceTable) + ")"
		if !existing {
			if _, err = tx.Exec(ctx, "SET LOCAL ROLE "+ident(owner)); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "CREATE FUNCTION "+tableIdent(r.Name)+"(source_row "+tableIdent(r.SourceTable)+") RETURNS SETOF "+tableIdent(r.TargetTable)+" LANGUAGE sql STABLE SECURITY INVOKER ROWS 1 SET search_path=pg_catalog AS "+literal(body)); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, "REVOKE ALL ON FUNCTION "+signature+" FROM PUBLIC; COMMENT ON FUNCTION "+signature+" IS '@gregale:rpc'; SET LOCAL ROLE NONE"); err != nil {
				return err
			}
			if _, err = tx.Exec(ctx, `INSERT INTO gregale_pipeline.relationships(pipeline_id,generation,name,source_table,target_table,function_oid,body_hash) VALUES($1,$2,$3,$4,$5,to_regprocedure($6)::oid,$7)`, p.ID, p.Generation, r.Name, r.SourceTable, r.TargetTable, signature, hash); err != nil {
				return err
			}
		}
		roles, err := relationshipRoles(ctx, tx, r)
		if err != nil {
			return err
		}
		for _, role := range roles {
			if _, err = tx.Exec(ctx, "GRANT EXECUTE ON FUNCTION "+signature+" TO "+ident(role)); err != nil {
				return err
			}
		}
	}
	encoded, _ := json.Marshal(p.Serving.Relationships)
	if len(p.Serving.Relationships) == 0 {
		encoded = []byte("[]")
	}
	if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET relationships=$3::jsonb WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, encoded); err != nil {
		return err
	}
	if p.Evolution != nil && p.Evolution.State == "approved" && p.Evolution.Kind == "relationships" {
		if _, err = tx.Exec(ctx, `UPDATE gregale_pipeline.mirrors SET relationship_change=$3 WHERE pipeline_id=$1 AND generation=$2`, p.ID, p.Generation, p.Evolution.ID); err != nil {
			return err
		}
	}
	if err = current(ctx); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func relationshipRoles(ctx context.Context, tx pgx.Tx, r api.DataPipelineRelationship) ([]string, error) {
	rows, err := tx.Query(ctx, `SELECT rolname FROM pg_roles WHERE rolcanlogin AND NOT(rolsuper OR rolbypassrls OR rolinherit)
 AND has_table_privilege(oid,$1,'SELECT') AND has_table_privilege(oid,$2,'SELECT') ORDER BY rolname`, tableIdent(r.SourceTable), tableIdent(r.TargetTable))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var roles []string
	for rows.Next() {
		var role string
		if err = rows.Scan(&role); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	return roles, rows.Err()
}
