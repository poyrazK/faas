// Edge flag routing (ADR-20261124212701). A variant flag with edge routes is
// evaluated here, before the response cache and the app, for each GET or HEAD
// request whose path it routes: the decided variant's action rewrites the
// path, so an app without an SDK (a static site, a page the code does not
// branch on) serves the variant. A verified customer is bucketed as the SDK
// would bucket it; an anonymous visitor by a random first-party cookie, which
// keeps its variant across visits. The decision joins the request's flag
// evidence, so experiments count verified customers' exposures as they do
// for SDK decisions; anonymous visitors are served but not counted.
package gateway

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
	"github.com/onebox-faas/faas/pkg/platformflags"
)

// EdgeVisitorCookie keeps an anonymous visitor's edge bucket. The __Host-
// prefix pins it to the app's own host, over HTTPS, at path /.
const EdgeVisitorCookie = "__Host-gregale_vid"

// EdgeFlagLoader returns the edge-routed flags of an app's project
// environment ("" reads production); an empty bundle when there are none.
type EdgeFlagLoader func(ctx context.Context, appID, environment string) (flags.Bundle, error)

type edgeFlagEntry struct {
	bundle  flags.Bundle
	expires time.Time
}

// EdgeFlagCache caches each app environment's edge routes like
// FlagKillCache caches kills: one lookup, emptied on feature_flags_changed,
// bounded by flagKillsTTL when a notification is missed. A failed lookup
// serves the request unrouted and is retried after flagKillsRetryAfter.
type EdgeFlagCache struct {
	load EdgeFlagLoader
	now  func() time.Time

	mu         sync.Mutex
	generation uint64
	entries    map[string]edgeFlagEntry
}

// NewEdgeFlagCache returns a cache over load; nil disables edge routing.
func NewEdgeFlagCache(load EdgeFlagLoader) *EdgeFlagCache {
	return &EdgeFlagCache{load: load, now: time.Now, entries: map[string]edgeFlagEntry{}}
}

// Bundle returns the edge routes of appID's environment.
func (c *EdgeFlagCache) Bundle(ctx context.Context, appID, environment string) flags.Bundle {
	if c == nil || c.load == nil || appID == "" {
		return flags.Bundle{}
	}
	key := appID + "\x00" + environment
	now := c.now()
	c.mu.Lock()
	entry, ok := c.entries[key]
	generation := c.generation
	c.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.bundle
	}
	lookupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flagKillsLookupBudget)
	bundle, err := c.load(lookupCtx, appID, environment)
	cancel()
	entry = edgeFlagEntry{expires: now.Add(flagKillsTTL)}
	if err != nil {
		entry.expires = now.Add(flagKillsRetryAfter)
	} else {
		entry.bundle = bundle
	}
	c.mu.Lock()
	if c.generation == generation {
		if len(c.entries) >= flagKillsMaxEntries {
			c.entries = map[string]edgeFlagEntry{}
		}
		c.entries[key] = entry
	}
	c.mu.Unlock()
	return entry.bundle
}

// InvalidateAll drops every cached app environment.
func (c *EdgeFlagCache) InvalidateAll() {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.generation++
	c.entries = map[string]edgeFlagEntry{}
	c.mu.Unlock()
}

// WithEdgeFlagLoader enables edge flag routing in the backend.
func (b *PGBackend) WithEdgeFlagLoader(load EdgeFlagLoader) *PGBackend {
	b.edgeFlags = NewEdgeFlagCache(load)
	return b
}

// EdgeFlagBundle implements edgeFlagSource.
func (b *PGBackend) EdgeFlagBundle(ctx context.Context, appID, environment string) flags.Bundle {
	if b == nil {
		return flags.Bundle{}
	}
	return b.edgeFlags.Bundle(ctx, appID, environment)
}

// edgeFlagSource is the optional backend capability edge routing reads.
type edgeFlagSource interface {
	EdgeFlagBundle(ctx context.Context, appID, environment string) flags.Bundle
}

// WithEdgeFlags installs the edge_flags platform flag, which releases edge
// routing by plan. Without it no request is routed.
func (h *Handler) WithEdgeFlags(release *platformflags.Set) *Handler {
	h.edgeFlagsRelease = release
	return h
}

type edgeFlagEvidenceKey struct{}

// edgeFlagEvidence returns the decisions edge routing made for the request.
func edgeFlagEvidence(ctx context.Context) []flags.Evidence {
	evidence, _ := ctx.Value(edgeFlagEvidenceKey{}).([]flags.Evidence)
	return evidence
}

// flagEvidenceWithEdge adds the request's edge decisions to the evidence the
// app reported; on any error the app's evidence stands alone.
func flagEvidenceWithEdge(ctx context.Context, appEvidence string) string {
	edge := edgeFlagEvidence(ctx)
	if len(edge) == 0 {
		return appEvidence
	}
	merged, err := flags.MergeEvidence(appEvidence, edge)
	if err != nil {
		return appEvidence
	}
	return merged
}

// applyEdgeFlags routes r by the app environment's edge flags and returns
// the request the rest of the pipeline serves, plus the visitor Set-Cookie
// value it wrote ("" when none), which a cached response must not keep.
//
// The visitor cookie is the platform's, not the app's: it is removed from
// every request, so the app never sees it and the response cache does not
// treat the visitor as signed in.
func (h *Handler) applyEdgeFlags(w http.ResponseWriter, r *http.Request, app App) (*http.Request, string) {
	visitor := edgeVisitorFrom(r)
	stripEdgeVisitorCookie(r)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return r, ""
	}
	// Named environments and deployment previews route by their own
	// environment; a PR preview token is not an environment and is not routed.
	environment := app.PinnedDeploymentScope
	if environment == "" && app.Scope != "" {
		return r, ""
	}
	if h.edgeFlagsRelease == nil || !h.edgeFlagsRelease.Enabled(platformflags.EdgeFlags, platformflags.Subject{AccountID: app.AccountID, Plan: string(app.Plan)}) {
		return r, ""
	}
	source, ok := h.backend.(edgeFlagSource)
	if !ok {
		return r, ""
	}
	bundle := source.EdgeFlagBundle(r.Context(), app.ID, environment)
	if len(bundle.Flags) == 0 {
		return r, ""
	}
	flag, route, ok := flags.EdgeMatch(bundle.Config, r.URL.Path)
	if !ok {
		return r, ""
	}
	fallback, _ := flag.Default.(string)
	customer := authenticatedFrom(r.Context()).PlatformTenantID
	subject, setCookie := "", ""
	if customer == "" {
		if visitor == "" {
			visitor, setCookie = issueEdgeVisitor(w)
		}
		customer, subject = visitor, visitor
	}
	attributes := map[string]string{}
	if country := h.lookupTelemetryCountry(r); country != telemetryUnknownDimension && country != "" {
		attributes["gregale.country"] = country
	}
	if family := normalizeTelemetryUserAgent(r.Header.Get("User-Agent")); family != telemetryUnknownDimension && family != "" {
		attributes["gregale.ua_family"] = family
	}
	decision := flags.EvaluateVariantWithContext(bundle, flag.Key, flags.EvaluationContext{Customer: customer, Subject: subject, Attributes: attributes}, fallback)
	// The variant depends on who asked: Cloudflare must not hand one
	// visitor's variant to the next.
	keepFromSharedCaches(w)
	variant, _ := decision.Value.(string)
	action, routed := route.Variants[variant]
	// A killed or missing flag decides from the fallback: it serves the
	// unrouted path, never the default variant's rewrite.
	if routed && decision.Source != "fallback" {
		target := flags.EdgeRewritePath(route, action, r.URL.Path)
		if !h.edgeRewriteGatedAlike(r, app, target) {
			// The target is gated differently from the path the client
			// asked for, and those gates have already run: serve the path
			// unrouted rather than skip a gate.
			return r, setCookie
		}
		// A copy, so requests made before this point keep the client's path.
		u := *r.URL
		u.Path, u.RawPath = target, ""
		r = r.WithContext(r.Context())
		r.URL = &u
	}
	evidence := append(edgeFlagEvidence(r.Context()), flags.Evidence{Decision: decision, Used: true})
	return r.WithContext(context.WithValue(r.Context(), edgeFlagEvidenceKey{}, evidence)), setCookie
}

// edgeRewriteGatedAlike reports whether target is gated exactly as the path
// the client sent: the declared-route gate admits it, and each path-scoped
// access rule (ip, geo, jwt, throttle, validate, limit, budget, maintenance,
// respond, waf, challenge) that matches one matches the other. Edge routing
// runs after those gates, so a rewrite may only move between paths they
// treat alike.
func (h *Handler) edgeRewriteGatedAlike(r *http.Request, app App, target string) bool {
	ctx, method := r.Context(), r.Method
	if !h.declaredRouteAllows(ctx, app, target, method) {
		return false
	}
	m := h.edgeRules
	if m == nil {
		return true
	}
	host, from := hostname(r.Host), publicRequestPath(r)
	checks := []func(path string) string{
		func(p string) string { return ruleID(m.MatchIP(ctx, host, p, method)) },
		func(p string) string { return ruleID(m.MatchGeo(ctx, host, p, method)) },
		func(p string) string { return ruleID(m.MatchJWT(ctx, host, p, method)) },
		func(p string) string { return ruleID(m.MatchThrottle(ctx, host, p, method)) },
		func(p string) string {
			if v := m.MatchValidate(ctx, host, p, method); v != nil {
				return v.ID
			}
			return ""
		},
		func(p string) string {
			if v := m.MatchLimit(ctx, host, p, method); v != nil {
				return v.ID
			}
			return ""
		},
		func(p string) string {
			if v := m.MatchBudget(ctx, host, p, method); v != nil {
				return v.ID
			}
			return ""
		},
		func(p string) string {
			if v := m.MatchMaintenance(ctx, host, p, method); v != nil {
				return v.ID
			}
			return ""
		},
		func(p string) string {
			if v := m.MatchRespond(ctx, host, p, method); v != nil {
				return v.ID
			}
			return ""
		},
	}
	if waf, ok := m.(WAFEdgeRuleMatcher); ok {
		checks = append(checks, func(p string) string { return ruleID(waf.MatchWAF(ctx, host, p, method)) })
	}
	if challenge := h.challengeMatcher(); challenge != nil {
		checks = append(checks, func(p string) string { return ruleID(challenge.MatchChallenge(ctx, host, p, method)) })
	}
	for _, match := range checks {
		if match(from) != match(target) {
			return false
		}
	}
	return true
}

// ruleID is a matched gate rule's ID, "" when none matched.
func ruleID[R edgeGateRule](rule R) string {
	var zero R
	if rule == zero {
		return ""
	}
	return rule.edgeGateRuleID()
}

// declaredRouteAllows reports whether the app's declared-route policy (its
// environment's, for a pinned deployment) admits path: true when the app
// does not restrict routes, false when the policy cannot be read.
func (h *Handler) declaredRouteAllows(ctx context.Context, app App, path, method string) bool {
	if app.PinnedDeploymentScope != "" && h.declaredRoutes != nil {
		if resolver, ok := h.declaredRoutes.(interface {
			ResolveScopedRoutePolicy(context.Context, App) (App, error)
		}); ok {
			resolved, err := resolver.ResolveScopedRoutePolicy(ctx, app)
			if err != nil {
				return false
			}
			app = resolved
		}
	}
	if !app.OnlyAllowDeclaredRoutes {
		return true
	}
	if h.declaredRoutes == nil {
		return false
	}
	allowed, err := h.declaredRoutes.MatchDeclaredRoute(ctx, app, path, method)
	return err == nil && allowed
}

// edgeVisitorFrom returns the anonymous visitor's id from its cookie, or ""
// when the request has none or an invalid one.
func edgeVisitorFrom(r *http.Request) string {
	if c, err := r.Cookie(EdgeVisitorCookie); err == nil {
		if id, err := uuid.Parse(c.Value); err == nil && id.Version() == 4 && id.String() == c.Value {
			return c.Value
		}
	}
	return ""
}

// issueEdgeVisitor sets a new visitor cookie and returns its id and the
// Set-Cookie value written.
func issueEdgeVisitor(w http.ResponseWriter) (string, string) {
	id := uuid.NewString()
	cookie := &http.Cookie{
		Name: EdgeVisitorCookie, Value: id, Path: "/", MaxAge: int(api.FlagsEdgeVisitorCookieMaxAge / time.Second),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	}
	value := cookie.String()
	w.Header().Add("Set-Cookie", value)
	return id, value
}

// stripEdgeVisitorCookie removes the visitor cookie and keeps every other.
func stripEdgeVisitorCookie(r *http.Request) {
	lines := r.Header.Values("Cookie")
	if len(lines) == 0 {
		return
	}
	var kept []string
	found := false
	for _, line := range lines {
		for _, part := range strings.Split(line, ";") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if name, _, _ := strings.Cut(part, "="); strings.TrimSpace(name) == EdgeVisitorCookie {
				found = true
				continue
			}
			kept = append(kept, part)
		}
	}
	if !found {
		return
	}
	r.Header.Del("Cookie")
	if len(kept) > 0 {
		r.Header.Set("Cookie", strings.Join(kept, "; "))
	}
}
