package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"testing"
)

// The CLI names the restart exactly when vmmd cannot patch running
// instances in place (H10-36).
func TestEgressAllowlistChangeNeedsRestart(t *testing.T) {
	for _, tc := range []struct {
		name          string
		before, after []string
		want          bool
	}{
		{"first entry", nil, []string{"1.1.1.1/32"}, true},
		{"clear", []string{"1.1.1.1/32"}, nil, true},
		{"edit within v4", []string{"1.1.1.1/32"}, []string{"8.8.8.0/24"}, false},
		{"add another v4", []string{"1.1.1.1/32"}, []string{"1.1.1.1/32", "8.8.8.0/24"}, false},
		{"v6 gains its first entry", []string{"1.1.1.1/32"}, []string{"1.1.1.1/32", "2001:db8::/32"}, true},
		{"empty stays empty", nil, nil, false},
	} {
		if got := main.EgressAllowlistChangeNeedsRestart(tc.before, tc.after); got != tc.want {
			t.Errorf("%s: needs restart = %t, want %t", tc.name, got, tc.want)
		}
	}
}
