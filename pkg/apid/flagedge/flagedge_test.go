package flagedge_test

import (
	"context"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/flagedge"
	"github.com/onebox-faas/faas/pkg/flags"
)

// ADR-20261124212701 §4: with edge_flags off, a publish keeps existing routes
// but cannot add or change them; on, anything publishes.
func TestPublishAllowed(t *testing.T) {
	routed := flags.Config{Flags: []flags.Flag{{Key: "pricing", Type: "variant", Default: "a",
		Variants: []flags.FlagVariant{{Key: "a"}, {Key: "b"}},
		Edge:     &flags.Edge{Routes: []flags.EdgeRoute{{Path: "/pricing", Variants: map[string]flags.EdgeAction{"b": {Rewrite: "/pricing-b"}}}}}}}}
	changed := flags.Config{Flags: []flags.Flag{{Key: "pricing", Type: "variant", Default: "a",
		Variants: []flags.FlagVariant{{Key: "a"}, {Key: "b"}},
		Edge:     &flags.Edge{Routes: []flags.EdgeRoute{{Path: "/pricing", Variants: map[string]flags.EdgeAction{"b": {Rewrite: "/pricing-c"}}}}}}}}
	plain := flags.Config{Flags: []flags.Flag{{Key: "pricing", Type: "variant", Default: "a", Variants: []flags.FlagVariant{{Key: "a"}, {Key: "b"}}}}}
	failing := func(context.Context) (flags.Config, error) { return flags.Config{}, errors.New("store down") }
	of := func(c flags.Config) func(context.Context) (flags.Config, error) {
		return func(context.Context) (flags.Config, error) { return c, nil }
	}
	tests := []struct {
		name     string
		released bool
		next     flags.Config
		current  func(context.Context) (flags.Config, error)
		want     bool
	}{
		{"released", true, changed, failing, true},
		{"no routes", false, plain, failing, true},
		{"unchanged routes", false, routed, of(routed), true},
		{"changed routes", false, changed, of(routed), false},
		{"new routes", false, routed, of(plain), false},
		{"unreadable current", false, routed, failing, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := flagedge.PublishAllowed(t.Context(), tc.released, tc.next, tc.current); got != tc.want {
				t.Fatalf("PublishAllowed = %v, want %v", got, tc.want)
			}
		})
	}
}
