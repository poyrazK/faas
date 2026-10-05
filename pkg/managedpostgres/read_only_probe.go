package managedpostgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// PrepareReadOnlyCredentialProbe creates disposable public-schema data before
// a reader is issued. Call only on isolated qualification databases. The caller
// owns cleanup. Identifiers are quoted; no provider vocabulary is involved.
func PrepareReadOnlyCredentialProbe(ctx context.Context, migration *pgx.Conn, fixture string) error {
	if migration == nil || !validReadOnlyProbeName(fixture) {
		return ErrInvalid
	}
	return createReadOnlyProbeTable(ctx, migration, fixture)
}

func validReadOnlyProbeName(name string) bool {
	if len(name) == 0 || len(name) > 48 {
		return false
	}
	for _, ch := range name {
		if (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') && ch != '_' {
			return false
		}
	}
	return true
}

func createReadOnlyProbeTable(ctx context.Context, migration *pgx.Conn, fixture string) error {
	table := pgx.Identifier{"public", fixture}.Sanitize()
	for _, query := range []string{
		"CREATE TABLE " + table + " (id serial PRIMARY KEY, tenant integer NOT NULL, value text)",
		"INSERT INTO " + table + " (tenant,value) VALUES (1,'visible'),(2,'hidden')",
		"ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY tenant_one ON " + table + " USING (tenant=1) WITH CHECK (tenant=1)",
	} {
		if _, err := migration.Exec(ctx, query); err != nil {
			return ErrUnavailable
		}
	}
	return nil
}

// VerifyReadOnlyCredentialProbe checks real SQL privileges against existing
// and future tables, including RLS, sequences, DDL and role escalation. It is
// shared by provider qualification and SQL integration tests. It intentionally
// uses a writable session: a client read-only transaction cannot prove grants.
func VerifyReadOnlyCredentialProbe(ctx context.Context, reader, migration *pgx.Conn, fixture string) error {
	if reader == nil || migration == nil || !validReadOnlyProbeName(fixture) {
		return ErrInvalid
	}
	var owner string
	if err := migration.QueryRow(ctx, "SELECT current_user").Scan(&owner); err != nil {
		return ErrUnavailable
	}
	var unsafe bool
	if err := reader.QueryRow(ctx, `SELECT current_user<>session_user OR r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls OR r.rolinherit
 OR EXISTS (SELECT 1 FROM pg_auth_members WHERE member=r.oid)
 OR has_database_privilege(current_user,current_database(),'CREATE,TEMPORARY')
 OR has_schema_privilege(current_user,'public','CREATE') FROM pg_roles r WHERE rolname=session_user`).Scan(&unsafe); err != nil || unsafe {
		return ErrUnavailable
	}
	if _, err := reader.Exec(ctx, "SET default_transaction_read_only=off; SET row_security=on"); err != nil {
		return ErrUnavailable
	}
	if err := createReadOnlyProbeTable(ctx, migration, fixture+"_future"); err != nil {
		return err
	}
	for _, name := range []string{fixture, fixture + "_future"} {
		table := pgx.Identifier{"public", name}.Sanitize()
		var count int
		if err := reader.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 1 {
			return ErrUnavailable
		}
		sequence := pgx.Identifier{"public", name + "_id_seq"}.Sanitize()
		if err := reader.QueryRow(ctx, "SELECT last_value FROM "+sequence).Scan(&count); err != nil {
			return ErrUnavailable
		}
		for _, query := range []string{
			"INSERT INTO " + table + " (tenant,value) VALUES (1,'forbidden')",
			"UPDATE " + table + " SET value='forbidden' WHERE tenant=1",
			"DELETE FROM " + table + " WHERE tenant=1",
			"TRUNCATE " + table,
			"ALTER TABLE " + table + " ADD COLUMN forbidden integer",
			"SELECT nextval('" + sequence + "')",
			"SELECT setval('" + sequence + "',100)",
		} {
			if !readOnlyProbeDenied(ctx, reader, query) {
				return ErrUnavailable
			}
		}
	}
	for _, query := range []string{
		"CREATE TABLE " + pgx.Identifier{"public", fixture + "_denied"}.Sanitize() + " (id integer)",
		"CREATE TEMP TABLE " + pgx.Identifier{fixture + "_denied"}.Sanitize() + " (id integer)",
		"CREATE SCHEMA " + pgx.Identifier{fixture + "_denied"}.Sanitize(),
		"CREATE ROLE " + pgx.Identifier{fixture + "_denied"}.Sanitize(),
		"SET ROLE " + pgx.Identifier{owner}.Sanitize(),
	} {
		if !readOnlyProbeDenied(ctx, reader, query) {
			return ErrUnavailable
		}
	}
	if _, err := reader.Exec(ctx, "SET row_security=off"); err != nil {
		return ErrUnavailable
	}
	denied := readOnlyProbeDenied(ctx, reader, "SELECT * FROM "+pgx.Identifier{"public", fixture}.Sanitize())
	if _, err := reader.Exec(ctx, "SET row_security=on"); err != nil || !denied {
		return ErrUnavailable
	}
	return nil
}

func readOnlyProbeDenied(ctx context.Context, conn *pgx.Conn, query string) bool {
	_, err := conn.Exec(ctx, query)
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "42501"
}
