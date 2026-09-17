package api

import "testing"

func TestAppVisibilityClosedSet(t *testing.T) {
	for _, tc := range []struct {
		value AppVisibility
		valid bool
	}{
		{AppVisibilityPublic, true},
		{AppVisibilityInternal, true},
		{"", false},
		{"private", false},
	} {
		if got := tc.value.Valid(); got != tc.valid {
			t.Errorf("%q.Valid()=%v, want %v", tc.value, got, tc.valid)
		}
	}
	if got := NormalizeAppVisibility(""); got != AppVisibilityPublic {
		t.Fatalf("empty visibility normalized to %q, want public", got)
	}
}
