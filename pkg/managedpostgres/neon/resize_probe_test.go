// adr: 593 — real SQL reconnect and marker/reader enforcement evidence.
package neon

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestResizeMarkerReconnectsExistingCredentialsAndChecksPermissions(t *testing.T) {
	f := newCredentialFixture(t)
	if f.config.TLSConfig == nil {
		t.Skip("requires the disposable TLS PostgreSQL fixture")
	}
	ctx := t.Context()
	for _, role := range []credentialRole{f.migration, f.readonly} {
		if err := f.manager.Ensure(ctx, f.material, role); err != nil {
			t.Fatal(err)
		}
	}
	writer := f.connect(t, f.migration.name)
	executeSQL(t, writer, "CREATE TABLE public.resize_marker(value text PRIMARY KEY)")
	executeSQL(t, writer, "INSERT INTO public.resize_marker VALUES ('preserved')")
	_ = f.connect(t, f.readonly.name)
	material := f.material
	material.Password = "local-fixture-password"
	material.Endpoints = []managedpostgres.Endpoint{{Role: managedpostgres.EndpointDirect, Host: f.config.Host, Port: f.config.Port}}
	for i := 0; i < 2; i++ {
		material.Username = f.migration.name
		if err := verifyResizeMarker(ctx, material, "public.resize_marker", "preserved", false); err != nil {
			t.Fatal("writer reconnect", err)
		}
		material.Username = f.readonly.name
		if err := verifyResizeMarker(ctx, material, "public.resize_marker", "preserved", true); err != nil {
			t.Fatal("reader reconnect/permissions", err)
		}
	}
	if err := verifyResizeMarker(ctx, material, "public.resize_marker", "lost", true); err == nil {
		t.Fatal("wrong marker accepted")
	}
	material.Username = f.migration.name
	if err := verifyResizeMarker(ctx, material, "public.resize_marker", "preserved", true); err == nil {
		t.Fatal("writable credential passed reader proof")
	}
}
