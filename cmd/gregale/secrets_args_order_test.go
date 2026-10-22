package main_test

import (
	"github.com/onebox-faas/faas/cmd/gregale"
	"slices"
	"testing"
)

// H10-85: an unknown flag after the keys stayed among them, so
// `secrets unset KEY --yes` failed with "Invalid secret key" instead of
// naming --yes. Flag-shaped arguments go to the flag parser wherever they sit.
func TestReorderSecretsSetArgsSendsUnknownFlagsToParser(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want []string
	}{
		{"unknown flag after keys", []string{"A_KEY", "--app", "web", "--yes"}, []string{"--app", "web", "--yes", "A_KEY"}},
		{"single-dash unknown flag", []string{"A_KEY=1", "-force"}, []string{"-force", "A_KEY=1"}},
		{"after -- stays a key", []string{"--app", "web", "--", "--yes"}, []string{"--app", "web", "--yes"}},
		{"value starting with dash", []string{"A_KEY=-1"}, []string{"A_KEY=-1"}},
		{"bare dash", []string{"-"}, []string{"-"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := main.ReorderSecretsSetArgs(tc.args)
			if err != nil {
				t.Fatalf("reorderSecretsSetArgs: %v", err)
			}
			if !slices.Equal(got, tc.want) {
				t.Fatalf("reorderSecretsSetArgs(%q) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}
