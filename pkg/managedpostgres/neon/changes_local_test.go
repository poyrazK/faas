// adr: 20261112140000
package neon

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	commitwork "github.com/onebox-faas/faas/pkg/commit"
)

func TestCaptureTablesMatchCommit(t *testing.T) {
	if strings.Join(captureTables, ",") != strings.Join(commitwork.CaptureTables, ",") {
		t.Fatalf("neon captureTables %v != commit.CaptureTables %v", captureTables, commitwork.CaptureTables)
	}
}

// TestPostgresChangesOnNeonRoleLayout runs the managed install against a
// local reproduction of a Neon database's roles: a non-superuser CREATEROLE
// owner login (Neon's neondb_owner is not a superuser), the pipeline session
// exactly as WithPipelineSQL opens it, the schema owner and credential roles
// the provider creates, and a relay login authenticated by its SCRAM
// verifier over TCP. Set the cluster's host auth to scram-sha-256 to make the
// relay login prove the verifier; under trust it only proves the grants.
func TestPostgresChangesOnNeonRoleLayout(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	controllerName := "gregale_control_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	f.extraRoles = append(f.extraRoles, controllerName)
	executeSQL(t, f.admin, "CREATE ROLE "+roleIdentifier(controllerName)+" LOGIN CREATEROLE NOSUPERUSER PASSWORD 'local-owner-password'")
	// Neon's owner logins belong to neon_superuser, which reads and writes
	// all data through the predefined roles.
	platformAdmin := "neon_superuser_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	anon := "anon_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	f.extraRoles = append(f.extraRoles, platformAdmin, anon)
	executeSQL(t, f.admin, "CREATE ROLE "+roleIdentifier(platformAdmin)+" NOLOGIN CREATEROLE IN ROLE pg_read_all_data, pg_write_all_data")
	executeSQL(t, f.admin, "GRANT "+roleIdentifier(platformAdmin)+" TO "+roleIdentifier(controllerName)+" WITH ADMIN OPTION")
	executeSQL(t, f.admin, "CREATE ROLE "+roleIdentifier(anon)+" NOLOGIN")
	executeSQL(t, f.admin, "GRANT pg_signal_backend TO "+roleIdentifier(controllerName))
	executeSQL(t, f.admin, "ALTER DATABASE "+roleIdentifier(f.config.Database)+" OWNER TO "+roleIdentifier(controllerName))
	executeSQL(t, f.admin, "ALTER SCHEMA public OWNER TO "+roleIdentifier(controllerName))
	controllerConfig := f.config.Copy()
	controllerConfig.User, controllerConfig.Password = controllerName, "local-owner-password"
	f.manager.connect = func(ctx context.Context, _ string) (*pgx.Conn, error) {
		return pgx.ConnectConfig(ctx, controllerConfig.Copy())
	}

	// pipeline is WithPipelineSQL minus the Neon API: same session
	// parameters, same lock, same schema initialization, then run.
	pipeline := func(run func(*pgx.Conn, string) error) error {
		cfg := controllerConfig.Copy()
		cfg.RuntimeParams = map[string]string{"application_name": "gregale-pipeline-worker", "search_path": "pg_catalog", "statement_timeout": "15000", "lock_timeout": "2000", "TimeZone": "UTC"}
		conn, err := pgx.ConnectConfig(ctx, cfg)
		if err != nil {
			return err
		}
		defer func() { _ = conn.Close(ctx) }()
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, `SELECT pg_catalog.pg_advisory_xact_lock(pg_catalog.hashtextextended($1,0))`, f.migration.schemaOwner); err != nil {
			return err
		}
		if err := initializeCredentialSchema(ctx, tx, f.migration); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		return run(conn, f.migration.schemaOwner)
	}

	// The customer's schema arrives through the migration login, as a
	// deploy's migrations would.
	if err := pipeline(func(*pgx.Conn, string) error { return nil }); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatalf("migration login: %v", err)
	}
	// A read-only login issued before the install gets every future public
	// table through default privileges, as does a Supabase-style anon role.
	if err := f.manager.Ensure(ctx, f.material, f.readonly); err != nil {
		t.Fatalf("read_only login: %v", err)
	}
	executeSQL(t, f.admin, "ALTER DEFAULT PRIVILEGES FOR ROLE "+roleIdentifier(f.migration.schemaOwner)+" IN SCHEMA public GRANT ALL ON TABLES TO "+roleIdentifier(anon))
	t.Cleanup(func() {
		_, _ = f.admin.Exec(context.Background(), "ALTER DEFAULT PRIVILEGES FOR ROLE "+roleIdentifier(f.migration.schemaOwner)+" IN SCHEMA public REVOKE ALL ON TABLES FROM "+roleIdentifier(anon))
	})
	migration := f.connect(t, f.migration.name)
	executeSQL(t, migration, `CREATE TABLE public.orders (id int PRIMARY KEY, status text NOT NULL, card text)`)

	sourceID := uuid.NewString()
	password, err := commitwork.NewRelayPassword()
	if err != nil {
		t.Fatal(err)
	}
	var relayRole string
	install := func(conn *pgx.Conn, owner string) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		res, err := commitwork.InstallChanges(ctx, tx, commitwork.ChangesInstall{SourceID: sourceID, RelayPassword: password, SchemaOwner: owner,
			Feeds: []commitwork.ChangeFeed{{Name: "orders", Table: "orders", Columns: []string{"id", "status"}}}})
		if err != nil {
			return err
		}
		relayRole = res.RelayRole
		return tx.Commit(ctx)
	}
	if err := pipeline(install); err != nil {
		t.Fatalf("managed install: %v", err)
	}
	f.extraRoles = append(f.extraRoles, relayRole)
	// Every later pipeline session re-runs the schema checks, which refuse a
	// SECURITY DEFINER function PUBLIC can execute; a redeploy reinstalls.
	if err := pipeline(install); err != nil {
		t.Fatalf("pipeline session after install (redeploy): %v", err)
	}

	// An application login issued after the install writes the table.
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatalf("read_write login after install: %v", err)
	}
	app := f.connect(t, f.runtime.name)
	for _, stmt := range []string{
		`INSERT INTO public.orders VALUES (1, 'new', '4242')`,
		`UPDATE public.orders SET status = 'paid' WHERE id = 1`,
		`UPDATE public.orders SET card = '0000' WHERE id = 1`,
	} {
		executeSQL(t, app, stmt)
	}
	deniedSQL(t, app, `INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES (gen_random_uuid(),'database.orders.insert','{}')`)
	deniedSQL(t, app, `SELECT * FROM public.gregale_outbox`)
	deniedSQL(t, app, `DELETE FROM public.gregale_outbox`)
	deniedSQL(t, f.connect(t, f.readonly.name), `SELECT * FROM public.gregale_outbox`)
	for _, table := range commitwork.CaptureTables {
		var exposed bool
		if err := f.admin.QueryRow(ctx, `SELECT has_table_privilege($1, 'public.'||$2, 'SELECT,INSERT,UPDATE,DELETE')`, anon, table).Scan(&exposed); err != nil || exposed {
			t.Fatalf("anon keeps access to %s (%v)", table, err)
		}
	}
	// Rotating the app login keeps capture working.
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatalf("rotate read_write login: %v", err)
	}

	// The relay logs in over TCP with the verifier the install set.
	relayConfig, err := pgxpool.ParseConfig("")
	if err != nil {
		t.Fatal(err)
	}
	relayConfig.ConnConfig.Host, relayConfig.ConnConfig.Port = "127.0.0.1", f.config.Port
	relayConfig.ConnConfig.Database, relayConfig.ConnConfig.User, relayConfig.ConnConfig.Password = f.config.Database, relayRole, password
	relayConfig.ConnConfig.TLSConfig = nil
	pool, err := pgxpool.NewWithConfig(ctx, relayConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("relay login %s: %v", relayRole, err)
	}
	if err := commitwork.QualifySchemaVersion(ctx, pool, 1); err != nil {
		t.Fatalf("qualify schema: %v", err)
	}
	if err := commitwork.QualifySource(ctx, pool, sourceID); err != nil {
		t.Fatalf("qualify source: %v", err)
	}
	if _, err := pool.Exec(ctx, `SELECT count(*) FROM public.orders`); err == nil {
		t.Fatal("the relay login can read application tables")
	}
	acceptor := &liveChangesAcceptor{}
	relay := commitwork.Relay{ContractVersion: 1, SourceID: sourceID, Pool: pool, Acceptor: acceptor}
	n, err := relay.Tick(ctx)
	if err != nil || n != 2 || len(acceptor.events) != 2 {
		t.Fatalf("relay tick: accepted=%d err=%v events=%d", n, err, len(acceptor.events))
	}
	if acceptor.events[0].Type != "database.orders.insert" || acceptor.events[1].Type != "database.orders.update" {
		t.Fatalf("event types %s, %s", acceptor.events[0].Type, acceptor.events[1].Type)
	}
	for _, e := range acceptor.events {
		if strings.Contains(string(e.Data), "4242") || strings.Contains(string(e.Data), "card") {
			t.Fatalf("event carries a column that is not allowlisted: %s", e.Data)
		}
	}
	if n, err := relay.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("second tick redelivered: %d %v", n, err)
	}
	pool.Close()

	// Uninstall by the non-superuser owner drops triggers and the relay role.
	err = pipeline(func(conn *pgx.Conn, owner string) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := commitwork.RemoveChanges(ctx, tx, sourceID, owner); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	if err != nil {
		t.Fatalf("managed uninstall: %v", err)
	}
	var left bool
	if err := f.admin.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname=$1)`, relayRole).Scan(&left); err != nil || left {
		t.Fatalf("relay role left after uninstall (%v)", err)
	}
	executeSQL(t, app, `UPDATE public.orders SET status = 'shipped' WHERE id = 1`)
	if err := pipeline(func(*pgx.Conn, string) error { return nil }); err != nil {
		t.Fatalf("pipeline session after uninstall: %v", err)
	}
}
