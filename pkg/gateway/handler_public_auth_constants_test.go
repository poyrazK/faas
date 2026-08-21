package gateway

// handler_public_auth_constants_test.go — gateway-side
// pin for the canonical-source rule (issue #477 / ADR-079 +
// ADR-118). The lowercase package-local mode constants
// (publicAuthModeOpen / Bearer / Basic / IPAllowlist) must
// stay byte-for-byte in sync with the exported api-layer
// constants and the state-layer constants. A drift surfaces
// as a runtime mismatch in pkg/gateway/handler.go's
// enforcePublicAuth switch (handler.go:3477-3483) — the
// switch silently fall-throughs to "open" for any mode
// string the gateway doesn't recognise, which means a
// typo here is a silent auth bypass.
//
// This test lives in `package gateway` (white-box) so it
// can read the unexported constants directly. The
// api/state mirror is pinned by
// pkg/api/public_auth_constants_test.go; together the
// three surfaces stay equal.

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestPublicAuthGatewayModeConstantsAgree pins the
// pkg/gateway package-local constants against the canonical
// pkg/api constants. ADR-118 adds publicAuthModeIPAllowlist
// ("ip_allowlist"); ADR-119 adds publicAuthModeInternalOnly
// ("internal_only"); ADR-123 adds publicAuthModeMembersOnly
// ("members_only"). Order matters: open, bearer, basic,
// ip_allowlist, internal_only, members_only — matches the
// historical ship order so a future contributor reading the
// slice knows which mode shipped when.
func TestPublicAuthGatewayModeConstantsAgree(t *testing.T) {
	gatewaySet := map[string]string{
		"open":          publicAuthModeOpen,
		"bearer":        publicAuthModeBearer,
		"basic":         publicAuthModeBasic,
		"ip_allowlist":  publicAuthModeIPAllowlist,
		"internal_only": publicAuthModeInternalOnly,
		"members_only":  publicAuthModeMembersOnly,
	}
	apiSet := map[string]string{
		"open":          api.AppPublicAuthModeOpen,
		"bearer":        api.AppPublicAuthModeBearer,
		"basic":         api.AppPublicAuthModeBasic,
		"ip_allowlist":  api.AppPublicAuthModeIPAllowlist,
		"internal_only": api.AppPublicAuthModeInternalOnly,
		"members_only":  api.AppPublicAuthModeMembersOnly,
	}
	if len(gatewaySet) != len(apiSet) {
		t.Fatalf("set size mismatch: pkg/gateway has %d modes, pkg/api has %d; "+
			"add/remove the constant on both sides in the same commit",
			len(gatewaySet), len(apiSet))
	}
	for name, gwVal := range gatewaySet {
		apiVal, ok := apiSet[name]
		if !ok {
			t.Errorf("pkg/gateway has mode %q (%q) with no pkg/api counterpart",
				name, gwVal)
			continue
		}
		if gwVal != apiVal {
			t.Errorf("mode %q drift: pkg/gateway.publicAuthMode* = %q, pkg/api.AppPublicAuthMode* = %q",
				name, gwVal, apiVal)
		}
	}
	for name, apiVal := range apiSet {
		gwVal, ok := gatewaySet[name]
		if !ok {
			t.Errorf("pkg/api has mode %q (%q) with no pkg/gateway counterpart",
				name, apiVal)
			continue
		}
		if gwVal != apiVal {
			// Already reported above; skip the duplicate.
			continue
		}
	}
}
