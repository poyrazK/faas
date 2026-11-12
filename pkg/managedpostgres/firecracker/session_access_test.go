// adr: 20261109180000 — a session login gets read_write grants under its own role and marker.
package firecracker

import (
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/pgscram"
)

func TestSessionCredentialSQLGrantsReadWrite(t *testing.T) {
	v, err := pgscram.NewVerifier("pw", pgscram.DefaultIterations)
	if err != nil {
		t.Fatal(err)
	}
	session := credentialRoleName(testDatabaseID, "k", managedpostgres.CredentialSession)
	if session == credentialRoleName(testDatabaseID, "k", managedpostgres.CredentialReadWrite) {
		t.Fatal("session and read_write logins share a role")
	}
	scope := credentialScope(testDatabaseID, "gregale")
	sql, err := credentialSQL("gregale", session, scope, managedpostgres.CredentialSession, v.String())
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO " + session + ";",
		"gregale:credential:v1:" + scope + ":session'",
		// ADR-20261110120000: direct session logins are connection-limited.
		"ALTER ROLE " + session + " CONNECTION LIMIT " + strconv.Itoa(api.SessionBindingConnectionLimit) + ";",
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("session credential SQL lacks %q", want)
		}
	}
	readWrite, err := credentialSQL("gregale", credentialRoleName(testDatabaseID, "k", managedpostgres.CredentialReadWrite), scope, managedpostgres.CredentialReadWrite, v.String())
	if err != nil || strings.Contains(readWrite, "CONNECTION LIMIT") {
		t.Fatalf("read_write login connection-limited: %v", err)
	}
}

// adr: 20261110120000 — session is advertised only with session_enabled.
func TestSessionAdvertisedOnlyWhenEnabled(t *testing.T) {
	p, _, _ := newTestProvider(t)
	if slices.Contains(p.Capabilities().CredentialAccess, managedpostgres.CredentialSession) {
		t.Fatal("session advertised without session_enabled")
	}
	enabled, err := ParseConfig(managedpostgres.BackendConfig{Region: "eu-central", Settings: map[string]string{settingHostSuffix: "db.test"}, SessionEnabled: true})
	if err != nil || !enabled.Session {
		t.Fatalf("session_enabled not read: %+v, %v", enabled, err)
	}
	p.cfg.Session = true
	if caps := p.Capabilities(); !slices.Contains(caps.CredentialAccess, managedpostgres.CredentialSession) || caps.Validate() != nil {
		t.Fatalf("session capabilities = %+v", caps.CredentialAccess)
	}
}
