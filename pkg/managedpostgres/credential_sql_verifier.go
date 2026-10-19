package managedpostgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialprobe"
)

// ConnectCredentialSQL opens a private TLS connection. Callers must close it
// and keep connection strings and driver errors out of reports and logs.
func ConnectCredentialSQL(ctx context.Context, dsn string) (*pgx.Conn, error) {
	return credentialprobe.ConnectCredentialSQL(ctx, dsn)
}

// VerifyCredentialSQL authenticates without writing customer data. ACL evidence
// cannot establish data correctness or connectivity from an application VM.
func VerifyCredentialSQL(ctx context.Context, dsn string, access CredentialAccess, major int) error {
	return credentialprobe.VerifyCredentialSQL(ctx, dsn, credentialprobe.Access(access), major)
}

func credentialProbeConfig(dsn string) (*pgx.ConnConfig, error) { return CredentialSQLConfig(dsn) }

// CredentialSQLConfig returns a TLS-only driver configuration without host
// PGOPTIONS or unsafe URI parameters. Callers must not log it or its DSN.
func CredentialSQLConfig(dsn string) (*pgx.ConnConfig, error) {
	return credentialprobe.CredentialSQLConfig(dsn)
}
