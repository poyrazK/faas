// adr: 520 — custom domains: the edge's certificate decision and routing agree.
package main

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestCustomDomainAcceptance is the acceptance evidence for the Custom
// domains capability. It drives the live path: the on-demand TLS ask policy
// gatewayd-public serves to Caddy (ADR-520) and the host resolution
// gatewayd-internal routes with, both reading one store. A claimed domain gets
// neither a certificate nor traffic until its TXT challenge verifies it, then
// gets both, and loses both when it is removed. Platform hosts are never
// issued on demand.
func TestCustomDomainAcceptance(t *testing.T) {
	ctx := context.Background()
	store := state.NewMemStore()
	app := seedApp(t, store, "shop", api.PlanPro)
	policy := gateway.NewOnDemandTLSPolicy(store, "gregale.dev", nil, nil)
	router := pgRouter{store: store, appsSuffix: ".gregale.dev"}
	const host = "api.shop.example"

	certificate := func() (bool, string) { return policy.Decide(ctx, host) }
	routes := func() bool {
		t.Helper()
		got, ok, err := router.ResolveHost(ctx, host)
		if err != nil {
			t.Fatalf("ResolveHost: %v", err)
		}
		return ok && got.ID == app.ID
	}

	if _, err := store.CreateCustomDomain(ctx, host, app.ID, "challenge-token"); err != nil {
		t.Fatal(err)
	}
	if ok, decision := certificate(); ok || decision != gateway.OnDemandTLSDenyUnverified {
		t.Fatalf("unverified domain certificate = %v %q, want %q", ok, decision, gateway.OnDemandTLSDenyUnverified)
	}
	if routes() {
		t.Fatal("unverified domain routed to the app")
	}

	if verified, err := store.MarkDomainVerifiedIfChallenge(ctx, host, "wrong-token"); err != nil || verified {
		t.Fatalf("verification with the wrong token = %v, %v", verified, err)
	}
	if verified, err := store.MarkDomainVerifiedIfChallenge(ctx, host, "challenge-token"); err != nil || !verified {
		t.Fatalf("verification = %v, %v", verified, err)
	}
	if ok, decision := certificate(); !ok || decision != gateway.OnDemandTLSAllowExact {
		t.Fatalf("verified domain certificate = %v %q, want %q", ok, decision, gateway.OnDemandTLSAllowExact)
	}
	if !routes() {
		t.Fatal("verified domain did not route to the app")
	}
	if ok, decision := policy.Decide(ctx, "api.gregale.dev"); ok || decision != gateway.OnDemandTLSDenyPlatform {
		t.Fatalf("platform host certificate = %v %q, want %q", ok, decision, gateway.OnDemandTLSDenyPlatform)
	}

	if err := store.DeleteCustomDomain(ctx, host); err != nil {
		t.Fatal(err)
	}
	if ok, decision := certificate(); ok || decision != gateway.OnDemandTLSDenyUnknown {
		t.Fatalf("removed domain certificate = %v %q, want %q", ok, decision, gateway.OnDemandTLSDenyUnknown)
	}
	if routes() {
		t.Fatal("removed domain still routed to the app")
	}
}
