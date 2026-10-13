//go:build !no_pg

// adr: 1027
package firecracker

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	scheddpb "github.com/onebox-faas/faas/api/proto/onebox/faas/schedd/v1"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

// The admin batches change cluster-wide defaults (they revoke CONNECT on
// the maintenance databases from PUBLIC), which is right inside a database
// VM and wrong on a shared CI server. This test therefore runs only against
// a disposable server named by FAAS_DBVM_SQL_DSN (superuser, no TLS;
// pg_hba must use scram-sha-256 for other roles), never DATABASE_URL.

var psqlSQLState = regexp.MustCompile(`(?m)^(?:psql:[^\n]*?)?(?:ERROR|FATAL):\s+([0-9A-Z]{5})\b`)

// psqlAdmin runs a batch the way guest-init's admin_sql op does.
func psqlAdmin(t *testing.T, dsn string) func(string) (*scheddpb.DatabaseAdminResponse, error) {
	psql := os.Getenv("FAAS_PSQL")
	if psql == "" {
		var err error
		if psql, err = exec.LookPath("psql"); err != nil {
			t.Skip("psql not found (set FAAS_PSQL)")
		}
	}
	return func(sql string) (*scheddpb.DatabaseAdminResponse, error) {
		cmd := exec.Command(psql, "-X", "-q", "-v", "ON_ERROR_STOP=1", "-v", "VERBOSITY=sqlstate", "-d", dsn, "-f", "-") //nolint:gosec // test-only: the operator's psql
		cmd.Stdin = bytes.NewBufferString(sql)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			state := "XX000"
			if m := psqlSQLState.FindSubmatch(stderr.Bytes()); m != nil {
				state = string(m[1])
			}
			t.Logf("admin batch failed (sqlstate %s): %s", state, stderr.String())
			return &scheddpb.DatabaseAdminResponse{ErrorCode: "sql_failed", Sqlstate: state}, nil
		}
		return &scheddpb.DatabaseAdminResponse{}, nil
	}
}

func pgState(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code
	}
	return ""
}

func TestCredentialBatchesEnforceTheRoleModel(t *testing.T) {
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
	dbName := "gregale_t" + hex.EncodeToString(suffix)
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
	var roles []string
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = super.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		// initSQL revokes CONNECT on the maintenance databases cluster-wide;
		// give it back so later tests on the same server are unaffected.
		_, _ = super.Exec(ctx, "GRANT CONNECT ON DATABASE postgres, template1 TO PUBLIC")
		for _, role := range roles {
			_, _ = super.Exec(ctx, "DROP ROLE IF EXISTS "+role)
		}
		_ = super.Close(ctx)
	})

	resourceID := uuid.NewString()
	for range 2 { // provisioning repeats safely
		if _, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: resourceID, Spec: devSpec()}); err != nil {
			t.Fatalf("provision: %v", err)
		}
	}
	issue := func(key string, access managedpostgres.CredentialAccess) managedpostgres.CredentialMaterial {
		t.Helper()
		m, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: resourceID, IdentityKey: key, Access: access})
		if err != nil {
			t.Fatalf("issue %s: %v", access, err)
		}
		roles = append(roles, m.Username)
		return m
	}
	connect := func(m managedpostgres.CredentialMaterial, password, database string) (*pgx.Conn, error) {
		c := base.Copy()
		c.User, c.Password, c.Database, c.TLSConfig, c.Fallbacks = m.Username, password, database, nil, nil
		return pgx.ConnectConfig(ctx, c)
	}
	mustConnect := func(m managedpostgres.CredentialMaterial) *pgx.Conn {
		t.Helper()
		conn, err := connect(m, m.Password, dbName)
		if err != nil {
			t.Fatalf("%s login: %v", m.Username, err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })
		return conn
	}

	mig := mustConnect(issue("binding-mig", managedpostgres.CredentialMigration))
	rwMaterial := issue("binding-rw", managedpostgres.CredentialReadWrite)
	rw := mustConnect(rwMaterial)
	ro := mustConnect(issue("binding-ro", managedpostgres.CredentialReadOnly))

	// Migrations create objects owned by the schema owner, which the
	// default privileges then expose to existing runtime roles.
	if _, err := mig.Exec(ctx, "CREATE TABLE items (id bigserial PRIMARY KEY, v text)"); err != nil {
		t.Fatalf("migration role cannot create: %v", err)
	}
	var owner string
	if err := mig.QueryRow(ctx, "SELECT tableowner FROM pg_tables WHERE tablename = 'items'").Scan(&owner); err != nil || owner != schemaOwner {
		t.Fatalf("items owner = %q, %v", owner, err)
	}
	if _, err := rw.Exec(ctx, "INSERT INTO items (v) VALUES ('a')"); err != nil {
		t.Fatalf("read-write insert: %v", err)
	}
	var n int
	if err := ro.QueryRow(ctx, "SELECT count(*) FROM items").Scan(&n); err != nil || n != 1 {
		t.Fatalf("read-only select = %d, %v", n, err)
	}
	// A role issued after the table exists gets it through ON ALL TABLES.
	ro2 := mustConnect(issue("binding-ro-2", managedpostgres.CredentialReadOnly))
	if err := ro2.QueryRow(ctx, "SELECT count(*) FROM items").Scan(&n); err != nil {
		t.Fatalf("late read-only select: %v", err)
	}

	denied := []struct {
		name string
		conn *pgx.Conn
		sql  string
	}{
		{"read-write create table", rw, "CREATE TABLE evil (id int)"},
		{"read-write temp table", rw, "CREATE TEMP TABLE evil (id int)"},
		{"read-write create schema", rw, "CREATE SCHEMA evil"},
		{"read-write drop table", rw, "DROP TABLE items"},
		{"read-only insert", ro, "INSERT INTO items (v) VALUES ('b')"},
		{"read-only nextval", ro, "SELECT nextval('items_id_seq')"},
		{"read-only create table", ro, "CREATE TABLE evil (id int)"},
	}
	for _, tc := range denied {
		if _, err := tc.conn.Exec(ctx, tc.sql); pgState(err) != "42501" {
			t.Errorf("%s: err = %v, want 42501", tc.name, err)
		}
	}
	for _, other := range []string{"postgres", "template1"} {
		if _, err := connect(rwMaterial, rwMaterial.Password, other); pgState(err) != "42501" {
			t.Errorf("connect to %s = %v, want 42501", other, err)
		}
	}

	// Revoking ends live sessions and refuses new ones.
	req := managedpostgres.CredentialRequest{ProviderResourceID: resourceID, IdentityKey: "binding-rw", Access: managedpostgres.CredentialReadWrite}
	if err := p.RevokeCredentials(ctx, req); err != nil {
		t.Fatal(err)
	}
	if _, err := rw.Exec(ctx, "SELECT 1"); err == nil {
		t.Error("a revoked role's session survived")
	}
	if _, err := connect(rwMaterial, rwMaterial.Password, dbName); pgState(err) != "28000" {
		t.Errorf("revoked login = %v, want 28000", err)
	}
	// Re-issuing the same binding re-enables the same login; rotation is a
	// new identity, hence a new role, and the revoked one stays out.
	reissued := issue("binding-rw", managedpostgres.CredentialReadWrite)
	if reissued.Password != rwMaterial.Password {
		t.Error("re-issue of the same binding changed the password")
	}
	mustConnect(reissued)
	if err := p.RevokeCredentials(ctx, req); err != nil {
		t.Fatal(err)
	}
	rotated := issue("binding-rw-2", managedpostgres.CredentialReadWrite)
	mustConnect(rotated)
	if _, err := connect(rwMaterial, rwMaterial.Password, dbName); pgState(err) != "28000" {
		t.Errorf("revoked login after rotation = %v, want 28000", err)
	}

	// The closing assertion refuses a runtime role that could create objects.
	grant := func(sql string) {
		t.Helper()
		db := base.Copy()
		db.Database = dbName
		conn, err := pgx.ConnectConfig(ctx, db)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close(ctx) }()
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	grant("GRANT CREATE ON SCHEMA public TO PUBLIC")
	_, err = p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: resourceID, IdentityKey: "binding-bad", Access: managedpostgres.CredentialReadWrite})
	if !errors.Is(err, managedpostgres.ErrConflict) {
		t.Fatalf("issue with a public CREATE grant = %v, want ErrConflict", err)
	}
	roles = append(roles, credentialRoleName(resourceID, "binding-bad", managedpostgres.CredentialReadWrite))
	if _, ok := verifiers.get(resourceID, credentialRoleName(resourceID, "binding-bad", managedpostgres.CredentialReadWrite)); ok {
		t.Fatal("a refused role's verifier was published")
	}
	grant("REVOKE CREATE ON SCHEMA public FROM PUBLIC")
}

// The platform's provider qualification, end to end on PostgreSQL: every
// check of QualifyProvider, including all four probers, must pass before
// native databases can be enabled. Admin batches run through psql as
// guest-init runs them; probe connections go straight to the server, and
// the connector marks the database awake the way pgproxyd's wake would.
func TestQualifyProviderPasses(t *testing.T) {
	dsn := os.Getenv("FAAS_DBVM_SQL_DSN")
	if dsn == "" {
		t.Skip("FAAS_DBVM_SQL_DSN (a disposable PostgreSQL superuser DSN) not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	base, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	suffix := make([]byte, 4)
	_, _ = rand.Read(suffix)
	dbName := "gregale_q" + hex.EncodeToString(suffix)
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
	p.poll = 20 * time.Millisecond
	p.WithConnector(func(ctx context.Context, m managedpostgres.CredentialMaterial) (*pgx.Conn, error) {
		c := base.Copy()
		c.User, c.Password, c.Database, c.TLSConfig, c.Fallbacks = m.Username, m.Password, m.Database, nil, nil
		conn, err := pgx.ConnectConfig(ctx, c)
		if err == nil {
			sched.setState(m.Endpoints[0].Host[:36], "running")
		}
		return conn, err
	})
	super, err := pgx.ConnectConfig(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		_, _ = super.Exec(ctx, "DROP DATABASE IF EXISTS "+dbName+" WITH (FORCE)")
		// initSQL revokes CONNECT on the maintenance databases cluster-wide;
		// give it back so later tests on the same server are unaffected.
		_, _ = super.Exec(ctx, "GRANT CONNECT ON DATABASE postgres, template1 TO PUBLIC")
		rows, err := super.Query(ctx, `SELECT rolname FROM pg_roles WHERE rolname ~ '^gregale_(rt|ro|mig)_'`)
		if err == nil {
			var roles []string
			for rows.Next() {
				var r string
				if rows.Scan(&r) == nil {
					roles = append(roles, r)
				}
			}
			rows.Close()
			for _, r := range roles {
				_, _ = super.Exec(ctx, "DROP ROLE IF EXISTS "+r)
			}
		}
		_ = super.Close(ctx)
	})

	report, err := managedpostgres.QualifyProvider(ctx, p, managedpostgres.QualificationOptions{
		ProviderName: Driver, ResourceID: uuid.NewString(), Spec: devSpec(), Mutating: true,
		Timeout: 4 * time.Minute, PollInterval: 20 * time.Millisecond,
	})
	for _, check := range report.Checks {
		t.Logf("check %s passed=%v %s", check.Name, check.Passed, check.Error)
	}
	if err != nil {
		t.Fatalf("QualifyProvider = %v", err)
	}
	if report.ScaleToZero == nil || report.CredentialPrivileges == nil || report.ReadOnlyCredentials == nil || report.ComputePolicy == nil {
		t.Fatalf("report lacks prober evidence: %+v", report)
	}
}
