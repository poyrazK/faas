// Package credentialprobe verifies delivered SQL credentials without control-plane dependencies.
package credentialprobe

import (
	"context"
	"crypto/tls"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialprobe/sqlc"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

// Access identifies the intended SQL privileges of the delivered credential.
type Access string

const (
	ReadWrite Access = "read_write"
	ReadOnly  Access = "read_only"
	Migration Access = "migration"
)

// ConnectCredentialSQL opens a private TLS connection without inheriting host
// PGOPTIONS or accepting additional URI parameters. Callers must close it and
// keep both the connection string and driver errors out of reports and logs.
func ConnectCredentialSQL(ctx context.Context, dsn string) (*pgx.Conn, error) {
	config, err := CredentialSQLConfig(dsn)
	if err != nil {
		return nil, pgerrors.ErrInvalid
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return nil, pgerrors.ErrUnavailable
	}
	return conn, nil
}

// VerifyCredentialSQL authenticates without writing customer data. ACL evidence
// cannot establish data correctness or connectivity from an application VM.
func VerifyCredentialSQL(ctx context.Context, dsn string, access Access, major int) error {
	config, err := CredentialSQLConfig(dsn)
	if err != nil || major < 1 || (access != ReadWrite && access != ReadOnly && access != Migration) {
		return pgerrors.ErrInvalid
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return pgerrors.ErrUnavailable
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = conn.Close(cleanup)
	}()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return pgerrors.ErrUnavailable
	}
	// Closing the private connection also rolls back this read-only transaction.
	row, err := sqlc.New().ProbeManagedPostgresCredential(ctx, tx, string(access))
	if err != nil {
		return pgerrors.ErrUnavailable
	}
	if row.Login != config.User || row.DatabaseName != config.Database || int(row.VersionNum)/10000 != major || !row.ReadOnly || !row.RowSecurity || !row.SchemaUsage || !row.DataAccess.Valid || !row.DataAccess.Bool || !row.UnsafeRole.Valid || row.UnsafeRole.Bool || !row.ElevatedRuntime.Valid || row.ElevatedRuntime.Bool {
		return pgerrors.ErrConflict
	}
	if access == Migration {
		if !row.SchemaCreate {
			return pgerrors.ErrConflict
		}
	} else if row.EffectiveUser != row.Login || row.SchemaCreate {
		return pgerrors.ErrConflict
	}
	return nil
}

// CredentialSQLConfig returns a TLS-only driver configuration suitable for
// private connections or application pools. It excludes host PGOPTIONS and
// unsafe URI parameters. Callers must not log the configuration or its DSN.
func CredentialSQLConfig(dsn string) (*pgx.ConnConfig, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.User.Username() == "" || u.Hostname() == "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/") || len(u.Path) < 2 {
		return nil, pgerrors.ErrInvalid
	}
	if password, ok := u.User.Password(); !ok || password == "" {
		return nil, pgerrors.ErrInvalid
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 || len(query["sslmode"]) != 1 {
		return nil, pgerrors.ErrInvalid
	}
	switch query.Get("sslmode") {
	case "require", "verify-ca", "verify-full":
	default:
		return nil, pgerrors.ErrInvalid
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || config.TLSConfig == nil || strings.ContainsAny(config.Host, "/\\\x00") {
		return nil, pgerrors.ErrInvalid
	}
	config.TLSConfig.MinVersion = tls.VersionTLS12
	for _, fallback := range config.Fallbacks {
		if fallback.TLSConfig == nil {
			return nil, pgerrors.ErrInvalid
		}
		fallback.TLSConfig.MinVersion = tls.VersionTLS12
	}
	config.RuntimeParams = map[string]string{"client_encoding": "UTF8", "standard_conforming_strings": "on"} // Exclude host PGOPTIONS.
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return config, nil
}
