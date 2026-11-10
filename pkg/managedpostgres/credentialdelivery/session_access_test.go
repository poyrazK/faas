// adr: 20261109180000 — session bindings are serving credentials on the direct endpoint.
package credentialdelivery_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery"
)

func TestConnectionURLSelectsEndpointByAccess(t *testing.T) {
	both := managedpostgres.CredentialMaterial{ProviderIdentityID: "role-1", Username: "role_1", Password: "secret", Database: "app", TLSMode: "require",
		Endpoints: []managedpostgres.Endpoint{
			{Role: managedpostgres.EndpointPooled, Host: "ep-1-pooler.example.com", Port: 5432},
			{Role: managedpostgres.EndpointDirect, Host: "ep-1.example.com", Port: 5432},
		}}
	pooledOnly := both
	pooledOnly.Endpoints = both.Endpoints[:1]
	for _, tc := range []struct {
		access   managedpostgres.CredentialAccess
		material managedpostgres.CredentialMaterial
		host     string
	}{
		{access: managedpostgres.CredentialReadWrite, material: both, host: "@ep-1-pooler.example.com:5432/"},
		{access: managedpostgres.CredentialSession, material: both, host: "@ep-1.example.com:5432/"},
		{access: managedpostgres.CredentialMigration, material: both, host: "@ep-1.example.com:5432/"},
		// A session login never falls back to the transaction pooler.
		{access: managedpostgres.CredentialSession, material: pooledOnly},
	} {
		got, err := credentialdelivery.ConnectionURL(tc.access, tc.material)
		if tc.host == "" {
			if !errors.Is(err, managedpostgres.ErrUnsupported) {
				t.Errorf("%s without a direct endpoint: got %q, %v; want ErrUnsupported", tc.access, got, err)
			}
			continue
		}
		if err != nil || !strings.Contains(got, tc.host) {
			t.Errorf("%s: got %q, %v; want host %s", tc.access, got, err, tc.host)
		}
	}
}

func TestSessionAccessHasReadWritePrivileges(t *testing.T) {
	session := managedpostgres.CredentialSession
	if !session.Known() || session.Privileges() != managedpostgres.CredentialReadWrite || !session.DirectOnly() {
		t.Fatalf("session: known=%v privileges=%q direct=%v", session.Known(), session.Privileges(), session.DirectOnly())
	}
	if managedpostgres.CredentialReadWrite.DirectOnly() || managedpostgres.CredentialReadOnly.Privileges() != managedpostgres.CredentialReadOnly {
		t.Fatal("read_write must stay pooled and other modes keep their own privileges")
	}
}
