// Unit tests for ValidateScope (ADR-090 PR-B).
//
// Coverage pins every rejection branch on the helper so a future
// relaxation (e.g. allowing underscores) is a deliberate change:
//   - the reserved sentinel "__all__" is rejected even though it
//     would otherwise be a valid slug-like string
//   - the empty string is rejected
//   - strings that fail EnvScopePattern (1..40 lowercase alnum +
//     dash, no leading/trailing dash) are rejected
//   - strings that exceed MaxEnvScopeLen are rejected
//   - well-formed scope names are accepted
//
// The helper is also exercised by the handler tests in
// cmd/apid/handlers_env_scope_test.go (the 400 env_scope_invalid
// wire path). The two test files together pin the helper at
// both the unit and the integration seam.

package api

import (
	"strings"
	"testing"
)

// TestValidateScope_ReservedSentinel pins the read-only sentinel
// rule. The string "__all__" fails the regex (leading underscore
// is not lowercase alnum) AND is rejected by the dedicated
// sentinel guard. The dedicated guard is the load-bearing one
// because a future relaxation that admits underscores would
// otherwise accidentally start accepting the sentinel.
func TestValidateScope_ReservedSentinel(t *testing.T) {
	prob := ValidateScope(EnvScopeAllSentinel)
	if prob == nil {
		t.Fatal("ValidateScope(__all__): got nil, want ErrEnvScopeReserved")
	}
	if prob.Code != CodeEnvScopeReserved {
		t.Errorf("code: got %q, want %q", prob.Code, CodeEnvScopeReserved)
	}
	if !strings.Contains(prob.Detail, EnvScopeAllSentinel) {
		t.Errorf("Detail should name the sentinel %q; got %q", EnvScopeAllSentinel, prob.Detail)
	}
}

// TestValidateScope_Empty_Rejected pins the empty-string arm.
// The handler's scopeFromQuery short-circuits empty to
// defaultEnvScope before calling ValidateScope, but a future
// caller that bypasses the helper must still see a clean
// rejection — the helper is the wire-shape gate.
func TestValidateScope_Empty_Rejected(t *testing.T) {
	prob := ValidateScope("")
	if prob == nil {
		t.Fatal("ValidateScope(\"\"): got nil, want ErrEnvScopeInvalid")
	}
	if prob.Code != CodeEnvScopeInvalid {
		t.Errorf("code: got %q, want %q", prob.Code, CodeEnvScopeInvalid)
	}
}

// TestValidateScope_TooLong_Rejected pins the length cap. A
// string of exactly MaxEnvScopeLen+1 bytes fails; the migration's
// CHECK constraint would also reject it, but the helper gives the
// customer a clean 400 problem with a docs link.
func TestValidateScope_TooLong_Rejected(t *testing.T) {
	// 41 chars: a..z1..a (lowercase alnum, valid shape, too long).
	scope := strings.Repeat("a", 41)
	prob := ValidateScope(scope)
	if prob == nil {
		t.Fatalf("ValidateScope(%q): got nil, want ErrEnvScopeInvalid", scope)
	}
	if prob.Code != CodeEnvScopeInvalid {
		t.Errorf("code: got %q, want %q", prob.Code, CodeEnvScopeInvalid)
	}
}

// TestValidateScope_PatternFailures_Rejected walks the regex
// failure modes. Each case must return 400 env_scope_invalid
// (NOT env_scope_reserved — that's reserved for "__all__"). A
// future relaxation that admits underscores or uppercase must
// be a deliberate change to EnvScopePattern + the migration's
// CHECK + this test.
func TestValidateScope_PatternFailures_Rejected(t *testing.T) {
	cases := []struct{ name, in string }{
		{"dash_only", "-"},
		{"leading_dash", "-foo"},
		{"trailing_dash", "foo-"},
		{"uppercase", "Staging"},
		{"underscore", "staging_eu"},
		{"space", "staging eu"},
		{"dot", "staging.eu"},
		{"tab", "staging\teu"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			prob := ValidateScope(tc.in)
			if prob == nil {
				t.Fatalf("ValidateScope(%q): got nil, want ErrEnvScopeInvalid", tc.in)
			}
			if prob.Code != CodeEnvScopeInvalid {
				t.Errorf("code: got %q, want %q", prob.Code, CodeEnvScopeInvalid)
			}
		})
	}
}

// TestValidateScope_Accepts exercises the accept path. Each
// well-formed scope must return nil so a future bug that
// accidentally rejects a valid slug trips this seam.
//
// The scope contract admits 1..40 chars. We pin catalog short and numeric
// names as well as legacy mid-range variants and the
// 40-char boundaries.
func TestValidateScope_Accepts(t *testing.T) {
	cases := []string{
		"a", "1", "ab", "12", "1a", // short catalog names
		"default", // 7 chars
		"staging", // 7 chars
		"prod-eu", // 8 chars
		"abc",
		strings.Repeat("a", 40), // exact 40-char upper bound
		"a-1",                   // mixed alnum + dash
		"prod-us-west-2",        // 15 chars
	}
	for _, in := range cases {
		in := in
		t.Run(in, func(t *testing.T) {
			if prob := ValidateScope(in); prob != nil {
				t.Errorf("ValidateScope(%q): got %+v, want nil", in, prob)
			}
		})
	}
}

// TestEnvScopePattern_Stable pins the regex constant. The
// migration's CHECK uses the same shape, so a future divergence
// between Go and SQL would either reject a valid input on the
// wire (Go allows, SQL rejects) or accept an invalid one (SQL
// allows, Go rejects). Either way the wire is broken — this
// test catches a refactor that changes the constant without a
// paired migration.
func TestEnvScopePattern_Stable(t *testing.T) {
	if EnvScopePattern != `^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$` {
		t.Errorf("EnvScopePattern drifted: got %q, want the migration's CHECK shape", EnvScopePattern)
	}
	if MaxEnvScopeLen != 40 {
		t.Errorf("MaxEnvScopeLen drifted: got %d, want 40 (mirrors CHECK upper bound)", MaxEnvScopeLen)
	}
}

func TestCatalogEnvironmentNamesAreValidRuntimeScopes(t *testing.T) {
	for _, scope := range []string{"a", "1", "ab", "12", "1a", "a-1", strings.Repeat("a", 33)} {
		if !ValidProjectEnvironmentSlug(scope) || ValidateScope(scope) != nil {
			t.Fatalf("catalog/runtime scope mismatch for %q", scope)
		}
	}
	if ValidProjectEnvironmentSlug(DefaultEnvScope) || ValidateScope(DefaultEnvScope) != nil {
		t.Fatal("default must remain a runtime scope and reserved in the catalog")
	}
}
