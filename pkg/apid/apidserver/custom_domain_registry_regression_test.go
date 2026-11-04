package apidserver_test

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/apid/domainhosts"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

func TestCustomDomainWildcardPublicationRechecksTenantOverlap(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := apidserver.MustSeedApp(t, e, "domain-overlap-app")
	other := domainRecoveryApp(t, store, "domain-tenant-next@example.com", "domain-tenant-next")
	const zone = "*.overlap.domains.example.com"
	const hostname = "tenant.overlap.domains.example.com"
	var inserted bool
	fake := &verifyWildcardBeforeClaimStore{MemStore: store, before: func() {
		if inserted {
			return
		}
		inserted = true
		limits, _ := api.LimitsFor(api.PlanPro)
		surface, err := store.CreateTenantSurfaceIfUnderQuota(t.Context(), state.CreateTenantSurfaceParams{AccountID: other.AccountID, AppID: other.ID, Name: "another-customer"}, limits)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = store.CreateTenantHostnameIfUnderQuota(t.Context(), state.CreateTenantHostnameParams{SurfaceID: surface.ID, Hostname: hostname, ChallengeToken: "tenant-proof"}, limits); err != nil {
			t.Fatal(err)
		}
	}}
	*(*e.S()).Store() = statetest.NewMem(fake)
	response := e.Do(t, http.MethodPost, "/v1/domains", api.CreateCustomDomainRequest{Domain: zone, AppID: app}, nil)
	if !inserted {
		t.Fatalf("publication seam not reached: %d %s", response.Code, response.Body)
	}
	hosts, err := store.ListTenantSurfaceHostnames(t.Context())
	if err != nil || !slices.Contains(hosts, hostname) {
		t.Fatalf("hostname seam failed: %v %v", hosts, err)
	}
	if response.Code != http.StatusConflict {
		t.Fatalf("wildcard overlaps another customer's reserved tenant hostname after preflight: %d %s", response.Code, response.Body)
	}
}

type domainAllowlistInterleavingStore struct {
	mem    *state.MemStore
	before func()
}

func (s *domainAllowlistInterleavingStore) CustomDomainTLSAllowedHosts(ctx context.Context, name string) ([]string, error) {
	s.before()
	return s.mem.CustomDomainTLSAllowedHosts(ctx, name)
}
func (s *domainAllowlistInterleavingStore) SetCustomDomainTLSAllowedHosts(ctx context.Context, name string, names []string) error {
	return s.mem.SetCustomDomainTLSAllowedHosts(ctx, name, names)
}
func (s *domainAllowlistInterleavingStore) SetCustomDomainTLSAllowedHostsForClaim(ctx context.Context, claim state.CustomDomain, names []string) error {
	return s.mem.SetCustomDomainTLSAllowedHostsForClaim(ctx, claim, names)
}

func TestCustomDomainAllowlistReadRejectsReplacementClaim(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	oldApp := apidserver.MustSeedApp(t, e, "domain-allowlist-old")
	next := domainRecoveryApp(t, store, "domain-allowlist-next@example.com", "domain-allowlist-next")
	const wildcard = "*.allowlist.domains.example.com"
	old := domainRecoveryVerified(t, store, wildcard, oldApp, "old-proof")
	fake := &domainAllowlistInterleavingStore{mem: store, before: func() {
		if err := store.DeleteCustomDomain(t.Context(), wildcard); err != nil {
			t.Fatal(err)
		}
		domainRecoveryVerified(t, store, wildcard, next.ID, "next-proof")
		if err := store.SetCustomDomainTLSAllowedHosts(t.Context(), wildcard, []string{"private-next-owner-host"}); err != nil {
			t.Fatal(err)
		}
	}}
	out, problem := domainhosts.Read(t.Context(), fake, old)
	if problem == nil || problem.Status != http.StatusNotFound || slices.Contains(out.Names, "private-next-owner-host") {
		t.Fatalf("old owner's read exposes replacement claim's private allowlist: %+v", out)
	}
}
