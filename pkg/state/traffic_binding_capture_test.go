// adr: 20261029190349
package state

import (
	"slices"
	"testing"
)

func TestTrafficBindingCaptureGaps(t *testing.T) {
	changes := []trafficBindingChange{
		{host: "a.example.org", kind: TrafficBindingScopeKindDomain},
		{host: "example.org", kind: TrafficBindingScopeKindDomain, subtree: true},
		{host: "b.example.org", kind: TrafficBindingScopeKindReservationTenant, added: true},
	}
	for _, tc := range []struct {
		name     string
		captured []trafficBindingChange
		want     []string
	}{
		{"everything recorded", []trafficBindingChange{{host: "a.example.org"}, {host: "example.org", subtree: true}, {host: "b.example.org", added: true}}, nil},
		{"exact host is not its subtree", []trafficBindingChange{{host: "a.example.org"}, {host: "example.org"}, {host: "b.example.org"}},
			[]string{TrafficBindingScopeKindDomain}},
		{"nothing recorded", nil, []string{TrafficBindingScopeKindDomain, TrafficBindingScopeKindDomain, TrafficBindingScopeKindReservationTenant}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trafficBindingCaptureGaps(changes, tc.captured); !slices.Equal(got, tc.want) {
				t.Fatalf("gaps = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestTrafficCapturedHostNormalizes(t *testing.T) {
	for _, tc := range []struct {
		host    string
		want    trafficBindingChange
		addedIn bool
	}{
		{"Shop.Example.ORG.", trafficBindingChange{host: "shop.example.org", kind: TrafficBindingScopeKindDomain, added: true}, true},
		{"*.Example.org", trafficBindingChange{host: "example.org", kind: TrafficBindingScopeKindDomain, subtree: true}, false},
	} {
		if got := trafficCapturedHost(TrafficBindingScopeKindDomain, tc.host, tc.addedIn); got != tc.want {
			t.Fatalf("%s: got %+v, want %+v", tc.host, got, tc.want)
		}
	}
}
