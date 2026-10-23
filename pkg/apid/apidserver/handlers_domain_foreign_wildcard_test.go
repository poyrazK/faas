package apidserver_test

import (
	"context"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

// A verified wildcard proves control of its zone (ADR-167). Another account
// must not reserve a name below it: the reservation needs no DNS proof, yet
// while pending it shadows the wildcard's route and certificate. The
// wildcard's own account may still add exact names below it.
func TestCreateDomain_RefusesNameBelowAnotherAccountsVerifiedWildcard(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanScale)
	store := *e.Store()
	zoneOwner, err := store.CreateAccount(ctx, "zone-owner@example.com", api.PlanScale)
	if err != nil {
		t.Fatal(err)
	}
	ownerApp, err := store.CreateApp(ctx, state.App{AccountID: zoneOwner.ID, Slug: "zone-owner", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCustomDomain(ctx, "*.zone.example.com", ownerApp.ID, "tok"); err != nil {
		t.Fatal(err)
	}
	appID := apidserver.MustSeedApp(t, e, "zone-neighbour")

	// Unverified, the wildcard proves nothing and blocks nothing.
	rec := e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{Domain: "early.zone.example.com", AppID: appID}, nil)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("below a pending wildcard: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if err := store.MarkDomainVerified(ctx, "*.zone.example.com"); err != nil {
		t.Fatal(err)
	}
	for _, domain := range []string{"api.zone.example.com", "a.b.zone.example.com", "*.shop.zone.example.com"} {
		rec := e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{Domain: domain, AppID: appID}, nil)
		apidserver.AssertProblem(t, rec, http.StatusConflict, api.CodeValidation)
		if _, err := store.DomainByName(ctx, domain); err == nil {
			t.Fatalf("%s: refused create still stored a claim", domain)
		}
	}
	for _, domain := range []string{"zone.example.com", "other.example.com"} {
		rec := e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{Domain: domain, AppID: appID}, nil)
		if rec.Code != http.StatusAccepted {
			t.Fatalf("%s is outside the wildcard: status=%d body=%s", domain, rec.Code, rec.Body.String())
		}
	}
}
