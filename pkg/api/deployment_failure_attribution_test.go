package api

import "testing"

// Prod 2026-11-23: `release: nmp run migrate` exited 127 and counted as a
// platform failure; only a missing platform runtime tool should.
func TestIsCustomerCausedReleaseTask(t *testing.T) {
	code := func(v int) *int { return &v }
	for _, tc := range []struct {
		name      string
		exit      *int
		stderr    string
		timedOut  bool
		cancelled bool
		want      bool
	}{
		{"own failure", code(1), "migration failed", false, false, true},
		{"customer typo", code(127), "/bin/sh: 1: nmp: not found\n", false, false, true},
		{"customer script missing", code(127), "/bin/sh: 1: ./scripts/migrate.sh: not found\n", false, false, true},
		{"npm missing from the base", code(127), "/bin/sh: 1: npm: not found\n", false, false, false},
		{"node missing via env", code(127), "/usr/bin/env: 'node': No such file or directory\n", false, false, false},
		{"python missing", code(127), "sh: python3: command not found\n", false, false, false},
		{"tool name inside a word", code(127), "/bin/sh: 1: mynpm: not found\n", false, false, true},
		{"customer script not executable", code(126), "/bin/sh: 1: ./migrate: Permission denied\n", false, false, true},
		{"own timeout", nil, "", true, false, true},
		{"owner cancel", nil, "", false, true, true},
		{"platform cancel", nil, "", false, false, false},
	} {
		if got := IsCustomerCausedReleaseTask(tc.exit, tc.stderr, tc.timedOut, tc.cancelled); got != tc.want {
			t.Errorf("%s: IsCustomerCausedReleaseTask = %v, want %v", tc.name, got, tc.want)
		}
	}
}
