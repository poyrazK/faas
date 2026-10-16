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
	// H9-134: a sync invoke past apid's wait answered 504 long_poll_timeout
	// and got the outage hint, although the invocation was still running and
	// a retry would run it twice.
	for _, tc := range []struct {
		status int
		code   string
		want   string
	}{
		{status: 403, want: "credential permissions"},
		{status: 429, want: "Retry-After"},
		{status: 501, want: "not enabled on this Gregale installation"},
		{status: 503, want: "service status"},
		{status: 504, code: "long_poll_timeout", want: "instead of retrying"},
		// Hunt #9 (H9-M106): tenant surfaces answer 503
		// tenant_surfaces_not_enabled; the outage hint told users to check
		// service and deployment status.
		{status: 503, code: api.CodeTenantSurfacesNotEnabled, want: "not enabled on this Gregale installation"},
		{status: 503, code: "object_storage_unavailable", want: "service status"},
	} {
		got := diagnosticProblem(api.Problem{Status: tc.status, Code: tc.code, Title: "x"})
		if !strings.Contains(got.Hint, tc.want) {
			t.Errorf("status %d code %q hint = %q, want it to mention %q", tc.status, tc.code, got.Hint, tc.want)
		}
	}
	server := diagnosticProblem(api.Problem{Status: 501, Hint: "server hint"})
	if server.Hint != "server hint" {
		t.Errorf("a server-supplied hint was replaced: %q", server.Hint)
	}
}

// A policy 403 (here an alert webhook resolving to a private address) must
// not be told to check credentials; an authorization 403 still is (H10-73).
func TestDiagnosticProblemCredentialHintOnlyForAuthorization(t *testing.T) {
	policy := diagnosticProblem(api.Problem{Status: 403, Code: api.CodeImageEgressDenied, Detail: "webhook_url resolves to 10.0.0.5"})
	if strings.Contains(policy.Hint, "profile check") {
		t.Fatalf("policy 403 hint = %q, want no credential hint", policy.Hint)
	}
	for _, code := range []string{"", api.CodeForbidden, api.CodeOrgRoleForbidden} {
		if got := diagnosticProblem(api.Problem{Status: 403, Code: code}); !strings.Contains(got.Hint, "profile check") {
			t.Fatalf("authorization 403 %q hint = %q, want the credential hint", code, got.Hint)
		}
	}
}
