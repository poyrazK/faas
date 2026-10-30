// adr: 20261029190349
package state

import (
	"slices"
	"testing"
)

func TestTrafficScopeLookup(t *testing.T) {
	const suffix = ".apps.example.test"
	alias := "tag-blue-0123456789abcdef0123456789abcdef" + suffix
	lookup := newTrafficScopeLookup(trafficBindingScope{
		Hosts:    []string{"shop.example.net", "web" + suffix, alias},
		Subtrees: []string{"example.org"},
	}, suffix)
	wantNames := []string{"*.apps.example.test", "*.example.net", "*.example.test", "*.net", "*.org", "*.test", "shop.example.net", alias, "web" + suffix}
	if !slices.Equal(lookup.names, wantNames) {
		t.Fatalf("names %v, want %v", lookup.names, wantNames)
	}
	if want := []string{"gro.elpmaxe."}; !slices.Equal(lookup.trees, want) {
		t.Fatalf("trees %v, want %v", lookup.trees, want)
	}
	if want := []string{"tag-blue-0123456789abcdef0123456789abcdef", "web"}; !slices.Equal(lookup.slugs, want) {
		t.Fatalf("slugs %v, want %v", lookup.slugs, want)
	}
	if len(lookup.aliasApps) != 1 || lookup.aliasApps[0].String() != "01234567-89ab-cdef-0123-456789abcdef" {
		t.Fatalf("alias apps %v", lookup.aliasApps)
	}
	if lookup.allAppHosts || lookup.fleet {
		t.Fatalf("narrow scope widened: %+v", lookup)
	}
	for _, tc := range []struct {
		tree       string
		allApps    bool
		everywhere bool
	}{
		{"apps.example.test", true, false},
		{"example.test", true, false},
		{"other.example.test", false, false},
		{"", false, true},
	} {
		lookup := newTrafficScopeLookup(trafficBindingScope{Subtrees: []string{tc.tree}}, suffix)
		if lookup.allAppHosts != tc.allApps || lookup.fleet != tc.everywhere {
			t.Fatalf("subtree %q: all apps %v fleet %v", tc.tree, lookup.allAppHosts, lookup.fleet)
		}
	}
}

func TestTrafficAliasHostApp(t *testing.T) {
	for _, label := range []string{"tag-x", "web-0123456789abcdef0123456789abcdef", "tag-0123456789abcdef0123456789abcdef", "tag-x-0123456789abcdef0123456789abcdeg"} {
		if _, ok := trafficAliasHostApp(label); ok {
			t.Fatalf("%s parsed as an alias", label)
		}
	}
}
