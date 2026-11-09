// adr: 20261109120000 — a session login gets read_write grants under its own role and marker.
package firecracker

import (
	"strings"
	"testing"

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
	} {
		if !strings.Contains(sql, want) {
			t.Errorf("session credential SQL lacks %q", want)
		}
	}
}
