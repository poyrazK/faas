// adr: 1667 — shared pause state and verified per-login contracts during rotation.
package neon

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Pinned installer bodies. Tests compare them with the embedded runtime.
func refreshRotationHashes() []string {
	return []string{
		"bd6f2299daf0e3ede75d776ba43d5d5195f3554a3f416e85f66a3fd6a53236cb", "f0b83ac5bf589f9e3c0d1c2c0bc2333932a0754f41c63038d1f3cd55fba83e94", "fad8ab2a3dd29a90d3c5d8e8a60a3e694607216edbd3416e76891db609f7a3e1", "be21d6106f30d3d7606072a2d035f31c065772f798e8ee192dda3f66a5c731d1",
	}
}

func prepareRefreshRotationCredential(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	if role.bindingID == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+roleIdentifier(role.schemaOwner)); err != nil {
		return err
	}
	if err := prepareRefreshRotationAsOwner(ctx, tx, role); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "RESET ROLE")
	return err
}

func prepareRefreshRotationAsOwner(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	exists, err := refreshRotationInstalled(ctx, tx)
	if err != nil || !exists {
		return err
	}
	if err := verifyRefreshRotationTables(ctx, tx, role.schemaOwner); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE gregale_data_api.refresh_rotation_bindings,gregale_data_api.refresh_rotation_members IN ROW EXCLUSIVE MODE`); err != nil {
		return err
	}
	if err := verifyRefreshRotationTables(ctx, tx, role.schemaOwner); err != nil {
		return err
	}
	var version int
	var deadline bool
	var sourceValid bool
	err = tx.QueryRow(ctx, `SELECT b.version,b.sql_deadline,coalesce(r.rolcanlogin AND NOT
 (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit)
 AND NOT EXISTS(SELECT 1 FROM pg_auth_members WHERE member=r.oid)
 AND shobj_description(r.oid,'pg_authid')=$3,false)
 FROM ONLY gregale_data_api.refresh_rotation_bindings b
 LEFT JOIN gregale_data_api.refresh_rotation_members m ON m.role_name=b.source_role AND m.binding_id=b.binding_id AND m.scope=b.scope
 LEFT JOIN pg_roles r ON r.rolname=m.role_name AND r.oid=m.role_oid WHERE b.binding_id=$1::uuid AND b.scope=$2`, role.bindingID, role.scope, role.marker).Scan(&version, &deadline, &sourceValid)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if version != 1 || !sourceValid {
		return managedpostgres.ErrConflict
	}
	if err := verifyRefreshRotationHooks(ctx, tx, role.schemaOwner); err != nil {
		return err
	}
	if deadline {
		if err := verifyDeadlineHooks(ctx, tx, role, false); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `SELECT gregale_data_api.prepare_refresh_member($1::uuid,$2,$3::name)`, role.bindingID, role.scope, role.name); err != nil {
		var sqlErr *pgconn.PgError
		if errors.As(err, &sqlErr) && sqlErr.Code == "55000" {
			return managedpostgres.ErrConflict
		}
		return err
	}
	if deadline {
		return verifyDeadlineHooks(ctx, tx, role, true)
	}
	return nil
}

func refreshRotationInstalled(ctx context.Context, tx pgx.Tx) (bool, error) {
	var exists bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='gregale_data_api' AND c.relname IN ('refresh_rotation_bindings','refresh_rotation_members'))`).Scan(&exists)
	return exists, err
}

func verifyRefreshRotationTables(ctx context.Context, tx pgx.Tx, owner string) error {
	shapes := [][]string{
		{"binding_id:uuid:true", "scope:text:true", "version:integer:true", "source_role:text:true", "sql_deadline:boolean:true", "paused:boolean:true", "token_hash:text:false"},
		{"role_name:text:true", "role_oid:oid:true", "binding_id:uuid:true", "scope:text:true", "fingerprint:text:true", "catalog_witness:text:true", "rpcs:oid[]:true"},
	}
	for i, name := range []string{"refresh_rotation_bindings", "refresh_rotation_members"} {
		var valid bool
		err := tx.QueryRow(ctx, `SELECT c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
 AND c.relowner=r.oid AND n.nspowner=r.oid
 AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(c.relacl,acldefault('r',c.relowner))) a WHERE a.grantee<>r.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_attribute a,LATERAL aclexplode(a.attacl) x WHERE a.attrelid=c.oid AND x.grantee<>r.oid)
 AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(n.nspacl,acldefault('n',n.nspowner))) a WHERE a.grantee=0)
 AND NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)
 AND (SELECT array_agg(a.attname::text||':'||a.atttypid::regtype::text||':'||a.attnotnull::text ORDER BY a.attnum) FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)=$3::text[]
 AND EXISTS(SELECT 1 FROM pg_constraint p WHERE p.conrelid=c.oid AND p.contype='p' AND p.convalidated
 AND p.conkey=CASE WHEN $2='refresh_rotation_bindings' THEN ARRAY[1,2]::smallint[] ELSE ARRAY[1]::smallint[] END)
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.rolname=$1 WHERE n.nspname='gregale_data_api' AND c.relname=$2`, owner, name, shapes[i]).Scan(&valid)
		if errors.Is(err, pgx.ErrNoRows) {
			return managedpostgres.ErrConflict
		}
		if err != nil {
			return err
		}
		if !valid {
			return managedpostgres.ErrConflict
		}
	}
	return nil
}

func verifyRefreshRotationHooks(ctx context.Context, tx pgx.Tx, owner string) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT count(*)=4 AND coalesce(bool_and(coalesce(
 encode(sha256(convert_to(p.prosrc,'UTF8')),'hex')=($2::text[])[array_position(ARRAY['check_request','refresh_status','refresh_catalog_witness','prepare_refresh_member'],p.proname::text)]
 AND p.prokind='f' AND p.provolatile='v' AND p.proparallel='u' AND NOT p.proisstrict AND NOT p.proleakproof AND p.pronargdefaults=0 AND p.provariadic=0
 AND p.proretset=(p.proname='refresh_status')
 AND p.prorettype=CASE p.proname WHEN 'refresh_status' THEN 'record'::regtype WHEN 'refresh_catalog_witness' THEN 'text'::regtype ELSE 'void'::regtype END
 AND p.oid IN(to_regprocedure('gregale_data_api.check_request()'),to_regprocedure('gregale_data_api.refresh_status(text)'),to_regprocedure('gregale_data_api.refresh_catalog_witness(name)'),to_regprocedure('gregale_data_api.prepare_refresh_member(uuid,text,name)'))
 AND p.proconfig=ARRAY['search_path=pg_catalog'] AND p.proowner=r.oid AND n.nspowner=r.oid
 AND p.prosecdef=(p.proname IN ('check_request','refresh_status'))
 AND l.lanname=CASE WHEN p.proname IN ('check_request','prepare_refresh_member') THEN 'plpgsql' ELSE 'sql' END
 AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')
 AND (p.proname IN ('check_request','refresh_status') OR NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee<>r.oid AND a.privilege_type='EXECUTE'))
 ,false)),false) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang JOIN pg_roles r ON r.rolname=$1
 WHERE n.nspname='gregale_data_api' AND p.proname IN ('check_request','refresh_status','refresh_catalog_witness','prepare_refresh_member')`, owner, refreshRotationHashes()).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return managedpostgres.ErrConflict
	}
	return nil
}

// Revoke already disabled login and terminated sessions. Remove only this
// generation's mapping, retaining the shared gate until the last member retires.
func retireRefreshRotationMember(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	if role.access != managedpostgres.CredentialDataAPI || role.bindingID == "" || role.marker == "" {
		return nil
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+roleIdentifier(role.schemaOwner)); err != nil {
		return err
	}
	exists, err := refreshRotationInstalled(ctx, tx)
	if err != nil {
		return err
	}
	if exists {
		if err := verifyRefreshRotationTables(ctx, tx, role.schemaOwner); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `LOCK TABLE gregale_data_api.refresh_rotation_bindings,gregale_data_api.refresh_rotation_members IN ROW EXCLUSIVE MODE`); err != nil {
			return err
		}
		if err := verifyRefreshRotationTables(ctx, tx, role.schemaOwner); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended('gregale-data-api-binding:'||$2||':'||$1,0))`, role.bindingID, role.scope); err != nil {
			return err
		}
		var conflicting bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM gregale_data_api.refresh_rotation_members WHERE role_name=$1
 AND (role_oid<>to_regrole($1)::oid OR binding_id<>$2::uuid OR scope<>$3))`, role.name, role.bindingID, role.scope).Scan(&conflicting); err != nil {
			return err
		}
		if conflicting {
			return managedpostgres.ErrConflict
		}
		if _, err := tx.Exec(ctx, `DELETE FROM ONLY gregale_data_api.refresh_rotation_members WHERE role_name=$1 AND role_oid=to_regrole($1)::oid AND binding_id=$2::uuid AND scope=$3`, role.name, role.bindingID, role.scope); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `UPDATE gregale_data_api.refresh_rotation_bindings b SET source_role=coalesce((SELECT m.role_name
 FROM gregale_data_api.refresh_rotation_members m JOIN pg_roles r ON r.rolname=m.role_name AND r.oid=m.role_oid
 WHERE m.binding_id=b.binding_id AND m.scope=b.scope AND r.rolcanlogin ORDER BY m.role_name LIMIT 1),b.source_role)
 WHERE b.binding_id=$1::uuid AND b.scope=$2 AND b.source_role=$3`, role.bindingID, role.scope, role.name); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM ONLY gregale_data_api.refresh_rotation_bindings b WHERE binding_id=$1::uuid AND scope=$2 AND NOT EXISTS(SELECT 1 FROM gregale_data_api.refresh_rotation_members m WHERE m.binding_id=b.binding_id AND m.scope=b.scope)`, role.bindingID, role.scope); err != nil {
			return err
		}
	}
	_, err = tx.Exec(ctx, "RESET ROLE")
	return err
}
