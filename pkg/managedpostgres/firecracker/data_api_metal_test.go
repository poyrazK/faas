//go:build metal && linux

// adr: 1027, 650 — a Data API login on a native database, the way the
// Data API runtime uses it: through pgproxyd's pooled endpoint, one
// transaction per request with transaction-local JWT claims. Claims never
// leak into another transaction on a shared server connection, RLS holds,
// and the role's statement timeout applies on pooled sessions.
package firecracker

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestMetalDataAPIThroughThePooler(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the driver metal test")
	}
	st := newMetalStack(t, workdir, base)
	p := st.p
	p.cfg.DataAPI = true
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := uuid.NewString()
	t.Cleanup(func() { _ = st.sched.Delete(context.Background(), id) })
	if _, err := p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: id, Spec: devSpec()}); err != nil {
		t.Fatal(err)
	}
	issue := func(key string, access managedpostgres.CredentialAccess) managedpostgres.CredentialMaterial {
		t.Helper()
		m, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: key, Access: access})
		if err != nil {
			t.Fatalf("issue %s: %v", access, err)
		}
		return m
	}
	connectRole := func(m managedpostgres.CredentialMaterial, role managedpostgres.EndpointRole) *pgx.Conn {
		t.Helper()
		var ep managedpostgres.Endpoint
		for _, e := range m.Endpoints {
			if e.Role == role {
				ep = e
			}
		}
		host, port, _ := net.SplitHostPort(st.proxyAddr)
		pc, err := pgx.ParseConfig("host=" + ep.Host + " port=" + port + " user=" + m.Username + " password=" + m.Password +
			" dbname=" + m.Database + " sslmode=" + m.TLSMode + " connect_timeout=30")
		if err != nil {
			t.Fatal(err)
		}
		pc.LookupFunc = func(context.Context, string) ([]string, error) { return []string{host}, nil }
		pc.TLSConfig.ServerName = ep.Host
		conn, err := pgx.ConnectConfig(ctx, pc)
		if err != nil {
			t.Fatalf("%s login on the %s endpoint: %v", m.Username, role, err)
		}
		t.Cleanup(func() { _ = conn.Close(context.WithoutCancel(ctx)) })
		return conn
	}
	mig := connectRole(issue("mig", managedpostgres.CredentialMigration), managedpostgres.EndpointDirect)
	// As the Data API starter's first migration does, before any Data API
	// login exists: the login then gets the existing tables.
	if _, err := mig.Exec(ctx, `CREATE SCHEMA IF NOT EXISTS api;
 CREATE TABLE api.notes(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY, subject text NOT NULL, body text NOT NULL);
 ALTER TABLE api.notes ENABLE ROW LEVEL SECURITY;
 CREATE POLICY own_notes ON api.notes USING (subject = current_setting('request.jwt.claims', true)::jsonb->>'sub')
   WITH CHECK (subject = current_setting('request.jwt.claims', true)::jsonb->>'sub')`); err != nil {
		t.Fatalf("migration: %v", err)
	}
	apiLogin := issue("api", managedpostgres.CredentialDataAPI)
	// Two pooled clients share the database's server connections.
	alice, anonymous := connectRole(apiLogin, managedpostgres.EndpointPooled), connectRole(apiLogin, managedpostgres.EndpointPooled)
	// request runs one Data API request as PostgREST does: claims set
	// transaction-locally first ('{}' for an anonymous caller). Whatever
	// server it lands on, no earlier request's claims are visible.
	request := func(conn *pgx.Conn, claims, sql string, args ...any) (int64, error) {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return 0, err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		var inherited string
		if err := tx.QueryRow(ctx, "SELECT coalesce(current_setting('request.jwt.claims', true), '')").Scan(&inherited); err != nil || inherited != "" {
			t.Fatalf("a pooled transaction started with claims %q, %v", inherited, err)
		}
		if _, err := tx.Exec(ctx, "SELECT set_config('request.jwt.claims', $1, true)", claims); err != nil {
			return 0, err
		}
		var n int64
		if err := tx.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			return 0, err
		}
		return n, tx.Commit(ctx)
	}
	started := time.Now()
	for i := range 50 {
		if _, err := request(alice, `{"sub":"alice"}`, "WITH n AS (INSERT INTO api.notes(subject, body) VALUES ('alice', $1) RETURNING 1) SELECT count(*) FROM n", "note"); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		if n, err := request(anonymous, "{}", "SELECT count(*) FROM api.notes"); err != nil || n != 0 {
			t.Fatalf("anonymous request %d sees %d notes, %v; claims leaked across pooled transactions", i, n, err)
		}
	}
	elapsed := time.Since(started)
	if n, err := request(anonymous, `{"sub":"alice"}`, "SELECT count(*) FROM api.notes"); err != nil || n != 50 {
		t.Fatalf("alice sees %d notes, %v", n, err)
	}
	if _, err := request(alice, `{"sub":"alice"}`, "WITH n AS (INSERT INTO api.notes(subject, body) VALUES ('bob', 'x') RETURNING 1) SELECT count(*) FROM n"); pgState(err) != "42501" {
		t.Fatalf("writing bob's row as alice = %v, want 42501", err)
	}
	var timeout string
	if err := alice.QueryRow(ctx, "SHOW statement_timeout").Scan(&timeout); err != nil || timeout != "15s" {
		t.Fatalf("pooled statement_timeout = %q, %v", timeout, err)
	}
	t.Logf("100 pooled Data API transactions in %v", elapsed.Round(time.Millisecond))
}
