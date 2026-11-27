package api

import (
	"slices"
	"testing"
)

// The Connect grant rides on the sign-in code only (ADR-20261127160000 §2):
// it has a fixed bit after the grantable scopes, no key carries it, and
// organization policies neither list nor cap it.
func TestConnectApprovalIsACodeOnlyScope(t *testing.T) {
	if IsValidScope(ScopeConnectApproval) {
		t.Fatalf("%s is a key scope; the api_keys vocabulary must not include it", ScopeConnectApproval)
	}
	if slices.Contains(MCPGrantableScopes, ScopeConnectApproval) {
		t.Fatalf("%s is in MCPGrantableScopes; organization policies would list it", ScopeConnectApproval)
	}
	if got := slices.Index(MCPCodeScopes, ScopeConnectApproval); got != 7 {
		t.Fatalf("%s is bit %d of the code; issued codes name it as bit 7", ScopeConnectApproval, got)
	}
	for _, scope := range MCPGrantableScopes {
		if !slices.Contains(MCPCodeScopes, scope) {
			t.Errorf("grantable %s has no bit in MCPCodeScopes", scope)
		}
	}
	if !slices.Equal(MCPCodeScopes[:7], MCPGrantableScopes[:7]) {
		t.Fatalf("MCPCodeScopes[:7] = %v; issued codes use the bits of %v", MCPCodeScopes[:7], MCPGrantableScopes[:7])
	}
	if len(MCPCodeScopes) > 8 {
		t.Fatalf("%d code scopes; the code's scope field holds 8 bits", len(MCPCodeScopes))
	}
}
