package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

// claimStore serves one exact custom-domain row for host: an unverified
// claim that expires at expires, made by another account's app.
type claimStore struct {
	MemStore *state.MemStore
	host     string
	appID    string
	expires  time.Time
}

func (s *claimStore) DomainByName(ctx context.Context, domain string) (state.CustomDomain, error) {
	if domain == s.host {
		return state.CustomDomain{Domain: domain, AppID: s.appID, ChallengeToken: "squat",
			CertStatus: state.CustomDomainCertPending, VerificationExpiresAt: s.expires}, nil
	}
	return s.MemStore.DomainByName(ctx, domain)
}

// Any account can create an exact claim below another account's verified
// wildcard with no DNS proof, and nothing deletes it when it expires. While
// pending it shadows the wildcard (ADR-167); once expired it reserves nothing
// and must not keep the wildcard owner's host dark.
func TestPgRouter_ExpiredExactClaimDoesNotShadowWildcard(t *testing.T) {
	ctx := context.Background()
	mem := state.NewMemStore()
	owner := seedApp(t, mem, "zone-owner", api.PlanPro)
	squatter := seedApp(t, mem, "zone-squatter", api.PlanPro)
	if _, err := mem.CreateCustomDomain(ctx, "*.example.com", owner.ID, "tok"); err != nil {
		t.Fatal(err)
	}
	if err := mem.MarkDomainVerified(ctx, "*.example.com"); err != nil {
		t.Fatal(err)
	}
	claim := &claimStore{MemStore: mem, host: "api.example.com", appID: squatter.ID, expires: time.Now().Add(time.Hour)}
	r := pgRouter{store: statetest.NewMem(claim), appsSuffix: ".apps.gregale.dev"}

	if _, ok, err := r.ResolveHost(ctx, "api.example.com"); err != nil || ok {
		t.Fatalf("live pending claim: ok=%v err=%v, want it to shadow the wildcard", ok, err)
	}

	claim.expires = time.Now().Add(-time.Minute)
	got, ok, err := r.ResolveHost(ctx, "api.example.com")
	if err != nil || !ok || got.ID != owner.ID {
		t.Fatalf("expired claim: resolve = %+v, ok=%v, err=%v; want the wildcard owner's app", got, ok, err)
	}
	if active, err := r.CachedCustomDomainRouteActive(ctx, "api.example.com", owner.ID); err != nil || !active {
		t.Fatalf("expired claim: cached route active=%v err=%v, want the wildcard route active", active, err)
	}
}
