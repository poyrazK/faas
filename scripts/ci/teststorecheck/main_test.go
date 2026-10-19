package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestFindings(t *testing.T) {
	tests := []struct {
		name string
		file string
		src  string
		want []string // kind|owner|store
	}{
		{
			name: "embedded concrete stores and Store",
			file: "pkg/sched/fake_test.go",
			src: `package sched
import "github.com/onebox-faas/faas/pkg/state"
type memFake struct {
	*state.MemStore
	calls int
}
type pgFake struct{ *state.PgStore }
type storeFake struct{ state.Store }`,
			want: []string{"embed|memFake|MemStore", "embed|pgFake|PgStore", "embed|storeFake|Store"},
		},
		{
			name: "named fields are fine",
			file: "pkg/sched/fake_test.go",
			src: `package sched
import "github.com/onebox-faas/faas/pkg/state"
type memFake struct {
	MemStore *state.MemStore
	Store    state.Store
}`,
		},
		{
			name: "interfaces embedding Store, named and literal",
			file: "pkg/sched/iface_test.go",
			src: `package sched
import (
	"context"

	"github.com/onebox-faas/faas/pkg/state"
)
type fooTestStore interface {
	state.Store
	Extra(context.Context) error
}
func contract(s interface {
	state.Store
	Other() error
}) {}
type narrow interface{ AppByID(context.Context, string) (state.App, error) }`,
			want: []string{"iface|fooTestStore|Store", "iface|a type literal in contract|Store"},
		},
		{
			name: "allow marker",
			file: "pkg/sched/fake_test.go",
			src: `package sched
import "github.com/onebox-faas/faas/pkg/state"
type optionalFake struct {
	*state.MemStore // teststorecheck:allow implements state.FooStore, which statetest.Mem would hide
}
type documented struct {
	// teststorecheck:allow implements state.BarStore
	state.Store
}`,
		},
		{
			name: "renamed import",
			file: "pkg/sched/fake_test.go",
			src: `package sched
import st "github.com/onebox-faas/faas/pkg/state"
type fake struct{ *st.MemStore }`,
			want: []string{"embed|fake|MemStore"},
		},
		{
			name: "unqualified inside pkg/state",
			file: "pkg/state/x_test.go",
			src: `package state
type counting struct{ *MemStore }
type fooTestStore interface {
	Store
	Extra() error
}
type alias = contractTestStore`,
			want: []string{"embed|counting|MemStore", "iface|fooTestStore|Store"},
		},
		{
			name: "local type in a function",
			file: "pkg/sched/fake_test.go",
			src: `package sched
import "github.com/onebox-faas/faas/pkg/state"
func TestX() {
	type local struct{ *state.MemStore }
	_ = struct{ state.Store }{}
}`,
			want: []string{"embed|local|MemStore", "embed|a type literal in TestX|Store"},
		},
		{
			name: "no state import",
			file: "pkg/other/x_test.go",
			src: `package other
type Store interface{ Get() }
type fake struct{ Store }`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := findings(tc.file, []byte(tc.src))
			if err != nil {
				t.Fatal(err)
			}
			var keys []string
			for _, f := range got {
				keys = append(keys, f.key())
			}
			if !reflect.DeepEqual(keys, tc.want) {
				t.Fatalf("findings = %q, want %q", keys, tc.want)
			}
		})
	}
}

func TestNewFindingsReportsOnlyAdditions(t *testing.T) {
	f := func(owner string) finding { return finding{kind: "embed", owner: owner, store: "MemStore"} }
	base := []finding{f("kept"), f("twice"), f("removed")}
	head := []finding{f("kept"), f("twice"), f("twice"), f("added")}
	var got []string
	for _, n := range newFindings(base, head) {
		got = append(got, n.owner)
	}
	if want := []string{"twice", "added"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("new = %q, want %q", got, want)
	}
}

func TestInScope(t *testing.T) {
	for p, want := range map[string]bool{
		"pkg/sched/loop_test.go":            true,
		"pkg/state/hostingtest/fixtures.go": true,
		"pkg/state/statetest/statetest.go":  false,
		"pkg/sched/loop.go":                 false,
		"pkg/sched/testdata/fake_test.go":   false,
		"docs/notes_test.md":                false,
	} {
		if got := inScope(p); got != want {
			t.Errorf("inScope(%q) = %v, want %v", p, got, want)
		}
	}
}

func TestMessageNamesTheFix(t *testing.T) {
	embed := message(finding{kind: "embed", owner: "fake", store: "MemStore"})
	iface := message(finding{kind: "iface", owner: "fooTestStore", store: "Store"})
	if !strings.Contains(embed, "statetest.NewMem(fake)") || !strings.Contains(embed, "MemStore *state.MemStore") {
		t.Fatalf("embed message: %s", embed)
	}
	if !strings.Contains(iface, "contractTestStore") {
		t.Fatalf("iface message: %s", iface)
	}
}
