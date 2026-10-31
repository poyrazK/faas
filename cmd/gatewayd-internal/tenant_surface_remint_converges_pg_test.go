//go:build !no_pg

// Every tenant_surfaces write fires tenant_surface_changed, and the gateway
// answers each one with RequestCertForSurface. Against a live trigger, one
// remint must settle within a few notifies; flipping cert_state to pending on
// every call made each call emit two more, an unbounded storm on the channel
// that carries every routing invalidation.

package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTenantSurfaceRemintConvergesOnLiveTrigger(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	ctx := context.Background()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	s := state.NewPgStore(pool)
	acct, err := s.CreateAccount(ctx, "remint-converges@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := s.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "remint-converges", Type: state.AppTypeApp, RAMMB: 256, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	lim := api.Limits{TenantSurfacesAllowed: true, TenantSurfacesPerAccount: 5, TenantHostnamesPerSurface: 10}
	surf, err := s.CreateTenantSurfaceIfUnderQuota(ctx, state.CreateTenantSurfaceParams{AccountID: acct.ID, AppID: app.ID, Name: "remint", CertKind: state.CertKindPerHostSAN}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTenantHostnameIfUnderQuota(ctx, state.CreateTenantHostnameParams{SurfaceID: surf.ID, Hostname: "remint.example", ChallengeToken: "tok"}, lim); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.MarkTenantHostnameVerifiedIfChallenge(ctx, "remint.example", "tok"); err != nil || !ok {
		t.Fatalf("verify hostname = %v, %v", ok, err)
	}
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	if _, err := conn.Exec(ctx, "LISTEN tenant_surface_changed"); err != nil {
		t.Fatal(err)
	}

	// The engine is unwired, so every remint ends in failed.
	issuer := gateway.NewTenantSurfaceCertIssuer(s, gateway.NewMetrics(), nil, nil)
	if err := issuer.RequestCertForSurface(ctx, surf.ID); err != nil {
		t.Fatal(err)
	}
	handled := 0
	for ; handled <= 20; handled++ {
		waitCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		n, err := conn.Conn().WaitForNotification(waitCtx)
		cancel()
		if err != nil {
			break
		}
		if err := issuer.RequestCertForSurface(ctx, n.Payload); err != nil {
			t.Fatal(err)
		}
	}
	if handled > 4 {
		t.Fatalf("one remint produced more than %d notifies without settling; the remint feeds itself", handled-1)
	}
	got, err := s.GetTenantSurfaceByID(ctx, surf.ID)
	if err != nil || got.CertState != state.CertStateFailed {
		t.Fatalf("surface = %+v, %v; want cert_state failed", got, err)
	}
}
