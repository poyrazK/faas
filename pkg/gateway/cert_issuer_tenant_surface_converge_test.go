// adr: 100 — every tenant_surfaces write notifies the remint path, so a
// remint must converge to no write at all.
package gateway

import (
	"testing"
	"time"

	"github.com/caddyserver/certmagic"

	"github.com/onebox-faas/faas/pkg/state"
)

// A remint that writes nothing new must not write at all: in production
// every tenant_surfaces UPDATE fires tenant_surface_changed, which calls
// RequestCertForSurface again. Flipping pending on every call turned one
// remint into an unbounded notify storm.
func TestTenantSurfaceCertIssuerReentryWritesNothing(t *testing.T) {
	issuer, m, surf, ctx := tenantSurfaceIssuerFixture(t)
	if err := issuer.RequestCertForSurface(ctx, surf.ID); err != nil {
		t.Fatal(err)
	}
	first, err := m.GetTenantSurfaceByID(ctx, surf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if first.CertState != state.CertStateFailed {
		t.Fatalf("cert_state = %q, want failed with the engine unwired", first.CertState)
	}
	for i := 0; i < 3; i++ {
		if err := issuer.RequestCertForSurface(ctx, surf.ID); err != nil {
			t.Fatal(err)
		}
	}
	again, _ := m.GetTenantSurfaceByID(ctx, surf.ID)
	if !again.UpdatedAt.Equal(first.UpdatedAt) || again.CertState != state.CertStateFailed {
		t.Fatalf("re-entry rewrote the surface: updated_at %v → %v, state %q", first.UpdatedAt, again.UpdatedAt, again.CertState)
	}
}

// A renewal keeps cert_state=issued and must still record the new expiry,
// or the renewer keeps re-kicking a surface whose certificate it renewed.
func TestTenantSurfaceCertIssuerRenewalRecordsNewExpiry(t *testing.T) {
	issuer, m, surf, ctx := tenantSurfaceIssuerFixture(t)
	le, err := NewLetsEncryptCertIssuer(t.TempDir(), "ops@example.com", true, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := le.initConfig(); err != nil {
		t.Fatal(err)
	}
	fake := &fakeLEIssuer{}
	le.cfg.Issuers = []certmagic.Issuer{fake}
	issuer.SetLetsEncryptIssuer(le)
	now := time.Now()

	// An earlier run issued both certificates; 10 of 90 days remain.
	fake.setWindow(now.Add(-80*24*time.Hour), now.Add(10*24*time.Hour))
	for _, host := range []string{"a.example", "b.example"} {
		if err := le.cfg.ObtainCertSync(ctx, host); err != nil {
			t.Fatal(err)
		}
	}
	old := now.Add(10 * 24 * time.Hour).Truncate(time.Second)
	if err := m.UpdateTenantSurfaceCert(ctx, state.UpdateSurfaceCertParams{SurfaceID: surf.ID, CertState: state.CertStateIssued, NotAfter: old}); err != nil {
		t.Fatal(err)
	}

	fake.setWindow(now.Add(-time.Hour), now.Add(90*24*time.Hour))
	if err := issuer.RequestCertForSurface(ctx, surf.ID); err != nil {
		t.Fatal(err)
	}
	renewed, _ := m.GetTenantSurfaceByID(ctx, surf.ID)
	if renewed.CertState != state.CertStateIssued || !renewed.CertNotAfter.After(now.Add(80*24*time.Hour)) {
		t.Fatalf("after renewal: state %q, cert_not_after %v; want issued with the new expiry", renewed.CertState, renewed.CertNotAfter)
	}
	orders := fake.issued()
	if err := issuer.RequestCertForSurface(ctx, surf.ID); err != nil {
		t.Fatal(err)
	}
	again, _ := m.GetTenantSurfaceByID(ctx, surf.ID)
	if fake.issued() != orders || !again.UpdatedAt.Equal(renewed.UpdatedAt) {
		t.Fatalf("re-entry after renewal: orders %d → %d, updated_at %v → %v; want neither to move", orders, fake.issued(), renewed.UpdatedAt, again.UpdatedAt)
	}
}
