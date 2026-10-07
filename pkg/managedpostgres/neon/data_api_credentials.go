package neon

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Data APIs use one restricted SQL identity. The runtime verifies the owner's
// IdP token and signs an internal token with this role, preserving sub for RLS.
// No external JWT can select a schema owner or require owner membership.
func ensureDataAPICredential(ctx context.Context, tx pgx.Tx, role credentialRole) error {
	owner, name := roleIdentifier(role.schemaOwner), roleIdentifier(role.name)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_namespace WHERE nspname='api')`).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		if _, err := tx.Exec(ctx, "CREATE SCHEMA api AUTHORIZATION "+owner); err != nil {
			return err
		}
	}
	var owned bool
	if err := tx.QueryRow(ctx, `SELECT n.nspowner=r.oid FROM pg_namespace n JOIN pg_roles r ON r.rolname=$1 WHERE n.nspname='api'`, role.schemaOwner).Scan(&owned); err != nil {
		return err
	}
	if !owned {
		return managedpostgres.ErrConflict
	}
	queries := []string{
		"REVOKE ALL ON SCHEMA api FROM PUBLIC",
		"GRANT CONNECT ON DATABASE " + roleIdentifier(role.database) + " TO " + name,
		"GRANT USAGE ON SCHEMA api TO " + name,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA api TO " + name,
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA api TO " + name,
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + owner + " IN SCHEMA api GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO " + name,
		"ALTER DEFAULT PRIVILEGES FOR ROLE " + owner + " IN SCHEMA api GRANT USAGE, SELECT ON SEQUENCES TO " + name,
	}
	for _, query := range queries {
		if _, err := tx.Exec(ctx, query); err != nil {
			return err
		}
	}
	var missing bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace
 WHERE n.nspname='api' AND ((c.relkind IN ('r','p','v','m','f') AND
 (NOT has_table_privilege($1,c.oid,'SELECT') OR NOT has_table_privilege($1,c.oid,'INSERT') OR
 NOT has_table_privilege($1,c.oid,'UPDATE') OR NOT has_table_privilege($1,c.oid,'DELETE')))
 OR (c.relkind='S' AND (NOT has_sequence_privilege($1,c.oid,'SELECT') OR NOT has_sequence_privilege($1,c.oid,'USAGE')))))`, role.name).Scan(&missing); err != nil {
		return err
	}
	if missing {
		return managedpostgres.ErrConflict
	}
	return verifyDataAPICredential(ctx, tx, role.name)
}

func verifyDataAPICredential(ctx context.Context, tx pgx.Tx, name string) error {
	var unsafe bool
	err := tx.QueryRow(ctx, `SELECT
 has_database_privilege($1,current_database(),'CREATE,TEMPORARY')
 OR EXISTS(SELECT 1 FROM pg_namespace n WHERE n.nspname !~ '^pg_' AND n.nspname<>'information_schema' AND has_schema_privilege($1,n.oid,'CREATE'))
 OR EXISTS(SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname NOT IN ('api','information_schema') AND n.nspname !~ '^pg_' AND c.relkind IN ('r','p','v','m','f') AND has_table_privilege($1,c.oid,'SELECT,INSERT,UPDATE,DELETE'))
 OR EXISTS(SELECT 1 FROM pg_shdepend WHERE refclassid='pg_authid'::regclass AND refobjid=(SELECT oid FROM pg_roles WHERE rolname=$1) AND deptype='o')
 OR EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname IN ('api','public') AND p.prosecdef AND has_function_privilege($1,p.oid,'EXECUTE'))`, name).Scan(&unsafe)
	if err != nil {
		return err
	}
	if unsafe {
		return managedpostgres.ErrConflict
	}
	return nil
}
