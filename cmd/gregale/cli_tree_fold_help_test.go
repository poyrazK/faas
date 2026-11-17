package main_test

import (
	"strings"
	"testing"
)

// The tree help for a fold's path points at `<path> --<flag> <V> --help`
// for the flags the fold adds; that help must be the folded command's.
func TestFoldFlagHelpShowsTheFoldedCommand(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"realtime", "send", "--principal", "user-1", "--help"}, "--notification-category"},
		{[]string{"help", "realtime", "send", "--principal", "user-1"}, "--notification-category"},
	} {
		code, stdout, stderr := runCLI(t, tc.args...)
		if code != 0 || !strings.Contains(stdout, tc.want) {
			t.Errorf("%q: exit %d, want help naming %q\nstdout:\n%s\nstderr:\n%s", tc.args, code, tc.want, stdout, stderr)
		}
	}
	// Without the fold flag the path keeps its own help.
	if _, stdout, _ := runCLI(t, "realtime", "send", "--help"); strings.Contains(stdout, "--notification-category") {
		t.Errorf("realtime send --help lists the folded command's flags:\n%s", stdout)
	}
}
