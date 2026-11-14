package main_test

import (
	"bytes"
	"reflect"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Found on prod 2026-11-11: `debug captures list my-app` treated "list" as
// the slug and answered "Not found", though the help says "List, show or
// delete". A lone word stays a slug.
func TestProfileCapturesVerb(t *testing.T) {
	for _, tc := range []struct {
		in      []string
		rest    []string
		del, ok bool
	}{
		{[]string{"my-app"}, []string{"my-app"}, false, true},
		{[]string{"my-app", "cap-1"}, []string{"my-app", "cap-1"}, false, true},
		{[]string{"list"}, []string{"list"}, false, true},
		{[]string{"list", "my-app"}, []string{"my-app"}, false, true},
		{[]string{"show", "my-app", "cap-1"}, []string{"my-app", "cap-1"}, false, true},
		{[]string{"delete", "my-app", "cap-1"}, []string{"my-app", "cap-1"}, true, true},
		{[]string{"rm", "my-app", "cap-1"}, []string{"my-app", "cap-1"}, true, true},
		{[]string{"list", "my-app", "extra"}, nil, false, false},
		{[]string{"delete", "my-app"}, nil, false, false},
	} {
		rest, del, ok := main.ProfileCapturesVerb(tc.in)
		if ok != tc.ok || (ok && (!reflect.DeepEqual(rest, tc.rest) || del != tc.del)) {
			t.Errorf("%v → %v del=%t ok=%t, want %v del=%t ok=%t", tc.in, rest, del, ok, tc.rest, tc.del, tc.ok)
		}
	}
}

// Found on prod 2026-11-11: a mirror with no samples printed P95_DELTA -846,
// as though the mirror were 846 ms faster.
func TestDebugCompareDeltaNeedsBothSides(t *testing.T) {
	var out bytes.Buffer
	main.RenderDebugCompareTable(&out, api.DebugCompareResponse{Routes: []api.DebugCompareRouteStats{
		{Route: "GET /", SourceP95: 846, SourceN: 2},
		{Route: "GET /both", SourceP95: 100, SourceN: 5, MirrorP95: 150, MirrorN: 5},
	}})
	lines := strings.Split(out.String(), "\n")
	var empty, both string
	for _, line := range lines {
		switch {
		case strings.HasPrefix(line, "GET /both"):
			both = line
		case strings.HasPrefix(line, "GET /"):
			empty = line
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(empty), " -") || strings.Contains(empty, "-846") {
		t.Fatalf("row with an empty mirror = %q, want delta -", empty)
	}
	if !strings.HasSuffix(strings.TrimSpace(both), "+50") {
		t.Fatalf("row with both sides = %q, want delta +50", both)
	}
}
