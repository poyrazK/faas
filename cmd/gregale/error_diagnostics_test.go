package main

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// The fallback hint must match the failure. Hunt #9: `gregale run` on an
// installation without the execution runtime got 501 and the generic 5xx
// advice to check service status and deployment state, which cannot help.
func TestDiagnosticProblemFallbackHints(t *testing.T) {
	for _, tc := range []struct {
		status int
		want   string
	}{
		{status: 403, want: "credential permissions"},
		{status: 429, want: "Retry-After"},
		{status: 501, want: "not enabled on this Gregale installation"},
		{status: 503, want: "service status"},
	} {
		got := diagnosticProblem(api.Problem{Status: tc.status, Title: "x"})
		if !strings.Contains(got.Hint, tc.want) {
			t.Errorf("status %d hint = %q, want it to mention %q", tc.status, got.Hint, tc.want)
		}
	}
	server := diagnosticProblem(api.Problem{Status: 501, Hint: "server hint"})
	if server.Hint != "server hint" {
		t.Errorf("a server-supplied hint was replaced: %q", server.Hint)
	}
}
