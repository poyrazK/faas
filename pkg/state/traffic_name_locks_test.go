// adr: 20261027163626
package state

import (
	"reflect"
	"testing"
)

func TestTrafficNameLocks(t *testing.T) {
	root := trafficSessionLock{Key: globalTrafficRoutesLock, Shared: true}
	tree := func(name string, shared bool) trafficSessionLock {
		return trafficSessionLock{Key: trafficTreeLockPrefix + name, Shared: shared}
	}
	host := func(name string) trafficSessionLock { return trafficSessionLock{Key: trafficHostLockPrefix + name} }
	for _, tc := range []struct {
		name            string
		hosts, subtrees []string
		want            []trafficSessionLock
	}{
		{name: "nothing", want: []trafficSessionLock{root}},
		{name: "host", hosts: []string{"Api.Example.com."},
			want: []trafficSessionLock{root, tree("com", true), tree("example.com", true), host("api.example.com")}},
		{name: "subtree", subtrees: []string{"apps.gregale.dev"},
			want: []trafficSessionLock{root, tree("dev", true), tree("gregale.dev", true), tree("apps.gregale.dev", false)}},
		{name: "every hostname", subtrees: []string{""},
			want: []trafficSessionLock{{Key: globalTrafficRoutesLock}}},
		{name: "exclusive wins over an ancestor's shared key", hosts: []string{"a.example.com"}, subtrees: []string{"example.com"},
			want: []trafficSessionLock{root, tree("com", true), tree("example.com", false), host("a.example.com")}},
		{name: "duplicates and blanks", hosts: []string{"x.test", "X.TEST", " "},
			want: []trafficSessionLock{root, tree("test", true), host("x.test")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := trafficNameLocks(tc.hosts, tc.subtrees); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("locks = %+v\nwant %+v", got, tc.want)
			}
		})
	}
}
