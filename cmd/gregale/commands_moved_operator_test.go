package main_test

import (
	"path/filepath"
	"strings"
	"testing"
)

// Operator commands that moved to gregalectl answer with their new home and
// never reach the API.
func TestMovedOperatorCommandsPointToGregalectl(t *testing.T) {
	t.Setenv("FAAS_API", "http://127.0.0.1:1")
	t.Setenv("FAAS_TOKEN", "test-token")
	t.Setenv("FAAS_COMPLETION_CACHE_PATH", filepath.Join(t.TempDir(), "cache.json"))
	for _, tc := range []struct{ args, want string }{
		{"admin credit --reason x 11111111-1111-4111-8111-111111111111 5", "gregalectl billing credit"},
		{"admin abuse-hold place --note x id", "gregalectl accounts abuse-hold"},
		{"github-webhook-secret set --installation-id 1", "gregalectl github webhook-secret"},
		{"postgres usage-import acct --file f.json", "gregalectl billing postgres usage-import"},
		{"admin postgres diagnostics acct", "gregalectl billing postgres diagnostics"},
		{"billing price-catalog list", "gregalectl billing price-catalog"},
		{"billing webhook-test stripe", "gregalectl billing webhook-test"},
	} {
		code, _, stderr := runCLI(t, strings.Fields(tc.args)...)
		if code == 0 || !strings.Contains(stderr, tc.want) {
			t.Errorf("%q = %d %q, want a failure naming %q", tc.args, code, stderr, tc.want)
		}
	}
}
