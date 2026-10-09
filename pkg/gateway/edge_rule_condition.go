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
}

func (c EdgeRuleCondition) edgeRuleMatch() *api.EdgeRuleMatchProgram { return c.Match }

type edgeRuleConditional interface {
	edgeRuleMatch() *api.EdgeRuleMatchProgram
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
}

// NewEdgeRuleMatchContext builds the request snapshot. lookup resolves a
// country for the trusted client IP; nil means no GeoIP database, so
// conditions on country see it as absent.
func NewEdgeRuleMatchContext(r *http.Request, clientIP net.IP, lookup func(net.IP) string) *EdgeRuleMatchContext {
	return &EdgeRuleMatchContext{
		Host: hostname(r.Host), Headers: r.Header, Query: r.URL.Query(),
		ClientIP: clientIP, lookup: lookup,
	}
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
	owned := OwnedEdgeRules(ctx, rules, account)
	conditional := false
	for i := range owned {
		if c, ok := any(&owned[i]).(edgeRuleConditional); ok && c.edgeRuleMatch() != nil {
			conditional = true
			break
		}
	}
	if !conditional {
		return owned
	}
	input := api.EdgeRuleMatchInput{Path: requestPath, Method: method}
	if m := edgeRuleMatchContextFrom(ctx); m != nil {
		input.Host, input.Headers, input.Query, input.ClientIP = m.Host, m.Headers, m.Query, m.ClientIP
		input.Country = m.resolvedCountry()
	}
	out := make([]T, 0, len(owned))
	for i := range owned {
		if c, ok := any(&owned[i]).(edgeRuleConditional); ok && !c.edgeRuleMatch().Matches(input) {
			continue
		}
		out = append(out, owned[i])
	}
	return out
}
