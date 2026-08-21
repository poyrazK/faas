package api_test

// public_auth_constants_test.go — cross-package pin for
// the canonical-source rule (issue #477 / ADR-079 + ADR-118).
// The same closed-enum mode strings live in pkg/api,
// pkg/state, and pkg/gateway (the gateway-local copies
// are unexported so the third surface is verified via
// pkg/gateway_test's companion test — see
// TestPublicAuthGatewayModeConstantsAgree in
// pkg/gateway/handler_public_auth_constants_test.go).
// A drift surfaces as a runtime SQL CHECK-constraint
// failure on a PATCH that the API itself accepted.
//
// This test lives in `package api_test` (external test
// package) because pkg/api cannot import pkg/state
// without a cycle (pkg/state stops importing pkg/api
// through the ... cycle per the API/state module split).
// The caps-test counterpart lives in the internal
// package at public_auth_caps_test.go because the
// boundary constants are unexported.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// TestPublicAuthModeConstantsAgree pins the
// pkg/api <-> pkg/state constant alignment. A future
// contributor adding a fifth mode (e.g. mTLS) MUST
// add the constant to all three surfaces in the same
// commit; running this test after only one side is
// updated fails immediately. The previous-incident
// pattern (webhook secret namespace drift pre-#476)
// is the reference for why this tripwire exists.
//
// ADR-118 adds AppPublicAuthModeIPAllowlist ("ip_allowlist");
// ADR-119 adds AppPublicAuthModeInternalOnly ("internal_only");
// ADR-120 adds AppPublicAuthModeMembersOnly ("members_only").
// Order matters: open, bearer, basic, ip_allowlist,
// internal_only, members_only — matches the historical ship
// order so a future contributor reading the slice knows which
// mode shipped when.
func TestPublicAuthModeConstantsAgree(t *testing.T) {
	apiSet := []string{
		api.AppPublicAuthModeOpen,
		api.AppPublicAuthModeBearer,
		api.AppPublicAuthModeBasic,
		api.AppPublicAuthModeIPAllowlist,
		api.AppPublicAuthModeInternalOnly,
		api.AppPublicAuthModeMembersOnly,
	}
	stateSet := []string{
		state.AppPublicAuthModeOpen,
		state.AppPublicAuthModeBearer,
		state.AppPublicAuthModeBasic,
		state.AppPublicAuthModeIPAllowlist,
		state.AppPublicAuthModeInternalOnly,
		state.AppPublicAuthModeMembersOnly,
	}
	if len(apiSet) != len(stateSet) {
		t.Fatalf("slice length mismatch: pkg/api has %d modes, pkg/state has %d; "+
			"add/remove the constant on both sides in the same commit",
			len(apiSet), len(stateSet))
	}
	for i := range apiSet {
		if apiSet[i] != stateSet[i] {
			t.Errorf("mode %d drift: pkg/api.AppPublicAuthMode* = %q, pkg/state.AppPublicAuthMode* = %q",
				i, apiSet[i], stateSet[i])
		}
	}
	// Cross-check the closed-enum hash so the test fails
	// narratively even if a future contributor shuffles
	// the order to mask the drift.
	apiMap := make(map[string]struct{}, len(apiSet))
	for _, m := range apiSet {
		apiMap[m] = struct{}{}
	}
	stateMap := make(map[string]struct{}, len(stateSet))
	for _, m := range stateSet {
		stateMap[m] = struct{}{}
	}
	for m := range apiMap {
		if _, ok := stateMap[m]; !ok {
			t.Errorf("pkg/api mode %q has no pkg/state counterpart", m)
		}
	}
	for m := range stateMap {
		if _, ok := apiMap[m]; !ok {
			t.Errorf("pkg/state mode %q has no pkg/api counterpart", m)
		}
	}
}
