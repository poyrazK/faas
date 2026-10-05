package managedpostgres

import (
	"context"
	"crypto/tls"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// VerifyCredentialSQL authenticates without writing customer data. ACL evidence
// cannot establish data correctness or connectivity from an application VM.
func VerifyCredentialSQL(ctx context.Context, dsn string, access CredentialAccess, major int) error {
	config, err := credentialProbeConfig(dsn)
	if err != nil || major < 1 || (access != CredentialReadWrite && access != CredentialReadOnly && access != CredentialMigration) {
		return ErrInvalid
	}
	conn, err := pgx.ConnectConfig(ctx, config)
	if err != nil {
		return ErrUnavailable
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		defer cancel()
		_ = conn.Close(cleanup)
	}()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return ErrUnavailable
	}
	// Closing the private connection also rolls back this read-only transaction.
	row, err := sqlc.New().ProbeManagedPostgresCredential(ctx, tx, string(access))
	if err != nil {
		return ErrUnavailable
	}
	if row.Login != config.User || row.DatabaseName != config.Database || int(row.VersionNum)/10000 != major || !row.ReadOnly || !row.RowSecurity || !row.SchemaUsage || !row.DataAccess.Valid || !row.DataAccess.Bool || !row.UnsafeRole.Valid || row.UnsafeRole.Bool || !row.ElevatedRuntime.Valid || row.ElevatedRuntime.Bool {
		return ErrConflict
	}
	if access == CredentialMigration {
		if !row.SchemaCreate {
			return ErrConflict
		}
	} else if row.EffectiveUser != row.Login || row.SchemaCreate {
		return ErrConflict
	}
	return nil
}

func credentialProbeConfig(dsn string) (*pgx.ConnConfig, error) {
	u, err := url.Parse(dsn)
	if err != nil || (u.Scheme != "postgres" && u.Scheme != "postgresql") || u.User == nil || u.User.Username() == "" || u.Hostname() == "" || u.Fragment != "" || !strings.HasPrefix(u.Path, "/") || len(u.Path) < 2 {
		return nil, ErrInvalid
	}
	if password, ok := u.User.Password(); !ok || password == "" {
		return nil, ErrInvalid
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil || len(query) != 1 || len(query["sslmode"]) != 1 {
		return nil, ErrInvalid
	}
	switch query.Get("sslmode") {
	case "require", "verify-ca", "verify-full":
	default:
		return nil, ErrInvalid
	}
	config, err := pgx.ParseConfig(dsn)
	if err != nil || config.TLSConfig == nil || strings.ContainsAny(config.Host, "/\\\x00") {
		return nil, ErrInvalid
	}
	config.TLSConfig.MinVersion = tls.VersionTLS12
	for _, fallback := range config.Fallbacks {
		if fallback.TLSConfig == nil {
			return nil, ErrInvalid
		}
		fallback.TLSConfig.MinVersion = tls.VersionTLS12
	}
	config.RuntimeParams = map[string]string{"client_encoding": "UTF8", "standard_conforming_strings": "on"} // Exclude host PGOPTIONS.
	config.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	return config, nil
}
