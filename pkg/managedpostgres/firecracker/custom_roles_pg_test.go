//go:build !no_pg

// adr: 20261116213000 — custom Postgres group roles.
package firecracker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// Runs only against a disposable server (FAAS_DBVM_SQL_DSN), like
// TestCredentialBatchesEnforceTheRoleModel.
func TestCustomRoleBatchesRunWindmillStyleRoles(t *testing.T) {
	dsn := os.Getenv("FAAS_DBVM_SQL_DSN")
	if dsn == "" {
		t.Skip("FAAS_DBVM_SQL_DSN (a disposable PostgreSQL superuser DSN) not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	base, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	tag := hex.EncodeToString(suffix)
	dbName := "gregale_r" + tag
	user, admin := "wm_user_"+tag, "wm_admin_"+tag
	cfg, err := ParseConfig(managedpostgres.BackendConfig{Region: "eu-central", Settings: map[string]string{settingHostSuffix: "db.test", settingDatabase: dbName}})
	if err != nil {
		t.Fatal(err)
	}
	cfg.CredentialKey = testCredentialKey
	sched, verifiers := newFakeScheduler(), newFakeVerifiers()
	sched.adminRun = psqlAdmin(t, dsn)
	p, err := New(cfg, sched, verifiers)
	if err != nil {
		t.Fatal(err)
	}
	super, err := pgx.ConnectConfig(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	roles := []string{admin, user}
	t.Cleanup(func() {
		ctx := context.WithoutCancel(ctx)
		_, _ = super.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		_, _ = super.Exec(ctx, "GRANT CONNECT ON DATABASE postgres, template1 TO PUBLIC")
		for _, role := range roles {
			_, _ = super.Exec(ctx, "DROP ROLE IF EXISTS "+role)
		}
		_ = super.Close(ctx)
	})
	resourceID := uuid.NewString()
	if _, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: resourceID, Spec: devSpec()}); err != nil {
		t.Fatalf("provision: %v", err)
	}
	issue := func(key string, access managedpostgres.CredentialAccess, custom ...string) (managedpostgres.CredentialMaterial, error) {
		m, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: resourceID, IdentityKey: key, Access: access, CustomRoles: managedpostgres.NewCustomRoleGrants(custom...)})
		if err == nil {
			roles = append(roles, m.Username)
		}
		return m, err
	}
	connect := func(m managedpostgres.CredentialMaterial) *pgx.Conn {
		t.Helper()
		c := base.Copy()
		c.User, c.Password, c.Database, c.TLSConfig, c.Fallbacks = m.Username, m.Password, dbName, nil, nil
		conn, err := pgx.ConnectConfig(ctx, c)
		if err != nil {
			t.Fatalf("%s login: %v", m.Username, err)
		}
		t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
		return conn
	}
	migMaterial, err := issue("mig", managedpostgres.CredentialMigration)
	if err != nil {
		t.Fatal(err)
	}
	rwMaterial, err := issue("rw", managedpostgres.CredentialReadWrite)
	if err != nil {
		t.Fatal(err)
	}
	both := []managedpostgres.CredentialAccess{managedpostgres.CredentialMigration, managedpostgres.CredentialReadWrite}
	desired := []managedpostgres.CustomRoleSpec{
		{Name: user, GrantedTo: both},
		{Name: admin, BypassRLS: true, GrantedTo: both, MemberOf: []string{user}},
	}
	for range 2 { // idempotent
		if err := p.ApplyCustomRoles(ctx, resourceID, desired); err != nil {
			t.Fatalf("apply: %v", err)
		}
	}
	mig := connect(migMaterial)
	for _, sql := range []string{
		"CREATE TABLE job (id bigserial PRIMARY KEY, owner text NOT NULL)",
		"ALTER TABLE job ENABLE ROW LEVEL SECURITY",
		"CREATE POLICY see_own ON job TO " + user + " USING (owner = current_setting('session.user', true))",
		"GRANT ALL ON job TO " + user + ", " + admin,
		"INSERT INTO job (owner) VALUES ('alice'), ('bob')",
		// Windmill calls set_session_context() without granting it: PUBLIC
		// has no EXECUTE here (ADR-20261123112326).
		"CREATE FUNCTION set_session_context(username text) RETURNS void LANGUAGE sql AS $fn$ SELECT set_config('session.user', username, true) $fn$",
	} {
		if _, err := mig.Exec(ctx, sql); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	rw := connect(rwMaterial)
	count := func(role string) int {
		t.Helper()
		tx, err := rw.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+role); err != nil {
			t.Fatalf("SET LOCAL ROLE %s: %v", role, err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_session_context('alice')"); err != nil {
			t.Fatalf("set_session_context as %s: %v", role, err)
		}
		var n int
		if err := tx.QueryRow(ctx, "SELECT count(*) FROM job").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if _, err := rw.Exec(ctx, "SELECT set_session_context('alice')"); err != nil {
		t.Fatalf("read_write login cannot run the migration's function: %v", err)
	}
	if n := count(user); n != 1 {
		t.Fatalf("user saw %d rows, want 1", n)
	}
	if n := count(admin); n != 2 {
		t.Fatalf("admin saw %d rows, want 2", n)
	}
	// A login issued later is granted the existing roles at issue time.
	later, err := issue("rw-later", managedpostgres.CredentialReadWrite, user, admin)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := connect(later).Exec(ctx, "SET ROLE "+admin); err != nil {
		t.Fatalf("later login cannot SET ROLE: %v", err)
	}
	// Amendment 2: read_write logins start as the default role.
	startsAs := func(m managedpostgres.CredentialMaterial) string {
		t.Helper()
		var role string
		if err := connect(m).QueryRow(ctx, "SELECT current_user::text").Scan(&role); err != nil {
			t.Fatal(err)
		}
		return role
	}
	desired[1].DefaultFor = []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite}
	if err := p.ApplyCustomRoles(ctx, resourceID, desired); err != nil {
		t.Fatalf("apply default: %v", err)
	}
	if got := startsAs(rwMaterial); got != admin {
		t.Fatalf("read_write login starts as %q, want %q", got, admin)
	}
	if got := startsAs(migMaterial); got != schemaOwner {
		t.Fatalf("migration login starts as %q, want the schema owner", got)
	}
	defaulted, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: resourceID, IdentityKey: "rw-default",
		Access: managedpostgres.CredentialReadWrite, CustomRoles: managedpostgres.NewCustomRoleGrants(user, admin), CustomRoleDefault: admin})
	if err != nil {
		t.Fatal(err)
	}
	roles = append(roles, defaulted.Username)
	if got := startsAs(defaulted); got != admin {
		t.Fatalf("login issued with a default starts as %q", got)
	}
	desired[1].DefaultFor = nil
	if err := p.ApplyCustomRoles(ctx, resourceID, desired); err != nil {
		t.Fatalf("apply without default: %v", err)
	}
	if got := startsAs(defaulted); got != defaulted.Username {
		t.Fatalf("after removing the default the login starts as %q", got)
	}
	// Data API logins are refused while a BYPASSRLS role exists, before the
	// login is created.
	p.cfg.DataAPI = true
	if _, err := issue("api", managedpostgres.CredentialDataAPI); !errors.Is(err, managedpostgres.ErrBypassRLSConflict) {
		t.Fatalf("data api login with a BYPASSRLS role: %v", err)
	}
	var created bool
	if err := super.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)", credentialRoleName(resourceID, "api", managedpostgres.CredentialDataAPI)).Scan(&created); err != nil || created {
		t.Fatalf("refused data api login was created (%v)", err)
	}
	// Dropping the declaration drops the roles.
	if err := p.ApplyCustomRoles(ctx, resourceID, nil); err != nil {
		t.Fatalf("empty apply: %v", err)
	}
	var left bool
	if err := super.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname IN ($1, $2))", user, admin).Scan(&left); err != nil || left {
		t.Fatalf("roles survived (%v)", err)
	}
}
