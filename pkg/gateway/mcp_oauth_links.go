package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
)

// Linking by key (ADR-20261124094745): a person who holds a consumer key
// for an app and signs in to its MCP server proves the key, and their
// sign-ins then bill to the key's consumer. The gateway verifies the OAuth
// token, as for any MCP call; apid verifies the key and moves the identity.

// OAuthConsumerLinker asks apid to link a verified OAuth caller to a key's
// consumer: apidgrpc.OAuthConsumers. Refusals are
// apidgrpc.ErrOAuthConsumersOff, apidgrpc.ErrOAuthLinkKeyRejected and
// apidgrpc.ErrOAuthLinkConflict.
type OAuthConsumerLinker interface {
	LinkOAuthConsumer(ctx context.Context, accountID, appID, issuer, subject, email, key string) (consumerID string, err error)
}

const (
	// oauthLinkMaxBody bounds the link request body: one consumer key.
	oauthLinkMaxBody = 4 << 10
	// oauthLinkTimeout bounds the apid call.
	oauthLinkTimeout = 5 * time.Second
)

// WithOAuthConsumerLinks serves MCP resource policies' consumer link path.
func (h *Handler) WithOAuthConsumerLinks(l OAuthConsumerLinker) *Handler {
	h.oauthLinks = l
	return h
}

// linkOAuthConsumer answers a verified caller's POST to the policy's
// consumer link path.
func (h *Handler) linkOAuthConsumer(w http.ResponseWriter, r *http.Request, app App, rule *EdgeRuleJWTResolved, claims *JWTClaims) {
	w.Header().Set("Cache-Control", "no-store")
	if h.oauthLinks == nil || h.oauthConsumerCache == nil || !h.APIMonetizationEnabledFor(app.AccountID) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found",
			"this MCP server does not link accounts to consumer keys"))
		return
	}
	var body api.LinkConsumerRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, oauthLinkMaxBody)).Decode(&body); err != nil || body.Key == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid request",
			`send {"key": "ck_..."}: the consumer key you hold for this MCP server`))
		return
	}
	issuer := oauthIssuer(rule, claims)
	ctx, cancel := context.WithTimeout(r.Context(), oauthLinkTimeout)
	defer cancel()
	consumerID, err := h.oauthLinks.LinkOAuthConsumer(ctx, app.AccountID, app.ID, issuer, claims.Subject, claims.Custom["email"], body.Key)
	switch {
	case errors.Is(err, apidgrpc.ErrOAuthConsumersOff):
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found",
			"this MCP server does not link accounts to consumer keys"))
		return
	case errors.Is(err, apidgrpc.ErrOAuthLinkKeyRejected):
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeConsumerLinkKeyRejected, "Key rejected",
			"the key is not a live consumer key for this MCP server"))
		return
	case errors.Is(err, apidgrpc.ErrOAuthLinkInvalidIdentity):
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid sign-in",
			"your sign-in's issuer or subject is too long to link"))
		return
	case errors.Is(err, apidgrpc.ErrOAuthLinkConflict):
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeConsumerLinkConflict, "Already linked",
			"your sign-in is linked to another consumer key; ask the MCP server's owner to unlink it"))
		return
	case err != nil:
		if h.log != nil {
			h.log.Error("oauth consumer link unavailable", "err", err, "app_id", app.ID)
		}
		w.Header().Set("Retry-After", "5")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeConsumerEnrollmentUnavailable,
			"Consumer link unavailable", "this MCP server could not link your account; try again shortly"))
		return
	}
	// This gateway bills the caller's next call to the key's consumer; the
	// others pick the link up when their cached entry expires.
	h.oauthConsumerCache.put(oauthConsumerCacheKey(app.ID, issuer, claims.Subject),
		oauthConsumerEntry{id: consumerID, active: true, expires: time.Now().Add(oauthConsumerTTL)})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(api.LinkConsumerResponse{ConsumerID: consumerID})
}
