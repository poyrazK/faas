// adr: 391 — SQL probing is TLS-only, read-only, and fails closed on identity/ACL drift.
package managedpostgres

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestCredentialProbeConfigRejectsUnsafeConnectionOptions(t *testing.T) {
	for _, dsn := range []string{
		"postgresql://role:secret@db.example/customer?sslmode=disable",
		"postgresql://role:secret@db.example/customer?sslmode=prefer",
		"postgresql://role:secret@db.example/customer?sslmode=require&options=-c%20role%3Dadmin",
		"postgresql://role:secret@db.example/customer?sslmode=require&sslmode=disable",
		"postgresql://role@db.example/customer?sslmode=require",
		"postgresql://role:secret@db.example/customer?sslmode=require#fragment",
		"postgresql://role:secret@db.example/customer?sslmode=require&host=/tmp",
		"host=db.example user=role password=secret sslmode=require",
	} {
		if _, err := credentialProbeConfig(dsn); err == nil {
			t.Fatal("accepted unsafe DSN")
		}
	}
	t.Setenv("PGOPTIONS", "-c role=admin")
	config, err := credentialProbeConfig("postgresql://role:secret@db.example/customer?sslmode=require")
	if err != nil || config.TLSConfig == nil || config.RuntimeParams["options"] != "" || config.RuntimeParams["client_encoding"] != "UTF8" {
		t.Fatal("safe config", err)
	}
}
func TestCredentialSQLVerificationNativeTLS(t *testing.T) {
	admin := pgtest.Open(t)
	ctx := context.Background()
	cfg := admin.Config().ConnConfig
	var ssl string
	if err := admin.QueryRow(ctx, "SHOW ssl").Scan(&ssl); err != nil {
		t.Fatal(err)
	}
	if ssl != "on" {
		t.Skip("native SQL credential verification needs PostgreSQL TLS enabled")
	}
	database := "probe_database_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, err := admin.Exec(ctx, `CREATE DATABASE "`+database+`"`); err != nil {
		t.Fatal(err)
	}
	privateCfg := admin.Config().Copy()
	privateCfg.ConnConfig.Database = database
	delete(privateCfg.ConnConfig.RuntimeParams, "search_path")
	pool, err := pgxpool.NewWithConfig(ctx, privateCfg)
	if err != nil {
		t.Fatal(err)
	}
	cfg = privateCfg.ConnConfig
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), `DROP DATABASE "`+database+`" WITH (FORCE)`)
	})
	if _, err := pool.Exec(ctx, "CREATE TABLE public.probe_existing(id bigint GENERATED ALWAYS AS IDENTITY)"); err != nil {
		t.Fatal(err)
	}
	role := "cutover_probe_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	quoted := "\"" + role + "\""
	setup := []string{
		"CREATE ROLE " + quoted + " LOGIN PASSWORD 'probe-secret' NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS",
		"REVOKE CREATE ON SCHEMA public FROM PUBLIC",
		fmt.Sprintf("REVOKE TEMPORARY ON DATABASE %q FROM PUBLIC", cfg.Database),
		"GRANT USAGE ON SCHEMA public TO " + quoted,
		"GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO " + quoted,
		"GRANT SELECT,USAGE ON ALL SEQUENCES IN SCHEMA public TO " + quoted,
	}
	for _, sql := range setup {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DROP OWNED BY "+quoted)
		_, _ = pool.Exec(context.Background(), "DROP ROLE "+quoted)
	})
	dsn := url.URL{Scheme: "postgresql", User: url.UserPassword(role, "probe-secret"), Host: fmt.Sprintf("127.0.0.1:%d", cfg.Port), Path: "/" + cfg.Database, RawQuery: "sslmode=require"}
	var version int
	if err := pool.QueryRow(ctx, "SELECT current_setting('server_version_num')::integer/10000").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialReadWrite, version); err != nil {
		debugCfg, configErr := credentialProbeConfig(dsn.String())
		if configErr != nil {
			t.Fatal(configErr)
		}
		debugConn, connectErr := pgx.ConnectConfig(ctx, debugCfg)
		if connectErr != nil {
			t.Fatalf("native probe connection failed: %v", connectErr)
		}
		defer debugConn.Close(ctx)
		debugTx, txErr := debugConn.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
		if txErr != nil {
			t.Fatal(txErr)
		}
		row, queryErr := sqlc.New().ProbeManagedPostgresCredential(ctx, debugTx, string(CredentialReadWrite))
		t.Fatalf("runtime SQL verification: %v; metadata=%+v; query=%v", err, row, queryErr)
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialReadWrite, version+1); err == nil {
		t.Fatal("wrong version accepted")
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialMigration, version); err == nil {
		t.Fatal("migration without CREATE accepted")
	}
	if _, err := pool.Exec(ctx, "GRANT CREATE ON SCHEMA public TO "+quoted); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialMigration, version); err != nil {
		t.Fatal("migration SQL verification", err)
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialReadWrite, version); err == nil {
		t.Fatal("runtime with schema CREATE accepted")
	}
	if _, err := pool.Exec(ctx, "REVOKE CREATE ON SCHEMA public FROM "+quoted); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "GRANT TRUNCATE ON public.probe_existing TO "+quoted); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialReadWrite, version); err == nil {
		t.Fatal("runtime TRUNCATE accepted")
	}
	if _, err := pool.Exec(ctx, "REVOKE TRUNCATE ON public.probe_existing FROM "+quoted); err != nil {
		t.Fatal(err)
	}
	owner := quoted[:len(quoted)-1] + "_owner\""
	for _, sql := range []string{
		"CREATE ROLE " + owner + " NOLOGIN NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS",
		"GRANT " + owner + " TO " + quoted,
		"GRANT USAGE,CREATE ON SCHEMA public TO " + owner,
		"GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public TO " + owner,
		"GRANT SELECT,USAGE ON ALL SEQUENCES IN SCHEMA public TO " + owner,
		"ALTER ROLE " + quoted + " SET role=" + owner,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "ALTER ROLE "+quoted+" RESET role")
		_, _ = pool.Exec(context.Background(), "DROP OWNED BY "+owner)
		_, _ = pool.Exec(context.Background(), "DROP ROLE "+owner)
	})
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialMigration, version); err != nil {
		t.Fatal("migration schema-owner role", err)
	}
	if _, err := pool.Exec(ctx, "ALTER ROLE "+owner+" BYPASSRLS"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialMigration, version); err == nil {
		t.Fatal("unsafe effective migration owner accepted")
	}
	if _, err := pool.Exec(ctx, "ALTER ROLE "+quoted+" RESET role"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "REVOKE "+owner+" FROM "+quoted); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "CREATE TABLE public.probe_acl_drift(id bigint)"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialReadWrite, version); err == nil {
		t.Fatal("missing target ACLs accepted")
	}
	if _, err := pool.Exec(ctx, "GRANT SELECT ON public.probe_acl_drift TO "+quoted); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "REVOKE INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA public FROM "+quoted); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "REVOKE USAGE ON ALL SEQUENCES IN SCHEMA public FROM "+quoted); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialReadOnly, version); err != nil {
		t.Fatal("read-only SQL verification", err)
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialReadWrite, version); err == nil {
		t.Fatal("read-only ACLs accepted for writer")
	}
	if _, err := pool.Exec(ctx, "ALTER ROLE "+quoted+" CREATEDB"); err != nil {
		t.Fatal(err)
	}
	if err := VerifyCredentialSQL(ctx, dsn.String(), CredentialReadOnly, version); err == nil {
		t.Fatal("unsafe role accepted")
	}
	cancelled, cancel := context.WithTimeout(ctx, time.Nanosecond)
	defer cancel()
	if err := VerifyCredentialSQL(cancelled, dsn.String(), CredentialReadOnly, version); err == nil {
		t.Fatal("expired probe succeeded")
	}
	bad := dsn
	bad.User = url.UserPassword(role, "wrong-secret")
	if err := VerifyCredentialSQL(ctx, bad.String(), CredentialReadOnly, version); err == nil || strings.Contains(err.Error(), "wrong-secret") || strings.Contains(err.Error(), role) {
		t.Fatal("authentication failure leaked credential")
	}
}
