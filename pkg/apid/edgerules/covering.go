package edgerules

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

// CoveringRules returns the rules of app's account written on its other apps
// in the same edge-rule scope whose host pattern can cover app's hosts: every
// rule but "@app" ones (ADR-091 amendment 2026-10-31, round 7). The gateway
// applies those to app's hosts as well as app's own rules, so a trace built
// from app's rules alone missed a "*" rule on a sibling app that refused the
// request. Rules of deleted apps are left out, as the gateway leaves them out.
func (s *Service) CoveringRules(ctx context.Context, app state.App) ([]state.EdgeRule, error) {
	rules, err := s.Host.Store().ListEdgeRulesForAccount(ctx, app.AccountID)
	if err != nil {
		return nil, fmt.Errorf("list account edge rules: %w", err)
	}
	scope, err := state.AppEdgeRuleScope(ctx, s.Host.Store(), app)
	if err != nil {
		return nil, err
	}
	sameScope := map[string]bool{}
	var out []state.EdgeRule
	for _, rule := range rules {
		if rule.AppID == app.ID || api.IsEdgeRuleAppHosts(rule.MatchHost) {
			continue
		}
		covers, known := sameScope[rule.AppID]
		if !known {
			if covers, err = s.appInScope(ctx, rule.AppID, scope); err != nil {
				return nil, err
			}
			sameScope[rule.AppID] = covers
		}
		if covers {
			out = append(out, rule)
		}
	}
	return out, nil
}

func (s *Service) appInScope(ctx context.Context, appID, scope string) (bool, error) {
	other, err := s.Host.Store().AppByID(ctx, appID)
	if errors.Is(err, state.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("load app %s: %w", appID, err)
	}
	if other.Status == state.AppDeleted {
		return false, nil
	}
	otherScope, err := state.AppEdgeRuleScope(ctx, s.Host.Store(), other)
	return otherScope == scope, err
}

// VisibleCoveringRules is CoveringRules filtered to the apps the request's
// caller may still read, as the account-wide list is: a company app's
// creator and the members of a project see different sets.
func (s *Service) VisibleCoveringRules(r *http.Request, acct state.Account, app state.App) ([]state.EdgeRule, error) {
	rules, err := s.CoveringRules(r.Context(), app)
	if err != nil || len(rules) == 0 {
		return rules, err
	}
	actor := acct
	if a, _, ok := middleware.AccountFromContext(r); ok {
		actor = a
	}
	return appaccess.VisibleResources(r, s.Host.Store(), actor, rules, func(rule state.EdgeRule) string { return rule.AppID })
}

// wantsCoveringRules reports the list request's ?covering=true.
func wantsCoveringRules(r *http.Request) bool {
	covering, _ := strconv.ParseBool(r.URL.Query().Get("covering"))
	return covering
}
