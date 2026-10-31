package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

// `gregale deploy --non-interactive` failed with a bare "unknown flag
// --non-interactive"; the flag is a prefix (Hunt 12 H12-27).
func TestGlobalFlagAfterCommandExplainsPlacement(t *testing.T) {
	main.SetupConnectionProfiles(t)
	main.SetFakeKeyring(t)
	main.ResetJSONOut(t)
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"deploy", "--non-interactive"}, "--non-interactive is a global flag: put it before the command, as in 'gregale --non-interactive deploy'"},
		{[]string{"logs", "--profile", "staging"}, "--profile is a global flag: put it before the command, as in 'gregale --profile NAME logs'"},
		{[]string{"deploy", "--naem", "demo"}, "Did you mean --name?"},
	}
	for _, tt := range tests {
		_, stderr, restore := main.SwapIO(t)
		code := main.Run(tt.args)
		restore()
		if got := stderr(); code == 0 || !strings.Contains(got, tt.want) {
			t.Errorf("run(%v) exit=%d, want %q in stderr: %q", tt.args, code, tt.want, got)
		}
	}
}
