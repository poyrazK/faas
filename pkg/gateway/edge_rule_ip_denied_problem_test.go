package gateway

import (
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// TestEdgeRuleIPDeniedProblemNamesARecipeRule: the allow-ips-only recipe
// refused an unlisted IP on production-us rc.294 with "client IP matches a
// deny CIDR on this edge rule" (Hunt 14 H14-68). A condition rule names
// itself; a plain CIDR denylist keeps its wording. Both stay ip_denied.
func TestEdgeRuleIPDeniedProblemNamesARecipeRule(t *testing.T) {
	recipe := &EdgeRuleIPResolved{ID: "0c3d5048-ea5a-4e40-b98d-26698de326b8", EdgeRuleCondition: EdgeRuleCondition{Match: &api.EdgeRuleMatchProgram{}}}
	plain := &EdgeRuleIPResolved{ID: "r2"}
	for _, tc := range []struct {
		rule   *EdgeRuleIPResolved
		detail string
	}{
		{recipe, "the request matches edge rule 0c3d5048-ea5a-4e40-b98d-26698de326b8, which refuses it"},
		{plain, "client IP matches a deny CIDR on this edge rule"},
	} {
		p := edgeRuleIPDeniedProblem(tc.rule)
		if p.Status != http.StatusForbidden || p.Code != api.CodeIPDenied || !strings.Contains(p.Detail, tc.detail) {
			t.Errorf("rule %s: got %d %s %q", tc.rule.ID, p.Status, p.Code, p.Detail)
		}
	}
}
