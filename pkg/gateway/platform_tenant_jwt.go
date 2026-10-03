package gateway

import (
	"context"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
)

// PlatformTenantJWTIdentity is the account-owned tenant resolved from a
// verified JWT claim.
type PlatformTenantJWTIdentity struct {
	TenantID string
	Active   bool
}

// PlatformTenantExternalRefResolver is consulted only for an opted-in JWT
// rule after its issuer, audience, signature, and expiry have been verified.
type PlatformTenantExternalRefResolver interface {
	ResolvePlatformTenantExternalRef(context.Context, string, string) (PlatformTenantJWTIdentity, bool, error)
}

func (h *Handler) applyPlatformTenantJWTClaim(w http.ResponseWriter, r *http.Request, app App,
	rule *EdgeRuleJWTResolved, claims *JWTClaims, authenticated *Authenticated) bool {
	claimName := rule.PlatformTenantExternalRefClaim
	if claimName == "" {
		return false
	}
	externalRef := claims.Custom[claimName]
	if externalRef == "" {
		return h.rejectPlatformTenantJWTIdentity(w, r, rule, "the verified token did not contain the configured customer claim")
	}
	if h.platformTenantExternalRefResolver == nil {
		h.rejectUnavailableEdgeRule(w, r, "jwt", rule.ID, "platform_tenant_resolver_not_configured")
		return true
	}
	identity, found, err := h.platformTenantExternalRefResolver.ResolvePlatformTenantExternalRef(r.Context(), app.AccountID, externalRef)
	if err != nil {
		h.rejectUnavailableEdgeRule(w, r, "jwt", rule.ID, "platform_tenant_resolver_unavailable")
		return true
	}
	if !found || !identity.Active {
		return h.rejectPlatformTenantJWTIdentity(w, r, rule, "the verified token does not resolve to an active platform customer")
	}
	if authenticated.PlatformTenantID != "" && authenticated.PlatformTenantID != identity.TenantID {
		return h.rejectPlatformTenantJWTIdentity(w, r, rule, "the verified customer does not match the request's other authenticated customer")
	}
	if authenticated.PlatformTenantID == "" {
		authenticated.PlatformTenantID = identity.TenantID
	}
	if authenticated.ConsumerID == "" && authenticated.PlatformTenantSurfaceID == "" &&
		authenticated.PlatformTenantJWTAuthorizationRuleID == "" {
		authenticated.PlatformTenantJWTAuthorizationRuleID = rule.ID
	}
	return false
}

func (h *Handler) rejectPlatformTenantJWTIdentity(w http.ResponseWriter, r *http.Request,
	rule *EdgeRuleJWTResolved, detail string) bool {
	api.WriteProblem(w, api.NewProblem(http.StatusForbidden, "platform_tenant_identity_rejected",
		"Customer identity rejected", detail))
	accountID := rule.AccountID
	h.jwtEmit(r.Context(), "jwt", "failed", rule.ID, r.Host, &accountID, map[string]any{"reason": "platform_tenant_identity_rejected"})
	return true
}
