package main

import (
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// accountHealthHint points a refused request at the account health checks
// when the refusal is one they explain (ADR-20261115090000): a revoked or
// expired key, a past-due account, an unverified email, or a plan limit.
// A revoked or expired key cannot read the health API either, so those
// point at the dashboard page, which a browser session can open. Other codes
// get no hint.
func accountHealthHint(code, dashboard string) string {
	switch {
	case code == api.CodeAPIKeyRevoked || code == api.CodeAPIKeyExpired:
		return "Sign in at " + dashboard + "/dashboard/account/health to see which requests were refused and why."
	case code == api.CodeBillingPastDue || code == api.CodeEmailNotVerified || planLimitCode(code):
		return "Run gregale account health get to see what else needs attention on your account."
	}
	return ""
}

// planLimitCode matches the plan-limit and quota codes the limit_pressure
// signal counts; rate limits are not plan limits.
func planLimitCode(code string) bool {
	return strings.HasPrefix(code, "plan_limit_") || strings.HasSuffix(code, "_quota_exceeded") ||
		strings.HasSuffix(code, "_limit_exceeded")
}
