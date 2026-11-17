package main_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// GREG-06: a logged-in CLI that could not read its account's capability
// view (API out of reach, no cache) fell back to the tier defaults and
// answered "unknown command" for `gregale app exec <app> --help`, a preview
// command the account could run and `gregale guide` recommends. Help for a
// named command is shown in that case; listings stay on the tier defaults.
func TestUnconfirmedViewStillShowsHelpForNamedPreviewCommand(t *testing.T) {
	clearCapabilityReleaseAll(t)
	t.Setenv("FAAS_TOKEN", "fp_live_unreachable")
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))

	for _, args := range [][]string{
		{"app", "exec", "--help"},
		{"app", "exec", "retail-demo-orders", "--help"},
		{"app", "retail-demo-orders", "exec", "--help"},
		{"help", "app", "exec"},
	} {
		code, stdout, stderr := runCLI(t, args...)
		if code != 0 || !strings.Contains(stdout, "fresh VM of the live deployment") || strings.Contains(stderr, "unknown command") {
			t.Errorf("%s = %d\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), code, stdout, stderr)
		}
	}
	if _, help, _ := runCLI(t, "app", "--help"); strings.Contains(help, "\n  exec ") {
		t.Errorf("app --help lists the tier-hidden exec verb:\n%s", help)
	}
}
