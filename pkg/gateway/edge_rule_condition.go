package gateway

// ADR-832: per-rule match conditions. Every compiled rule kind embeds an
// EdgeRuleCondition; ApplicableEdgeRules drops rules whose condition does not
// hold for the request before the kind's first-match pick runs, so a
// condition behaves exactly like one more selector on every kind.

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// EdgeRuleCondition is embedded in every compiled rule kind. A nil Match
// applies the rule to every request its selectors pick.
type EdgeRuleCondition struct {
	Match *api.EdgeRuleMatchProgram
	// RuleID / AppID identify the rule for ADR-830 hit counts; LogOnly marks
	// a log-mode rule, which is matched and counted but never acts.
	RuleID  string
	AppID   string
	LogOnly bool
}

func (c EdgeRuleCondition) edgeRuleMatch() *api.EdgeRuleMatchProgram { return c.Match }
func (c EdgeRuleCondition) edgeRuleLogOnly() bool                    { return c.LogOnly }
func (c EdgeRuleCondition) edgeRuleIdentity() (string, string)       { return c.RuleID, c.AppID }

type edgeRuleConditional interface {
	edgeRuleMatch() *api.EdgeRuleMatchProgram
	edgeRuleLogOnly() bool
	edgeRuleIdentity() (string, string)
}

// EdgeRuleHitRecorder counts rule matches (ADR-830). Implementations must be
// cheap and non-blocking: they run on the request path.
type EdgeRuleHitRecorder interface {
	RecordEdgeRuleHit(ruleID, appID string, logged bool)
}

type edgeRuleMatchContextKey struct{}

// EdgeRuleMatchContext is the per-request data conditions read. Country is
// resolved at most once, and only when a condition needs it.
type EdgeRuleMatchContext struct {
	Host     string
	Headers  http.Header
	Query    url.Values
	ClientIP net.IP // nil unless the single trusted forwarded hop parsed

	countryOnce sync.Once
	lookup      func(net.IP) string
	country     string

	// ADR-830: hit recording, deduplicated per request (a kind can be looked
	// up more than once while serving one request).
	hits     EdgeRuleHitRecorder
	seenMu   sync.Mutex
	seenHits map[string]struct{}
}

// NewEdgeRuleMatchContext builds the request snapshot. lookup resolves a
// country for the trusted client IP; nil means no GeoIP database, so
// conditions on country see it as absent. hits may be nil (no counting).
func NewEdgeRuleMatchContext(r *http.Request, clientIP net.IP, lookup func(net.IP) string, hits EdgeRuleHitRecorder) *EdgeRuleMatchContext {
	return &EdgeRuleMatchContext{
		Host: hostname(r.Host), Headers: r.Header, Query: r.URL.Query(),
		ClientIP: clientIP, lookup: lookup, hits: hits,
	}
}

func (m *EdgeRuleMatchContext) recordHit(ruleID, appID string, logged bool) {
	if m == nil || m.hits == nil || ruleID == "" {
		return
	}
	m.seenMu.Lock()
	if m.seenHits == nil {
		m.seenHits = map[string]struct{}{}
	}
	_, seen := m.seenHits[ruleID]
	m.seenHits[ruleID] = struct{}{}
	m.seenMu.Unlock()
	if !seen {
		m.hits.RecordEdgeRuleHit(ruleID, appID, logged)
	}
}

// ObserveEdgeRuleMatch counts the enforced rule a kind lookup selected and
// the first log-mode rule of that kind that matched, then returns the
// enforced one. Log-mode rules never act (ADR-830).
func ObserveEdgeRuleMatch[T any](ctx context.Context, enforced, logged *T) *T {
	m := edgeRuleMatchContextFrom(ctx)
	if m == nil || m.hits == nil {
		return enforced
	}
	if c, ok := any(enforced).(edgeRuleConditional); ok && enforced != nil {
		id, app := c.edgeRuleIdentity()
		m.recordHit(id, app, false)
	}
	if c, ok := any(logged).(edgeRuleConditional); ok && logged != nil {
		id, app := c.edgeRuleIdentity()
		m.recordHit(id, app, true)
	}
	return enforced
}

func (m *EdgeRuleMatchContext) resolvedCountry() string {
	m.countryOnce.Do(func() {
		if m.lookup != nil && m.ClientIP != nil {
			m.country = m.lookup(m.ClientIP)
		}
	})
	return m.country
}

// edgeRuleCountryLookup adapts the configured GeoIP reader for conditions.
// A lookup error or missing record leaves the country absent.
func (h *Handler) edgeRuleCountryLookup() func(net.IP) string {
	if h.geoReader == nil {
		return nil
	}
	return func(ip net.IP) string {
		country, found, err := h.geoReader.Lookup(ip)
		if err != nil || !found {
			return ""
		}
		return country
	}
}

// WithEdgeRuleMatchContext attaches the request snapshot conditions read.
func WithEdgeRuleMatchContext(ctx context.Context, m *EdgeRuleMatchContext) context.Context {
	return context.WithValue(ctx, edgeRuleMatchContextKey{}, m)
}

func edgeRuleMatchContextFrom(ctx context.Context) *EdgeRuleMatchContext {
	m, _ := ctx.Value(edgeRuleMatchContextKey{}).(*EdgeRuleMatchContext)
	return m
}

// ApplicableEdgeRules is OwnedEdgeRules plus ADR-832 conditions: it keeps the
// owner's rules whose condition holds for this request. requestPath and
// method are the values the kind's selectors see. When no rule carries a
// condition the owned slice is returned without building a snapshot. With no
// match context attached (callers outside the request path), conditions see
// only the path and method, and every other field is absent.
func ApplicableEdgeRules[T any](ctx context.Context, rules []T, account func(*T) string, requestPath, method string) []T {
	return filterEdgeRules(ctx, rules, account, requestPath, method, false)
}

// LoggedEdgeRules is ApplicableEdgeRules for log-mode rules only (ADR-830):
// the owner's log-mode rules whose condition holds. It returns nil at once
// when the slice has no log-mode rule.
func LoggedEdgeRules[T any](ctx context.Context, rules []T, account func(*T) string, requestPath, method string) []T {
	for i := range rules {
		if c, ok := any(&rules[i]).(edgeRuleConditional); ok && c.edgeRuleLogOnly() {
			return filterEdgeRules(ctx, rules, account, requestPath, method, true)
		}
	}
	return nil
}

func filterEdgeRules[T any](ctx context.Context, rules []T, account func(*T) string, requestPath, method string, logOnly bool) []T {
	owned := OwnedEdgeRules(ctx, rules, account)
	needsFilter := false
	for i := range owned {
		c, ok := any(&owned[i]).(edgeRuleConditional)
		if ok && (c.edgeRuleMatch() != nil || c.edgeRuleLogOnly() != logOnly) {
			needsFilter = true
			break
		}
	}
	if !needsFilter && !logOnly {
		return owned
	}
	var input api.EdgeRuleMatchInput
	inputReady := false
	out := make([]T, 0, len(owned))
	for i := range owned {
		c, ok := any(&owned[i]).(edgeRuleConditional)
		if ok && c.edgeRuleLogOnly() != logOnly {
			continue
		}
		if ok && c.edgeRuleMatch() != nil {
			if !inputReady {
				input = edgeRuleMatchInputFor(ctx, requestPath, method)
				inputReady = true
			}
			if !c.edgeRuleMatch().Matches(input) {
				continue
			}
		}
		out = append(out, owned[i])
	}
	return out
}

func edgeRuleMatchInputFor(ctx context.Context, requestPath, method string) api.EdgeRuleMatchInput {
	input := api.EdgeRuleMatchInput{Path: requestPath, Method: method}
	if m := edgeRuleMatchContextFrom(ctx); m != nil {
		input.Host, input.Headers, input.Query, input.ClientIP = m.Host, m.Headers, m.Query, m.ClientIP
		input.Country = m.resolvedCountry()
	}
	return input
}
