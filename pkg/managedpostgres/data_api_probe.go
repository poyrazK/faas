package managedpostgres

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

// VerifyDataAPICredentialProbe checks a Data API login against its SQL
// contract (ADR-650): migration creates a private public-schema table and
// an RLS-protected api table; the login cannot read the first, create
// objects, become the owner or truncate, and reads and writes only the
// rows its request.jwt.claims subject owns. Call only on isolated
// qualification databases; the caller drops public.<fixture> and
// api.<fixture>.
func VerifyDataAPICredentialProbe(ctx context.Context, conn, migration *pgx.Conn, owner, fixture string) error {
	if conn == nil || migration == nil || !validReadOnlyProbeName(fixture) || owner == "" {
		return ErrInvalid
	}
	// The login carries the Data API's statement and lock deadlines.
	var statementMS, lockMS int
	if err := conn.QueryRow(ctx, `SELECT (extract(epoch FROM current_setting('statement_timeout')::interval)*1000)::int,
 (extract(epoch FROM current_setting('lock_timeout')::interval)*1000)::int`).Scan(&statementMS, &lockMS); err != nil ||
		statementMS != api.DataAPIQueryTimeoutMS || lockMS != api.DataAPILockTimeoutMS {
		return ErrUnavailable
	}
	table := "api." + dataAPIIdentifier(fixture)
	for _, query := range []string{
		"CREATE TABLE public." + dataAPIIdentifier(fixture) + " (secret text)",
		"CREATE TABLE " + table + " (id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, subject text NOT NULL, body text NOT NULL)",
		"ALTER TABLE " + table + " ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY own_notes ON " + table + " USING(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub') WITH CHECK(subject=current_setting('request.jwt.claims',true)::jsonb->>'sub')",
	} {
		if _, err := migration.Exec(ctx, query); err != nil {
			return ErrUnavailable
		}
	}
	for _, query := range []string{
		"SELECT * FROM public." + dataAPIIdentifier(fixture),
		"CREATE TABLE api." + dataAPIIdentifier(fixture+"_forbidden") + " (id integer)",
		"CREATE TEMP TABLE " + dataAPIIdentifier(fixture+"_forbidden") + " (id integer)",
		"SET ROLE " + dataAPIIdentifier(owner),
		"TRUNCATE " + table,
	} {
		if !readOnlyProbeDenied(ctx, conn, query) {
			return ErrUnavailable
		}
	}
	for _, query := range []string{
		`SELECT set_config('request.jwt.claims','{"sub":"alice"}',false)`,
		"INSERT INTO " + table + " (subject,body) VALUES ('alice','hello')",
		"UPDATE " + table + " SET body='updated' WHERE subject='alice'",
	} {
		if _, err := conn.Exec(ctx, query); err != nil {
			return ErrUnavailable
		}
	}
	if !readOnlyProbeDenied(ctx, conn, "INSERT INTO "+table+" (subject,body) VALUES ('bob','forbidden')") {
		return ErrUnavailable
	}
	if _, err := conn.Exec(ctx, `SELECT set_config('request.jwt.claims','{"sub":"bob"}',false)`); err != nil {
		return ErrUnavailable
	}
	var count int
	if err := conn.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
		return ErrUnavailable
	}
	return nil
}

func dataAPIIdentifier(name string) string { return pgx.Identifier{name}.Sanitize() }
