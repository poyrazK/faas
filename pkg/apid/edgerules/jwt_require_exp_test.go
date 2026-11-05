package edgerules

import (
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 091 — a kind=jwt action that omits require_exp rejects tokens without
// exp; accepting them takes an explicit false.
func TestActionFromBodyJWTRequireExpDefaultsOn(t *testing.T) {
	base := `"issuer":"https://auth.example","jwks_url":"https://auth.example/jwks","algorithms":["ES256"]`
	cases := map[string]bool{
		`{` + base + `}`:                     true,
		`{` + base + `,"require_exp":true}`:  true,
		`{` + base + `,"require_exp":false}`: false,
		`{` + base + `,"require_exp":null}`:  true,
	}
	for raw, want := range cases {
		action := ActionFromBody(string(state.EdgeRuleKindJWT), json.RawMessage(raw))
		if action.JWT == nil || action.JWT.RequireExp != want {
			t.Errorf("%s: RequireExp = %+v, want %v", raw, action.JWT, want)
		}
	}
}
