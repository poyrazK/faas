//go:build !no_pg

package main

import (
	"context"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type starterAsyncMatcher struct{ appID, accountID string }

func (m starterAsyncMatcher) MatchAsync(_ context.Context, _, path, method string) *gateway.EdgeRuleAsyncResolved {
	if path != "/documents" || method != "POST" {
		return nil
	}
	return &gateway.EdgeRuleAsyncResolved{ID: "starter-documents-async", AppID: m.appID, AccountID: m.accountID}
}
func (starterAsyncMatcher) Converging(string, string) bool { return false }
func (starterAsyncMatcher) MatchRoute(ctx context.Context, host, path, method string) *gateway.EdgeRuleResolved {
	return nil
}
func (starterAsyncMatcher) MatchRewrite(ctx context.Context, host, path, method string) *gateway.EdgeRuleRewriteResolved {
	return nil
}
func (starterAsyncMatcher) MatchRedirect(ctx context.Context, host, path, method string) *gateway.EdgeRuleRedirectResolved {
	return nil
}
func (starterAsyncMatcher) MatchHeaders(ctx context.Context, host, path, method string) *gateway.EdgeRuleHeadersResolved {
	return nil
}
func (starterAsyncMatcher) MatchCORS(ctx context.Context, host, path, method string) *gateway.EdgeRuleCORSResolved {
	return nil
}
func (starterAsyncMatcher) MatchJWT(ctx context.Context, host, path, method string) *gateway.EdgeRuleJWTResolved {
	return nil
}
func (starterAsyncMatcher) MatchIP(ctx context.Context, host, path, method string) *gateway.EdgeRuleIPResolved {
	return nil
}
func (starterAsyncMatcher) MatchValidate(ctx context.Context, host, path, method string) *gateway.EdgeRuleValidateResolved {
	return nil
}
func (starterAsyncMatcher) MatchLimit(ctx context.Context, host, path, method string) *gateway.EdgeRuleLimitResolved {
	return nil
}
func (starterAsyncMatcher) MatchMaintenance(ctx context.Context, host, path, method string) *gateway.EdgeRuleMaintenanceResolved {
	return nil
}
func (starterAsyncMatcher) MatchGeo(ctx context.Context, host, path, method string) *gateway.EdgeRuleGeoResolved {
	return nil
}
func (starterAsyncMatcher) MatchThrottle(ctx context.Context, host, path, method string) *gateway.EdgeRuleThrottleResolved {
	return nil
}
func (starterAsyncMatcher) MatchBudget(ctx context.Context, host, path, method string) *gateway.EdgeRuleBudgetResolved {
	return nil
}
func (starterAsyncMatcher) MatchCache(ctx context.Context, host, path, method string) *gateway.EdgeRuleCacheResolved {
	return nil
}
func (starterAsyncMatcher) MatchRespond(ctx context.Context, host, path, method string) *gateway.EdgeRuleRespondResolved {
	return nil
}
func (starterAsyncMatcher) MatchRetry(ctx context.Context, host, path, method string) *gateway.EdgeRuleRetryResolved {
	return nil
}
func (starterAsyncMatcher) MatchCircuitBreaker(ctx context.Context, host, path, method string) *gateway.EdgeRuleCircuitBreakerResolved {
	return nil
}
func (starterAsyncMatcher) Reset() {}
