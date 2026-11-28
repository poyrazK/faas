// Copyright 2026 Gregale. Local patch to Coraza v3.8.1 (see ../../PATCHES.md).
// SPDX-License-Identifier: Apache-2.0

package collections

import (
	"sort"
	"testing"

	"github.com/corazawaf/coraza/v3/types"
	"github.com/corazawaf/coraza/v3/types/variables"
)

func pairs(md []types.MatchData) []string {
	out := make([]string, 0, len(md))
	for _, m := range md {
		out = append(out, m.Variable().Name()+":"+m.Key()+"="+m.Value())
	}
	sort.Strings(out)
	return out
}

func equal(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMapFindAllCacheFollowsWrites(t *testing.T) {
	for name, tc := range map[string]struct {
		write func(m *Map)
		want  []string
	}{
		"add":       {func(m *Map) { m.Add("c", "3") }, []string{"ARGS_GET:a=1", "ARGS_GET:b=2", "ARGS_GET:c=3"}},
		"set":       {func(m *Map) { m.Set("a", []string{"9"}) }, []string{"ARGS_GET:a=9", "ARGS_GET:b=2"}},
		"set index": {func(m *Map) { m.SetIndex("b", 0, "8") }, []string{"ARGS_GET:a=1", "ARGS_GET:b=8"}},
		"remove":    {func(m *Map) { m.Remove("a") }, []string{"ARGS_GET:b=2"}},
		"reset":     {func(m *Map) { m.Reset() }, []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			m := NewMap(variables.ArgsGet)
			m.Add("a", "1")
			m.Add("b", "2")
			if got := pairs(m.FindAll()); !equal(got, []string{"ARGS_GET:a=1", "ARGS_GET:b=2"}) {
				t.Fatalf("before write: %v", got)
			}
			tc.write(m)
			if got := pairs(m.FindAll()); !equal(got, tc.want) {
				t.Fatalf("after write: got %v, want %v", got, tc.want)
			}
		})
	}
}

// GetField filters the slice FindAll returns in place; that must not change
// what the next rule sees.
func TestMapFindAllReturnsCallerOwnedSlice(t *testing.T) {
	m := NewMap(variables.ArgsGet)
	m.Add("a", "1")
	m.Add("b", "2")
	first := m.FindAll()
	first[0] = first[1]
	first = first[:1]
	_ = first
	if got := pairs(m.FindAll()); !equal(got, []string{"ARGS_GET:a=1", "ARGS_GET:b=2"}) {
		t.Fatalf("cached matches changed by a caller: %v", got)
	}
}

func TestNamesFindAllCacheFollowsWrites(t *testing.T) {
	c := NewNamedCollection(variables.ArgsPost)
	names := c.Names(variables.ArgsPostNames)
	c.Add("a", "1")
	if got := pairs(names.FindAll()); !equal(got, []string{"ARGS_POST_NAMES:a=a"}) {
		t.Fatalf("before write: %v", got)
	}
	c.Add("b", "2")
	if got := pairs(names.FindAll()); !equal(got, []string{"ARGS_POST_NAMES:a=a", "ARGS_POST_NAMES:b=b"}) {
		t.Fatalf("after write: %v", got)
	}
}

// ARGS concatenates ARGS_GET, ARGS_POST and ARGS_PATH and renames their
// matches. The parts cache their own matches, so renaming must not reach them.
func TestConcatKeyedDoesNotRenameParts(t *testing.T) {
	get := NewNamedCollection(variables.ArgsGet)
	post := NewNamedCollection(variables.ArgsPost)
	args := NewConcatKeyed(variables.Args, get, post)
	get.Add("q", "1")
	post.Add("p", "2")

	if got := pairs(args.FindAll()); !equal(got, []string{"ARGS:p=2", "ARGS:q=1"}) {
		t.Fatalf("ARGS: %v", got)
	}
	_ = args.FindString("")
	if got := pairs(get.FindAll()); !equal(got, []string{"ARGS_GET:q=1"}) {
		t.Fatalf("ARGS_GET renamed by ARGS: %v", got)
	}
	if got := pairs(post.FindAll()); !equal(got, []string{"ARGS_POST:p=2"}) {
		t.Fatalf("ARGS_POST renamed by ARGS: %v", got)
	}

	// The body is parsed after the query: ARGS must see the new field.
	post.Add("r", "3")
	if got := pairs(args.FindAll()); !equal(got, []string{"ARGS:p=2", "ARGS:q=1", "ARGS:r=3"}) {
		t.Fatalf("ARGS after body: %v", got)
	}
}
