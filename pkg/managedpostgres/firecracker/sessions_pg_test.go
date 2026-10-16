// adr: 1027 — a cutover source's revoked identities (ADR-1370 §4) lose
// their open sessions, and the observation reports what they still hold.
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

func TestRoleListRefusesForeignRoleNames(t *testing.T) {
	for _, roles := range [][]string{nil, {}, {"postgres"}, {"gregale_rt_x; DROP ROLE postgres"}, {"gregale_schema_owner"}} {
		if _, err := roleList(roles); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Errorf("roleList(%q) = %v, want ErrInvalid", roles, err)
		}
	}
	role := credentialRoleName(uuid.NewString(), "binding", managedpostgres.CredentialReadWrite)
	if list, err := roleList([]string{role}); err != nil || list != "ARRAY['"+role+"']::name[]" {
		t.Fatalf("roleList = %q, %v", list, err)
	}
}

// The server needs max_prepared_transactions > 0 for the prepared case
// (pg_ctl -o "-c max_prepared_transactions=10"); it is skipped otherwise.
func TestRevokedRolesLoseTheirSessionsAndTheObservationSeesWhatRemains(t *testing.T) {
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
	dbName := "gregale_s" + hex.EncodeToString(suffix)
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
	issue := func(key string) (managedpostgres.CredentialRequest, managedpostgres.CredentialMaterial) {
		t.Helper()
		req := managedpostgres.CredentialRequest{ProviderResourceID: resourceID, IdentityKey: key, Access: managedpostgres.CredentialReadWrite}
		m, err := p.IssueCredentials(ctx, req)
		if err != nil {
			t.Fatalf("issue %s: %v", key, err)
		}
		roles = append(roles, m.Username)
		return req, m
	}
	connect := func(m managedpostgres.CredentialMaterial) (*pgx.Conn, error) {
		c := base.Copy()
		c.User, c.Password, c.Database, c.TLSConfig, c.Fallbacks = m.Username, m.Password, dbName, nil, nil
		return pgx.ConnectConfig(ctx, c)
	}
	mustConnect := func(m managedpostgres.CredentialMaterial) *pgx.Conn {
		t.Helper()
		conn, err := connect(m)
		if err != nil {
			t.Fatalf("%s login: %v", m.Username, err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })
		return conn
	}
	sourceReq, source := issue("source")
	_, other := issue("other")
	sourceConn, otherConn := mustConnect(source), mustConnect(other)
	set := []string{source.Username}

	if sessions, prepared, err := p.roleSessionsRemain(ctx, resourceID, set); err != nil || !sessions || prepared {
		t.Fatalf("observation with an open session = %v %v, %v", sessions, prepared, err)
	}
	// Terminating alone ends the set's sessions and nothing else.
	for range 2 {
		if err := p.terminateRoleSessions(ctx, resourceID, set); err != nil {
			t.Fatalf("terminate: %v", err)
		}
	}
	waitClear := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			sessions, _, err := p.roleSessionsRemain(ctx, resourceID, set)
			if err != nil {
				t.Fatal(err)
			}
			if !sessions {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("terminated sessions did not end")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitClear()
	if err := sourceConn.Ping(ctx); err == nil {
		t.Fatal("the terminated session survived")
	}
	if err := otherConn.Ping(ctx); err != nil {
		t.Fatalf("a binding outside the set lost its session: %v", err)
	}

	// The cutover's order: revoke (NOLOGIN), terminate, observe zero.
	sourceConn = mustConnect(source)
	if err := p.RevokeCredentials(ctx, sourceReq); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	// Through the SessionTerminator capability, as an activation calls it.
	if err := p.TerminateSessions(ctx, []managedpostgres.CredentialRequest{sourceReq}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		obs, err := p.ObserveSessions(ctx, []managedpostgres.CredentialRequest{sourceReq})
		if err != nil {
			t.Fatal(err)
		}
		if obs == (managedpostgres.SessionObservation{}) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("revoked identity still holds %+v", obs)
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := connect(source); pgState(err) != "28000" {
		t.Fatalf("revoked login = %v, want 28000", err)
	}

	// A prepared transaction outlives its session; the observation keeps
	// reporting it until it is resolved.
	var maxPrepared int
	if err := super.QueryRow(ctx, "SELECT current_setting('max_prepared_transactions')::int").Scan(&maxPrepared); err != nil {
		t.Fatal(err)
	}
	if maxPrepared == 0 {
		t.Log("max_prepared_transactions = 0: prepared-transaction case skipped")
		return
	}
	_, prep := issue("prepared")
	prepConn := mustConnect(prep)
	gid := "gregale-test-" + hex.EncodeToString(suffix)
	if _, err := prepConn.Exec(ctx, "BEGIN; SELECT 1; PREPARE TRANSACTION '"+gid+"'"); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	// ROLLBACK PREPARED runs in the transaction's database.
	inDB := base.Copy()
	inDB.Database = dbName
	resolver, err := pgx.ConnectConfig(ctx, inDB)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = resolver.Exec(context.Background(), "ROLLBACK PREPARED '"+gid+"'")
		_ = resolver.Close(context.Background())
	})
	prepSet := []string{prep.Username}
	if err := p.terminateRoleSessions(ctx, resourceID, prepSet); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for {
		sessions, prepared, err := p.roleSessionsRemain(ctx, resourceID, prepSet)
		if err != nil {
			t.Fatal(err)
		}
		if !sessions {
			if !prepared {
				t.Fatal("the prepared transaction was not reported")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("sessions did not end")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, err := resolver.Exec(ctx, "ROLLBACK PREPARED '"+gid+"'"); err != nil {
		t.Fatal(err)
	}
	if _, prepared, err := p.roleSessionsRemain(ctx, resourceID, prepSet); err != nil || prepared {
		t.Fatalf("after ROLLBACK PREPARED = %v, %v", prepared, err)
	}
}
