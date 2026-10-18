// adr: 1666 — prepare owner-approved deadline grants before credential publication.
package neon

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Version one pins the installed runtime bodies, not owner-supplied executable
// code. A parity test ties these hashes to the embedded installer and API limits.
const deadlineEnforceSHA256 = "df9cf6cd8695ff23e76c109edee0e84fd480088ddfc754aa829bc7ef27148efe"
const deadlineRequestSHA256 = "09c6dcac7ae942f34bc56ea836acf38d2b08ffb62eeade90a888f979c6cbfd14"

func prepareSQLDeadlineCredential(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	if role.bindingID == "" {
		return nil
	}
	// Even resolving an object by its qualified name requires schema USAGE.
	// Use explicit owner authority before lookup, not inherited provider grants.
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+roleIdentifier(role.schemaOwner)); err != nil {
		return err
	}
	if err := prepareSQLDeadlineAsOwner(ctx, tx, role); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, "RESET ROLE")
	return err
}

func prepareSQLDeadlineAsOwner(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('gregale_data_api.deadline_binding_policies') IS NOT NULL`).Scan(&exists); err != nil || !exists {
		return err
	}
	// The provider already holds the schema-owner advisory lock. The owner CLI
	// takes the same lock. Reject hidden rows, views and foreign ACLs before reads.
	if err := verifyDeadlinePolicyTable(ctx, tx, role.schemaOwner); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE gregale_data_api.deadline_binding_policies IN SHARE MODE`); err != nil {
		return err
	}
	if err := verifyDeadlinePolicyTable(ctx, tx, role.schemaOwner); err != nil {
		return err
	}
	var version int
	var enforce, request string
	err := tx.QueryRow(ctx, `SELECT version,enforce_sha256,request_sha256 FROM ONLY gregale_data_api.deadline_binding_policies
 WHERE binding_id=$1::uuid AND scope=$2`, role.bindingID, role.scope).Scan(&version, &enforce, &request)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if version != 1 || enforce != deadlineEnforceSHA256 || request != deadlineRequestSHA256 {
		return managedpostgres.ErrConflict
	}
	var refresh bool
	if err := tx.QueryRow(ctx, `SELECT to_regclass('gregale_data_api.refresh_gate') IS NOT NULL`).Scan(&refresh); err != nil {
		return err
	}
	if refresh {
		return managedpostgres.ErrConflict
	}
	if err := verifyDeadlineHooks(ctx, tx, role, false); err != nil {
		return err
	}
	for _, query := range []string{
		"GRANT USAGE ON SCHEMA gregale_data_api TO " + roleIdentifier(role.name),
		"GRANT EXECUTE ON FUNCTION gregale_data_api.enforce_deadline(),gregale_data_api.check_deadline_request() TO " + roleIdentifier(role.name),
	} {
		if _, err := tx.Exec(ctx, query); err != nil {
			return err
		}
	}
	return verifyDeadlineHooks(ctx, tx, role, true)
}

func verifyDeadlinePolicyTable(ctx context.Context, tx pgx.Tx, owner string) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT c.relkind='r' AND c.relpersistence='p' AND NOT c.relrowsecurity AND NOT c.relforcerowsecurity
 AND c.relowner=r.oid AND n.nspowner=r.oid
 AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(c.relacl,acldefault('r',c.relowner))) a WHERE a.grantee<>r.oid)
 AND NOT EXISTS(SELECT 1 FROM pg_attribute a, LATERAL aclexplode(a.attacl) x WHERE a.attrelid=c.oid AND x.grantee<>r.oid)
 AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(n.nspacl,acldefault('n',n.nspowner))) a WHERE a.grantee=0)
 AND NOT EXISTS(SELECT 1 FROM pg_inherits WHERE inhrelid=c.oid OR inhparent=c.oid)
 AND (SELECT array_agg(a.attname::text||':'||a.atttypid::regtype::text||':'||a.attnotnull::text ORDER BY a.attnum)
 FROM pg_attribute a WHERE a.attrelid=c.oid AND a.attnum>0 AND NOT a.attisdropped)
 =ARRAY['binding_id:uuid:true','scope:text:true','version:integer:true','enforce_sha256:text:true','request_sha256:text:true']
 AND EXISTS(SELECT 1 FROM pg_constraint p WHERE p.conrelid=c.oid AND p.contype='p' AND p.convalidated AND p.conkey=ARRAY[1,2]::smallint[])
 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace JOIN pg_roles r ON r.rolname=$1
 WHERE c.oid=to_regclass('gregale_data_api.deadline_binding_policies')`, owner).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return managedpostgres.ErrConflict
	}
	return nil
}

func verifyDeadlineHooks(ctx context.Context, tx pgx.Tx, role credentialRole, grants bool) error {
	var valid bool
	err := tx.QueryRow(ctx, `SELECT count(*)=2 AND coalesce(bool_and(coalesce(
 encode(sha256(convert_to(p.prosrc,'UTF8')),'hex')=CASE p.proname WHEN 'enforce_deadline' THEN $1 ELSE $2 END
 AND l.lanname='plpgsql' AND p.prokind='f' AND p.pronargs=0 AND p.prorettype='void'::regtype
 AND NOT p.prosecdef AND NOT p.proretset AND NOT p.proisstrict AND NOT p.proleakproof
 AND p.provolatile='v' AND p.proparallel='u' AND p.proconfig=ARRAY['search_path=pg_catalog']
 AND p.proowner=owner.oid AND n.nspowner=owner.oid
 AND NOT pg_has_role($3::name,owner.oid,'MEMBER') AND NOT has_schema_privilege($3::name,n.oid,'CREATE')
 AND (NOT $5 OR (has_schema_privilege($3::name,n.oid,'USAGE') AND has_function_privilege($3::name,p.oid,'EXECUTE')))
 AND NOT EXISTS(SELECT 1 FROM aclexplode(coalesce(p.proacl,acldefault('f',p.proowner))) a WHERE a.grantee=0 AND a.privilege_type='EXECUTE')
 ,false)),false) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang
 JOIN pg_roles owner ON owner.rolname=$4
 WHERE p.oid IN (to_regprocedure('gregale_data_api.enforce_deadline()'),to_regprocedure('gregale_data_api.check_deadline_request()'))`,
		deadlineEnforceSHA256, deadlineRequestSHA256, role.name, role.schemaOwner, grants).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return managedpostgres.ErrConflict
	}
	return nil
}
