package gateway

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apidgrpc"
	"github.com/onebox-faas/faas/pkg/state"
)

// OAuth consumers (ADR-20261124094745): a caller whose token an MCP
// resource policy verified becomes the app's API consumer, so its calls are
// billed and admitted against the app's free plan like a consumer key's.
// apid owns enrollment; the gateway caches the answer.

// OAuthConsumerEnroller asks apid for the consumer an OAuth caller is:
// apidgrpc.OAuthConsumers. Refusals are apidgrpc.ErrOAuthConsumersOff and
// apidgrpc.ErrOAuthConsumerSignupLimit.
type OAuthConsumerEnroller interface {
	EnrollOAuthConsumer(ctx context.Context, accountID, appID, issuer, subject, email string) (apidgrpc.OAuthConsumer, error)
}

const (
	// oauthConsumerTTL keeps an enrolled caller's consumer, so a revoked
	// consumer is refused within it.
	oauthConsumerTTL = 5 * time.Minute
	// oauthConsumersOffTTL keeps an app's "off" answer, so turning OAuth
	// consumers on takes effect within it.
	oauthConsumersOffTTL = 5 * time.Minute
	// oauthConsumersOnTTL keeps the mark that an app enrolls callers. While
	// it holds, an apid outage refuses the app's callers (503) rather than
	// serving them unattributed; an app without it, which may never have
	// opted in, keeps serving through the outage.
	oauthConsumersOnTTL = 24 * time.Hour
	// oauthConsumerRefusedTTL keeps a daily-limit refusal briefly.
	oauthConsumerRefusedTTL = 30 * time.Second
	// oauthConsumerCacheMax bounds the cache; past it, it starts over.
	oauthConsumerCacheMax = 100_000
	// oauthConsumerEnrollTimeout bounds the apid call on the request path.
	oauthConsumerEnrollTimeout = 2 * time.Second
)

type oauthConsumerEntry struct {
	id     string
	active bool
	// accountTools is the app's opt-in to MCP account tools
	// (ADR-20261125194510).
	accountTools bool
	err          error
	expires      time.Time
}

type oauthConsumerCache struct {
	mu      sync.Mutex
	entries map[string]oauthConsumerEntry
	// apps holds the per-app "off" and "on" marks apart from callers, so
	// churn of callers never drops the mark that fails an app closed.
	apps map[string]oauthConsumerEntry
}

// table returns the map key belongs in. Callers hold c.mu.
func (c *oauthConsumerCache) table(key string) *map[string]oauthConsumerEntry {
	if strings.HasPrefix(key, "sub\x00") {
		return &c.entries
	}
	return &c.apps
}

func (c *oauthConsumerCache) get(key string, now time.Time) (oauthConsumerEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := (*c.table(key))[key]
	if !ok || !now.Before(e.expires) {
		return oauthConsumerEntry{}, false
	}
	return e, true
}

func (c *oauthConsumerCache) put(key string, e oauthConsumerEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := c.table(key)
	if *t == nil || len(*t) >= oauthConsumerCacheMax {
		*t = map[string]oauthConsumerEntry{}
	}
	(*t)[key] = e
}

// WithOAuthConsumers lets MCP resource policies enroll verified callers as
// API consumers. Nil leaves them unattributed, as before.
func (h *Handler) WithOAuthConsumers(e OAuthConsumerEnroller) *Handler {
	h.oauthConsumers, h.oauthConsumerCache = e, &oauthConsumerCache{}
	return h
}

// oauthConsumer returns the consumer an OAuth caller is, from the cache or
// apid. An empty ID with a nil error means the app does not enroll callers,
// or is not known to while apid cannot answer.
func (h *Handler) oauthConsumer(ctx context.Context, app App, issuer, subject, email string) (oauthConsumerEntry, error) {
	now := time.Now()
	key := oauthConsumerCacheKey(app.ID, issuer, subject)
	if e, ok := h.oauthConsumerCache.get(key, now); ok {
		return e, e.err
	}
	ctx, cancel := context.WithTimeout(ctx, oauthConsumerEnrollTimeout)
	defer cancel()
	enrolled, err := h.oauthConsumers.EnrollOAuthConsumer(ctx, app.AccountID, app.ID, issuer, subject, email)
	onKey := "on\x00" + app.ID
	switch {
	case errors.Is(err, apidgrpc.ErrOAuthConsumersOff):
		// Cached per caller, not per app: an app that stopped enrolling
		// still answers callers who joined before with their consumer.
		e := oauthConsumerEntry{expires: now.Add(oauthConsumersOffTTL)}
		h.oauthConsumerCache.put(key, e)
		h.oauthConsumerCache.put(onKey, oauthConsumerEntry{expires: now})
		return e, nil
	case errors.Is(err, apidgrpc.ErrOAuthConsumerSignupLimit), errors.Is(err, apidgrpc.ErrOAuthConsumerRefused):
		h.oauthConsumerCache.put(key, oauthConsumerEntry{err: err, expires: now.Add(oauthConsumerRefusedTTL)})
		return oauthConsumerEntry{}, err
	case err != nil:
		if _, on := h.oauthConsumerCache.get(onKey, now); on {
			return oauthConsumerEntry{}, err
		}
		if h.log != nil {
			h.log.Warn("oauth consumer enrollment unavailable; serving an app not known to enroll callers", "err", err, "app_id", app.ID)
		}
		return oauthConsumerEntry{}, nil
	}
	e := oauthConsumerEntry{id: enrolled.ID, active: enrolled.Active, accountTools: enrolled.AccountTools, expires: now.Add(oauthConsumerTTL)}
	h.oauthConsumerCache.put(key, e)
	h.oauthConsumerCache.put(onKey, oauthConsumerEntry{expires: now.Add(oauthConsumersOnTTL)})
	return e, nil
}

// oauthStageCaller reports whether app was reached through a stage's
// environment URL or scoped domain rather than production.
func oauthStageCaller(app App) bool {
	return app.PublicEnvironmentID != "" && !state.IsProductionScope(app.PinnedDeploymentScope)
}

// oauthIssuer is the issuer an OAuth consumer's identity is keyed by: the
// token's iss, or the rule's pinned issuer when the token has none.
func oauthIssuer(rule *EdgeRuleJWTResolved, claims *JWTClaims) string {
	if claims.Issuer != "" {
		return claims.Issuer
	}
	return rule.Issuer
}

func oauthConsumerCacheKey(appID, issuer, subject string) string {
	return "sub\x00" + appID + "\x00" + issuer + "\x00" + subject
}

// attachOAuthConsumer stamps the verified caller's consumer on r. It reports
// true after answering: 403 for a revoked consumer, 429 past the app's daily
// sign-ups, and 503 when apid cannot answer for an app known to enroll
// callers, because an unattributed call would be served unbilled and
// unlimited.
func (h *Handler) attachOAuthConsumer(w http.ResponseWriter, r *http.Request, app App, rule *EdgeRuleJWTResolved, claims *JWTClaims) bool {
	authenticated := authenticatedFrom(r.Context())
	if h.oauthConsumers == nil || authenticated.ConsumerID != "" || !h.APIMonetizationEnabledFor(app.AccountID) {
		return false
	}
	e, err := h.oauthConsumer(r.Context(), app, oauthIssuer(rule, claims), claims.Subject, claims.Custom["email"])
	switch {
	case errors.Is(err, apidgrpc.ErrOAuthConsumerSignupLimit):
		w.Header().Set("Retry-After", "3600")
		w.Header().Set("Cache-Control", "no-store")
		api.WriteProblem(w, api.NewProblem(http.StatusTooManyRequests, api.CodeConsumerSignupDailyLimit, "Sign-ups closed for today",
			"this MCP server took its new customers for the day; try again tomorrow"))
		return true
	case errors.Is(err, apidgrpc.ErrOAuthConsumerRefused):
		// Serving the caller unattributed would be unbilled and unlimited.
		if h.log != nil {
			h.log.Warn("oauth consumer enrollment refused", "app_id", app.ID)
		}
		return mcpPolicyReject(w, http.StatusForbidden, rule.MCP, "This MCP server cannot open an account for you; contact its owner")
	case err != nil:
		if h.log != nil {
			h.log.Error("oauth consumer enrollment unavailable", "err", err, "app_id", app.ID)
		}
		w.Header().Set("Cache-Control", "no-store")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeConsumerEnrollmentUnavailable,
			"Consumer enrollment unavailable", "this MCP server could not verify your account; try again shortly"))
		return true
	case e.id == "":
		return false
	case !e.active:
		return mcpPolicyReject(w, http.StatusForbidden, rule.MCP, "This account was closed by the MCP server's owner")
	}
	authenticated.ConsumerID = e.id
	// A stage's callers are never billed or plan-capped, like a stage's
	// consumer keys (ADR-20261104120000 amendment 2026-11-22). A consumer
	// key wins over a tenant surface, and so does an OAuth consumer
	// (usageAttribution keeps one of them).
	authenticated.StageConsumer = oauthStageCaller(app)
	ctx := withAuthenticated(r.Context(), authenticated)
	if e.accountTools {
		ctx = context.WithValue(ctx, mcpAccountToolsKey{}, true)
	}
	*r = *r.WithContext(ctx)
	return false
}
