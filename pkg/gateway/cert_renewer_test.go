package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// renewerFixture seeds a memstore with one surface whose
// cert_not_after is well inside the renew window so
// tickOnce surfaces it; the second surface is outside the
// window and must NOT be touched.
func renewerFixture(t *testing.T) (*SurfaceCertRenewer, *state.MemStore, state.TenantSurface, context.Context) {
	t.Helper()
	m := state.NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "renewer@example.com", api.PlanPro)
	app, _ := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "renewer", RAMMB: 256, Status: state.AppActive})
	lim := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 10}
	surf, _ := m.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "renewer",
	}, lim)
	// Mark the surface active + issued with cert_not_after
	// 5 days in the future (well inside the 30-day renew
	// window). The renewer's tickOnce should pick it up.
	if err := m.UpdateTenantSurfaceStatus(ctx, surf.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateTenantSurfaceCert(ctx, state.UpdateSurfaceCertParams{
		SurfaceID: surf.ID,
		CertState: state.CertStateIssued,
		NotAfter:  time.Now().Add(5 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	renewer := NewSurfaceCertRenewer(m, nil)
	renewer.SetTick(time.Hour) // not used in tickOnce tests
	renewer.SetRenewBefore(30 * 24 * time.Hour)
	return renewer, m, surf, ctx
}

func TestSurfaceCertRenewer_TickOnce_NilStoreFailsClosed(t *testing.T) {
	r := NewSurfaceCertRenewer(nil, nil)
	if err := r.tickOnce(context.Background()); err == nil {
		t.Fatal("nil store = nil err; want non-nil")
	}
}

func TestSurfaceCertRenewer_TickOnce_RuntimeDisabledIsNoop(t *testing.T) {
	r := NewSurfaceCertRenewer(nil, nil).SetEnabled(func() bool { return false })
	if err := r.tickOnce(context.Background()); err != nil {
		t.Fatalf("disabled renewer tick = %v, want nil", err)
	}
}

func TestSurfaceCertRenewer_TickOnce_PicksUpDueSurface(t *testing.T) {
	r, m, surf, ctx := renewerFixture(t)
	before := time.Now()
	if err := r.tickOnce(ctx); err != nil {
		t.Fatalf("tickOnce: %v", err)
	}
	// UpdatedAt should have been bumped — the renewer touched
	// the row. Read via the surface row directly.
	got, err := m.GetTenantSurfaceByID(ctx, surf.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.UpdatedAt.Before(before) {
		t.Errorf("UpdatedAt = %v, want >= %v (renewer should have touched)", got.UpdatedAt, before)
	}
}

func TestSurfaceCertRenewer_TickOnce_SkipsOutsideWindow(t *testing.T) {
	m := state.NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "skip@example.com", api.PlanPro)
	app, _ := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "skip", RAMMB: 256, Status: state.AppActive})
	lim := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 10}
	surf, _ := m.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "skip",
	}, lim)
	if err := m.UpdateTenantSurfaceStatus(ctx, surf.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	// Cert expires 90 days in the future — well outside the
	// 30-day renew window. The renewer must NOT touch it.
	if err := m.UpdateTenantSurfaceCert(ctx, state.UpdateSurfaceCertParams{
		SurfaceID: surf.ID,
		CertState: state.CertStateIssued,
		NotAfter:  time.Now().Add(90 * 24 * time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	r := NewSurfaceCertRenewer(m, nil)
	r.SetRenewBefore(30 * 24 * time.Hour)
	if err := r.tickOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetTenantSurfaceByID(ctx, surf.ID)
	if got.UpdatedAt.After(before.Add(time.Second)) {
		t.Errorf("UpdatedAt = %v, want before %v (renewer should not have touched)", got.UpdatedAt, before.Add(time.Second))
	}
}

func TestSurfaceCertRenewer_TickOnce_SkipsNonIssuedState(t *testing.T) {
	// cert_state != 'issued' surfaces must NOT be touched by
	// the renewer. The renewer's WHERE clause filters them
	// out so a 'failed' surface that just needs manual
	// intervention doesn't auto-retry.
	m := state.NewMemStore()
	ctx := context.Background()
	acct, _ := m.CreateAccount(ctx, "fail@example.com", api.PlanPro)
	app, _ := m.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "fail", RAMMB: 256, Status: state.AppActive})
	lim := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 10}
	surf, _ := m.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{
		AccountID: acct.ID, AppID: app.ID, Name: "fail",
	}, lim)
	if err := m.UpdateTenantSurfaceStatus(ctx, surf.ID, state.SurfaceStatusActive); err != nil {
		t.Fatal(err)
	}
	if err := m.UpdateTenantSurfaceCert(ctx, state.UpdateSurfaceCertParams{
		SurfaceID: surf.ID,
		CertState: state.CertStateFailed,
		LastError: "dns_poller never verified",
	}); err != nil {
		t.Fatal(err)
	}
	before := time.Now().UTC()
	r := NewSurfaceCertRenewer(m, nil)
	r.SetRenewBefore(30 * 24 * time.Hour)
	if err := r.tickOnce(ctx); err != nil {
		t.Fatal(err)
	}
	got, _ := m.GetTenantSurfaceByID(ctx, surf.ID)
	if got.UpdatedAt.After(before.Add(time.Second)) {
		t.Errorf("UpdatedAt = %v, want before %v (renewer should not touch failed surfaces)", got.UpdatedAt, before.Add(time.Second))
	}
}
