// adr: 1027 — suspending a cutover source's binding identities (ADR-1370
// §4) stops every session and login while keeping the credentials, and a
// resume lets the same credentials in again.
package firecracker

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestSuspendSQLRefusesForeignRoleNames(t *testing.T) {
	for _, roles := range [][]string{nil, {}, {"postgres"}, {"gregale_rt_x; DROP ROLE postgres"}, {"gregale_schema_owner"}} {
		for name, build := range map[string]func([]string) (string, error){"suspend": suspendSQL, "resume": resumeSQL, "idle": idleSQL} {
			if _, err := build(roles); !errors.Is(err, managedpostgres.ErrInvalid) {
				t.Errorf("%s(%q) = %v, want ErrInvalid", name, roles, err)
			}
		}
	}
	role := credentialRoleName(uuid.NewString(), "binding", managedpostgres.CredentialReadWrite)
	if batch, err := suspendSQL([]string{role}); err != nil || !strings.Contains(batch, "'"+role+"'") {
		t.Fatalf("suspend batch = %q, %v", batch, err)
	}
}

func TestSuspendStopsSessionsAndLoginsAndResumeLetsThemBack(t *testing.T) {
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
	var materials []managedpostgres.CredentialMaterial
	for _, access := range []managedpostgres.CredentialAccess{managedpostgres.CredentialReadWrite, managedpostgres.CredentialReadOnly} {
		m, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: resourceID, IdentityKey: "binding-" + string(access), Access: access})
		if err != nil {
			t.Fatalf("issue %s: %v", access, err)
		}
		materials = append(materials, m)
		roles = append(roles, m.Username)
	}
	// Another binding on the same database is not in the suspended set.
	other, err := p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: resourceID, IdentityKey: "other", Access: managedpostgres.CredentialReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	roles = append(roles, other.Username)
	connect := func(m managedpostgres.CredentialMaterial) (*pgx.Conn, error) {
		c := base.Copy()
		c.User, c.Password, c.Database, c.TLSConfig, c.Fallbacks = m.Username, m.Password, dbName, nil, nil
		return pgx.ConnectConfig(ctx, c)
	}
	var sessions []*pgx.Conn
	for _, m := range append(materials, other) {
		conn, err := connect(m)
		if err != nil {
			t.Fatalf("%s login: %v", m.Username, err)
		}
		sessions = append(sessions, conn)
		t.Cleanup(func() { _ = conn.Close(context.Background()) })
	}
	suspended := []string{materials[0].Username, materials[1].Username}
	if idle, err := p.rolesIdle(ctx, resourceID, suspended); err != nil || idle {
		t.Fatalf("idle with open sessions = %v, %v", idle, err)
	}

	for range 2 { // suspension repeats safely
		if err := p.suspendRoles(ctx, resourceID, suspended); err != nil {
			t.Fatalf("suspend: %v", err)
		}
	}
	waitIdle := func() {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			idle, err := p.rolesIdle(ctx, resourceID, suspended)
			if err != nil {
				t.Fatal(err)
			}
			if idle {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("suspended roles kept their sessions")
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitIdle()
	for i, conn := range sessions[:2] {
		if err := conn.Ping(ctx); err == nil {
			t.Fatalf("%s's session survived the suspension", materials[i].Username)
		}
	}
	if err := sessions[2].Ping(ctx); err != nil {
		t.Fatalf("a binding outside the set lost its session: %v", err)
	}
	for _, m := range materials {
		if _, err := connect(m); pgState(err) != "28000" {
			t.Fatalf("%s login while suspended = %v, want 28000", m.Username, err)
		}
	}
	verifiers.mu.Lock()
	kept := len(verifiers.byID[resourceID])
	verifiers.mu.Unlock()
	if kept != 3 {
		t.Fatalf("verifiers after suspension = %d, want all 3 kept", kept)
	}

	for range 2 { // resume repeats safely
		if err := p.resumeRoles(ctx, resourceID, suspended); err != nil {
			t.Fatalf("resume: %v", err)
		}
	}
	for _, m := range materials {
		conn, err := connect(m)
		if err != nil {
			t.Fatalf("%s login after resume: %v", m.Username, err)
		}
		t.Cleanup(func() { _ = conn.Close(context.Background()) })
	}
	if idle, err := p.rolesIdle(ctx, resourceID, suspended); err != nil || idle {
		t.Fatalf("idle after resume with sessions = %v, %v", idle, err)
	}
}
