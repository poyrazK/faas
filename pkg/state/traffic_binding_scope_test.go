// adr: 20261027163626
package state

import (
	"reflect"
	"testing"
)

func TestTrafficBindingChanges(t *testing.T) {
	base := trafficBindingClaims{
		Domains: []trafficDomainClaim{{Domain: "shop.example.com", App: "a", Account: "x", Eligible: true}, {Domain: "*.example.org", App: "a", Account: "x"}},
		Tenants: []trafficTenantClaim{{trafficHostTenant: trafficHostTenant{Host: "t.example.net"}, Account: "x", Verified: true}},
		Aliases: []trafficNamedHostClaim{{Host: "tag-a-1.apps.test", App: "a", Account: "x"}},
	}
	reservations := []trafficHostReservation{{Kind: "primary", Host: "web.apps.test"}, {Kind: "domain", Host: "shop.example.com"}}
	changed := base
	changed.Domains = []trafficDomainClaim{{Domain: "Shop.Example.com", App: "a", Account: "x"}, {Domain: "*.example.org", App: "b", Account: "x"}}
	changed.Tenants = nil
	for _, tc := range []struct {
		name              string
		after             trafficBindingClaims
		afterReservations []trafficHostReservation
		want              []trafficBindingChange
	}{
		{name: "unchanged", after: base, afterReservations: reservations},
		{name: "claims", after: changed, afterReservations: reservations, want: []trafficBindingChange{
			{host: "example.org", kind: TrafficBindingScopeKindDomain, subtree: true},
			{host: "shop.example.com", kind: TrafficBindingScopeKindDomain},
			{host: "t.example.net", kind: TrafficBindingScopeKindTenant},
		}},
		{name: "reservations", after: base, afterReservations: []trafficHostReservation{{Kind: "primary", Host: "web.apps.test"}, {Kind: "primary", Host: "new.apps.test"}}, want: []trafficBindingChange{
			{host: "shop.example.com", kind: TrafficBindingScopeKindReservationDomain},
			{host: "new.apps.test", kind: TrafficBindingScopeKindReservationPrimary, added: true},
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trafficBindingChanges(base, tc.after, reservations, tc.afterReservations); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("changes = %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

func TestTrafficBindingScopeCovers(t *testing.T) {
	scope := trafficBindingScope{Hosts: []string{"shop.example.com"}, Subtrees: []string{"example.org"}}
	for _, tc := range []struct {
		host    string
		subtree bool
		want    bool
	}{
		{"shop.example.com", false, true},
		{"www.example.com", false, false},
		{"a.example.org", false, true},
		{"example.org", false, false}, // the apex is not inside its subtree
		{"example.org", true, true},
		{"api.example.org", true, true},
		{"example.com", true, false},
	} {
		if got := scope.covers(tc.host, tc.subtree); got != tc.want {
			t.Errorf("covers(%q, subtree=%v) = %v, want %v", tc.host, tc.subtree, got, tc.want)
		}
	}
	if !(trafficBindingScope{Subtrees: []string{""}}).covers("anything.test", false) {
		t.Error("the every-hostname subtree must cover any host")
	}
	var missed []string
	SetTrafficBindingScopeObserver(func(_ string, kinds []string) { missed = kinds })
	t.Cleanup(func() { SetTrafficBindingScopeObserver(nil) })
	auditTrafficBindingScope("app_binding", scope, []trafficBindingChange{
		{host: "shop.example.com", kind: "domain"},
		{host: "web.apps.test", kind: "reservation_primary"},
		{host: "new.apps.test", kind: "reservation_primary", added: true}, // a new exact claim
		{host: "example.net", kind: "domain", subtree: true, added: true}, // a new wildcard
	})
	if !reflect.DeepEqual(missed, []string{"reservation_primary", "domain"}) {
		t.Fatalf("missed = %v", missed)
	}
}
