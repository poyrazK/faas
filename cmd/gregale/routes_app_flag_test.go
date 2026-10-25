package main_test

import (
	"slices"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// Production-us rc.294: `gregale routes status --app h14-py` answered
// "unknown flag --app" (Hunt 14 H14-17).
func TestRoutesTakeTheAppFlag(t *testing.T) {
	for _, tc := range []struct {
		args, want []string
	}{
		{[]string{"--app", "h14-py", "--since", "7d"}, []string{"h14-py", "--since", "7d"}},
		{[]string{"--since", "7d", "--app=h14-py"}, []string{"h14-py", "--since", "7d"}},
		{[]string{"h14-py", "--app", "h14-py"}, []string{"h14-py"}},
		{[]string{"h14-py", "--since", "7d"}, []string{"h14-py", "--since", "7d"}},
	} {
		if got := main.RoutesAppFlagAsPositional(tc.args); !slices.Equal(got, tc.want) {
			t.Errorf("routesAppFlagAsPositional(%q) = %q, want %q", tc.args, got, tc.want)
		}
	}
}
