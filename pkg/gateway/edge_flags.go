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
// the request the rest of the pipeline serves.
func (h *Handler) applyEdgeFlags(w http.ResponseWriter, r *http.Request, app App) *http.Request {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		return r
	}
	if h.edgeFlagsRelease == nil || !h.edgeFlagsRelease.Enabled(platformflags.EdgeFlags, platformflags.Subject{AccountID: app.AccountID, Plan: string(app.Plan)}) {
		return r
	}
	source, ok := h.backend.(edgeFlagSource)
	if !ok {
		return r
	}
	bundle := source.EdgeFlagBundle(r.Context(), app.ID, app.Scope)
	if len(bundle.Flags) == 0 {
		return r
	}
	flag, route, ok := flags.EdgeMatch(bundle.Config, r.URL.Path)
	if !ok {
		return r
	}
	fallback, _ := flag.Default.(string)
	customer := authenticatedFrom(r.Context()).PlatformTenantID
	subject := ""
	if customer == "" {
		customer = edgeVisitor(w, r)
		subject = customer
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
	if action, ok := route.Variants[variant]; ok {
		r.URL.Path = flags.EdgeRewritePath(route, action, r.URL.Path)
		r.URL.RawPath = ""
	}
	evidence := append(edgeFlagEvidence(r.Context()), flags.Evidence{Decision: decision, Used: true})
	return r.WithContext(context.WithValue(r.Context(), edgeFlagEvidenceKey{}, evidence))
}

// edgeVisitor returns the anonymous visitor's id from its cookie, issuing a
// new one when the request has none or an invalid one.
func edgeVisitor(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(EdgeVisitorCookie); err == nil {
		if id, err := uuid.Parse(c.Value); err == nil && id.Version() == 4 && id.String() == c.Value {
			return c.Value
		}
	}
	id := uuid.NewString()
	http.SetCookie(w, &http.Cookie{
		Name: EdgeVisitorCookie, Value: id, Path: "/", MaxAge: int(api.FlagsEdgeVisitorCookieMaxAge / time.Second),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode,
	})
	return id
}
