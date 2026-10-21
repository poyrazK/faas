package neon

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// adr: 20261016151947 — issuing a database's live login prepares it (the
// schema-change log and publication) and creates a REPLICATION login that
// reads the customer tables, writes nothing, and may SET ROLE to the
// database's binding logins, including later ones, without inheriting their
// privileges. Issuing again changes only the password. Revoking ends its
// sessions and drops the login and its slot, and is idempotent.
func TestSQLLiveLoginIssueAndRevoke(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	if err := f.manager.Ensure(ctx, f.material, f.migration); err != nil {
		t.Fatal(err)
	}
	executeSQL(t, f.connect(t, f.migration.name), `CREATE TABLE items (id int PRIMARY KEY, v text); INSERT INTO items VALUES (1, 'a')`)
	if err := f.manager.Ensure(ctx, f.material, f.runtime); err != nil {
		t.Fatal(err)
	}
	live := &sqlLiveLogins{connect: f.manager.connect}
	login := liveLogin(f.readonly)
	later := f.readonly
	later.name += "_later"
	f.extraRoles = append(f.extraRoles, login.name, later.name)

	if _, err := live.Issue(ctx, f.material, f.readonly, "gregale_live"); err != nil {
		t.Fatal(err)
	}
	password := func() string {
		var hash string
		if err := f.admin.QueryRow(ctx, `SELECT rolpassword FROM pg_authid WHERE rolname = $1`, login.name).Scan(&hash); err != nil {
			t.Fatal(err)
		}
		return hash
	}
	first := password()
	if _, err := live.Issue(ctx, f.material, f.readonly, "gregale_live"); err != nil {
		t.Fatalf("issuing again: %v", err)
	}
	if password() == first {
		t.Error("issuing again kept the password")
	}

	var replication, inherit, prepared bool
	if err := f.admin.QueryRow(ctx, `SELECT r.rolreplication, r.rolinherit,
		to_regclass('gregale_live.ddl_log') IS NOT NULL AND EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'gregale_live')
		FROM pg_roles r WHERE r.rolname = $1`, login.name).Scan(&replication, &inherit, &prepared); err != nil {
		t.Fatal(err)
	}
	if !replication || inherit || !prepared {
		t.Fatalf("replication %v, inherit %v, prepared %v; want a NOINHERIT REPLICATION login on a prepared database", replication, inherit, prepared)
	}

	if err := f.manager.Ensure(ctx, f.material, later); err != nil {
		t.Fatalf("a binding created after enabling: %v", err)
	}
	for _, binding := range []string{f.runtime.name, later.name} {
		var set, usage bool
		if err := f.admin.QueryRow(ctx, `SELECT pg_has_role($1, $2, 'SET'), pg_has_role($1, $2, 'USAGE')`, login.name, binding).Scan(&set, &usage); err != nil {
			t.Fatal(err)
		}
		if !set || usage {
			t.Errorf("binding %s: SET %v, inherits %v; want SET without inheritance", binding, set, usage)
		}
	}

	conn := f.connect(t, login.name)
	var count int
	if err := conn.QueryRow(ctx, `SELECT count(*) FROM items`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("the live login read %d rows: %v", count, err)
	}
	deniedSQL(t, conn, `INSERT INTO items VALUES (2, 'b')`)
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var as string
	if err := tx.QueryRow(ctx, `SELECT set_config('role', $1, true), current_user`, f.runtime.name).Scan(new(string), &as); err != nil || as != f.runtime.name {
		t.Fatalf("SET ROLE to the binding: %q, %v", as, err)
	}
	_ = tx.Rollback(ctx)

	cfg := f.config.Config.Copy()
	cfg.User, cfg.Password = login.name, "local-fixture-password"
	cfg.RuntimeParams = map[string]string{"replication": "database"}
	repl, err := pgconn.ConnectConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("replication connection: %v", err)
	}
	if _, err := repl.Exec(ctx, "IDENTIFY_SYSTEM").ReadAll(); err != nil {
		t.Fatalf("IDENTIFY_SYSTEM: %v", err)
	}
	_ = repl.Close(ctx)

	slot := "lq_revoke_" + login.scope[:8]
	executeSQL(t, f.admin, `SELECT pg_create_logical_replication_slot('`+slot+`', 'pgoutput')`)
	if err := live.Revoke(ctx, f.material, f.readonly, slot); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	var roleLeft, slotLeft bool
	if err := f.admin.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1), EXISTS (SELECT 1 FROM pg_replication_slots WHERE slot_name = $2)`,
		login.name, slot).Scan(&roleLeft, &slotLeft); err != nil {
		t.Fatal(err)
	}
	if roleLeft || slotLeft {
		t.Fatalf("after revoke: role left %v, slot left %v", roleLeft, slotLeft)
	}
	if _, err := conn.Exec(ctx, "SELECT 1"); err == nil {
		t.Error("the live login's session survived revocation")
	}
	if err := live.Revoke(ctx, f.material, f.readonly, slot); err != nil {
		t.Errorf("revoking again: %v", err)
	}
}

// adr: 20261016151947 — a role that merely has the live login's name is not
// taken over.
func TestSQLLiveLoginRefusesAForeignRole(t *testing.T) {
	f := newCredentialFixture(t)
	ctx := context.Background()
	login := liveLogin(f.readonly)
	f.extraRoles = append(f.extraRoles, login.name)
	executeSQL(t, f.admin, "CREATE ROLE "+roleIdentifier(login.name)+" LOGIN")
	_, err := (&sqlLiveLogins{connect: f.manager.connect}).Issue(ctx, f.material, f.readonly, "gregale_live")
	if !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("issuing over a foreign role: %v, want ErrConflict", err)
	}
	var exists bool
	if err := f.admin.QueryRow(ctx, `SELECT to_regclass('gregale_live.ddl_log') IS NOT NULL`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Error("a refused issue left the schema-change log behind")
	}
}

// adr: 20261016151947 — a live login on a Neon endpoint tells livequeryd
// which compute to poll after releasing the database; any other host leaves
// the database never released.
func TestNeonAwake(t *testing.T) {
	direct := func(host string) []managedpostgres.Endpoint {
		return []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: host, Port: 5432}}
	}
	for host, want := range map[string]string{
		"ep-cool-darkness-123456.us-east-2.aws.neon.tech": "neon:proj-1/ep-cool-darkness-123456",
		"db.example.com": "",
		"":               "",
	} {
		if got := neonAwake("proj-1", direct(host)); got != want {
			t.Errorf("neonAwake(%q) = %q, want %q", host, got, want)
		}
	}
	if got := neonAwake("bad/project", direct("ep-a.neon.tech")); got != "" {
		t.Errorf("an invalid project ID produced %q", got)
	}
}
