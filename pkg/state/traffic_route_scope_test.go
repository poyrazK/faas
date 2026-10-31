// adr: 20261027163626
package state

import (
	"reflect"
	"testing"
)

func TestNewTrafficRouteScope(t *testing.T) {
	for _, tc := range []struct {
		name            string
		hosts, subtrees []string
		want            *trafficRouteScope
	}{
		{name: "nothing", want: &trafficRouteScope{Names: []string{}, Trees: []string{}}},
		{name: "host and its ancestors", hosts: []string{"Api.Example.com."},
			want: &trafficRouteScope{Names: []string{"api.example.com", "com", "example.com"}, Trees: []string{}}},
		{name: "subtree", hosts: []string{"x.example.com"}, subtrees: []string{"apps.example.test"},
			want: &trafficRouteScope{Names: []string{"apps.example.test", "com", "example.com", "example.test", "test", "x.example.com"},
				Trees: []string{"apps.example.test"}}},
		{name: "every hostname reads every route", hosts: []string{"x.test"}, subtrees: []string{""}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := newTrafficRouteScope(tc.hosts, tc.subtrees); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("scope = %+v, want %+v", got, tc.want)
			}
		})
	}
}
