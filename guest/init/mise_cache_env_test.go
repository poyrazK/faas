package main

import (
	"reflect"
	"testing"
)

// Prod hunt r28: a Railpack pnpm app printed "mise WARN failed to write cache
// file: /mise/cache/…: Permission denied" on every start; Railpack's image env
// points MISE_CACHE_DIR at a root-owned directory.
func TestStampMiseCacheEnv(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   []string
		want []string
	}{
		{"railpack default moves under HOME",
			[]string{"MISE_CACHE_DIR=/mise/cache", "HOME=/tmp"},
			[]string{"MISE_CACHE_DIR=/mise/cache", "HOME=/tmp", "MISE_CACHE_DIR=/tmp/.cache/mise"}},
		{"customer choice kept",
			[]string{"MISE_CACHE_DIR=/data/mise", "HOME=/tmp"},
			[]string{"MISE_CACHE_DIR=/data/mise", "HOME=/tmp"}},
		{"no mise", []string{"HOME=/tmp"}, []string{"HOME=/tmp"}},
		{"no writable home", []string{"MISE_CACHE_DIR=/mise/cache", "HOME=/"}, []string{"MISE_CACHE_DIR=/mise/cache", "HOME=/"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := StampMiseCacheEnv(append([]string(nil), tc.in...)); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}
