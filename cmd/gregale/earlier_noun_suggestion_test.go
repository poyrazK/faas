package main_test

import (
	"strings"
	"testing"
)

// prod hunt 2026-11-21: `gregale secret` printed no suggestion and `secrt`
// suggested `start`, because `secrets` is an earlier name the suggester
// skips. A typo of an earlier noun now suggests the tree noun that replaced
// it; the earlier name itself is still never suggested.
func TestUnknownCommandSuggestsTreeNounForEarlierNounTypo(t *testing.T) {
	for _, tc := range []struct{ typed, want string }{
		{"secret", "did you mean 'gregale env'?"},
		{"log", "did you mean 'gregale logs'?"},
		{"domian", "did you mean 'gregale domain'?"},
	} {
		code, _, stderr := runCLI(t, tc.typed)
		if code == 0 {
			t.Fatalf("%s: exit 0", tc.typed)
		}
		if !strings.Contains(stderr, tc.want) {
			t.Errorf("%s: stderr %q, want %q", tc.typed, stderr, tc.want)
		}
		if strings.Contains(stderr, "'gregale secrets'") {
			t.Errorf("%s: suggested the earlier name: %q", tc.typed, stderr)
		}
	}
}
