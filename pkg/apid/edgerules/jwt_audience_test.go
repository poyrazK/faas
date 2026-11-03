package edgerules

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — a new or edited kind=jwt rule names an audience or a required
// claim; without one every token its issuer signs passes (amendment
// 2026-11-02, review round 8).
func TestJWTRuleNeedsAnAudienceOrARequiredClaim(t *testing.T) {
	base := `"issuer":"https://token.actions.githubusercontent.com","jwks_url":"https://token.actions.githubusercontent.com/.well-known/jwks","algorithms":["RS256"]`
	cases := map[string]bool{
		`{` + base + `}`:                                        false,
		`{` + base + `,"audience":[]}`:                          false,
		`{` + base + `,"audience":[" "]}`:                       false,
		`{` + base + `,"audience":["my-api"]}`:                  true,
		`{` + base + `,"required_claims":{"repository":"a/b"}}`: true,
	}
	for raw, ok := range cases {
		prob := ValidateEdgeRuleAction(string(state.EdgeRuleKindJWT), json.RawMessage(raw), "/**", api.PlanPro)
		if (prob == nil) != ok {
			t.Errorf("%s: problem = %v, want accepted=%v", raw, prob, ok)
		}
	}
}
