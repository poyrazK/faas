package main_test

import (
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// `debug requests frontend` lists, as `debug requests list frontend` does; a
// typo'd subcommand followed by an app is still an unknown subcommand.
func TestDebugRequestsBareSlugLists(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"frontend"}, true},
		{[]string{"frontend", "--status", "500"}, true},
		{[]string{"--app", "frontend"}, true},
		{[]string{"lst", "frontend"}, false},
		{nil, false},
	} {
		if got := main.DebugRequestsImpliesList(tc.args); got != tc.want {
			t.Errorf("DebugRequestsImpliesList(%q) = %v, want %v", tc.args, got, tc.want)
		}
	}
}
