// adr: 20261103163954
package state

import (
	"context"
	"slices"
	"testing"
	"time"
)

func TestMemKnownRouteHostsListsSlugsAndVerifiedDomains(t *testing.T) {
	m := NewMemStore()
	m.mu.Lock()
	m.apps["app-1"] = App{ID: "app-1", Slug: "pulse"}
	m.apps["app-2"] = App{ID: "app-2", Slug: "shop"}
	m.domains["www.example.com"] = CustomDomain{Domain: "www.example.com", AppID: "app-1", VerifiedAt: time.Unix(1_800_000_000, 0)}
	m.domains["pending.example.com"] = CustomDomain{Domain: "pending.example.com", AppID: "app-2"}
	m.domains["*.wild.example.com"] = CustomDomain{Domain: "*.wild.example.com", AppID: "app-1", VerifiedAt: time.Unix(1_800_000_000, 0)}
	m.customDomainTLSHosts = map[string]customDomainTLSHost{"a.wild.example.com": {wildcardDomain: "*.wild.example.com"}}
	m.mu.Unlock()

	slugs, domains, err := m.KnownRouteHosts(context.Background(), 10)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(slugs)
	slices.Sort(domains)
	if !slices.Equal(slugs, []string{"pulse", "shop"}) {
		t.Fatalf("slugs = %v, want [pulse shop]", slugs)
	}
	if !slices.Equal(domains, []string{"*.wild.example.com", "a.wild.example.com", "www.example.com"}) {
		t.Fatalf("domains = %v, want verified claims and admitted concrete wildcard host", domains)
	}
	if slugs, _, _ := m.KnownRouteHosts(context.Background(), 1); len(slugs) != 1 {
		t.Fatalf("limit 1 returned %d slugs", len(slugs))
	}
}
