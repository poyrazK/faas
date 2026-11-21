package edgerules

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// duplicateCORSRuleProblem refuses a second enabled CORS rule on the host and
// path an existing one already matches. The edge applies only the first CORS
// rule that matches a request, so the second one was accepted and then
// silently never used (prod 2026-11-15: a wildcard-origin rule was ignored
// until the first rule was deleted). Rules with extra match conditions are
// left alone; they can legitimately narrow the same path.
func (s *Service) duplicateCORSRuleProblem(ctx context.Context, appID string, req api.CreateEdgeRuleRequest) *api.Problem {
	if req.Kind != string(state.EdgeRuleKindCORSA) || (req.Enabled != nil && !*req.Enabled) || req.Match != nil {
		return nil
	}
	rules, err := s.Host.Store().ListEdgeRulesForApp(ctx, appID)
	if err != nil {
		return api.ErrCapacity("could not check the app's existing CORS rules")
	}
	path := req.MatchPath
	if path == "" {
		path = "/"
	}
	for _, rule := range rules {
		if rule.Kind != state.EdgeRuleKindCORSA || !rule.Enabled || rule.Match != nil {
			continue
		}
		if strings.EqualFold(rule.MatchHost, req.MatchHost) && rule.MatchPath == path {
			return api.ErrValidation(fmt.Sprintf(
				"CORS rule %s already covers host %s path %s, and only the first matching CORS rule applies; add the origins to that rule or delete it first (gregale edge cors delete)",
				rule.ID, rule.MatchHost, path))
		}
	}
	return nil
}
