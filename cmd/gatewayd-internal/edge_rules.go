// Edge-rule matcher wiring for gatewayd-internal (ADR-091 / issue #561,
// PR 3 + PR 4 + PR 5). The cache primitive + interfaces live in pkg/gateway/edge_rules.go;
// this file is the production seam where `state.EdgeRule → gateway.EdgeRule*Resolved`
// happens, the pg_notify invalidation loop in cmd/gatewayd-internal/backend.go
// calls Reset() on, and the handler calls Match* on.
//
// PR 3 shipped `kind=route`. PR 4 widens with kind=rewrite, kind=redirect,
// kind=headers — three new Match* methods on the same gatewaydEdgeRules
// struct, three new compile* helpers. The cache is a single per-host
// hostEntry (pkg/gateway/edge_rules.go) holding all four kinds' compiled
// slices; recompilation happens once on a miss for any kind (the SQL
// roundtrip dominates, so paying the compile cost four times is
// irrelevant). The single pg_notify invalidation channel
// (`db.NotifyEdgeRuleChanged`) covers all kinds wholesale.
//
// PR 5 widens with kind=cors / kind=jwt / kind=ip on the same shape —
// three more Match* methods, three more compile* helpers, and the
// HostEntry widens to 7 slots (still one SQL pass on a cache miss).

package main

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"path"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

// edgeRuleStore is the slice of state.Store the matcher needs.
// Pinning the interface keeps cmd/gatewayd-internal free of any
// reverse dep on the full state.Store surface so tests can inject
// a tiny fake that returns canned rule slices.
//
// GetCorsPresetByID (issue #975 #4 PR-B / ADR-129 D3) is the
// compile-side preset resolver. The compile path calls this for
// every kind=cors rule that stamps cors_preset_id; the merge
// helper (state.MergeCorsPresetIntoRule) produces the resolved
// action and re-validates the *+credentials footgun. A
// cross-tenant IDOR (preset owned by a different account) maps
// to ErrNotFound; the compile path records the miss and the
// rule does not match. Mirrors the per-rule lookup the apid
// PATCH path uses at handlers_cors_presets.go:158-163.
type edgeRuleStore interface {
	MatchEdgeRulesForHost(ctx context.Context, host string) ([]state.EdgeRule, error)
	GetCorsPresetByID(ctx context.Context, accountID, id string) (state.CorsPreset, error)
}

// gatewaydEdgeRules is the production gateway.EdgeRuleMatcher impl.
// Per-host cache + state.Store-backed loader + a no-op auditor thin
// wrapper. nil-safe (the gateway handler skips Match* when
// h.edgeRules == nil; this type is never nil because cmd/gatewayd-internal/run.go
// always wires one before the listener accepts).
//
// validate holds the pkg/edgevalidate adapter (set in PR-B).
// compileValidateRules calls CompileSchema on every kind=validate
// rule so the hot path never sees a cold cache. nil-safe: when
// nil, kind=validate rules compile-then-drop (the rule is
// silently skipped — same posture as a path-glob parse error).
// The cmd-side run.go wires a non-nil adapter in production.
//
// ADR-091 hardening PR-B: metrics carries the Prometheus counter
// registry the compile* helpers increment when a rule is dropped at
// parse time (gateway_edge_rule_compile_error_total{kind}). nil
// is safe — the compile helpers guard before incrementing.
type gatewaydEdgeRules struct {
	store    edgeRuleStore
	cache    *gateway.EdgeRuleCache
	log      *slog.Logger
	validate validateCompiler
	metrics  *gateway.Metrics
}

// validateCompiler is the surface compileValidateRules needs from
// the cmd-side adapter (cmd/gatewayd-internal/edge_validate.go).
// We narrow the seam so this file stays free of pkg/edgevalidate;
// the adapter file provides the concrete impl.
//
// Note: this is deliberately a *small* projection of the
// pkg/edgevalidate.Manager surface. CompileSchema is the only
// method compileValidateRules needs at load time — Validate
// itself is called from the handler-side applier via the
// gateway.Validator interface (handler.go::WithValidator setter),
// not through this matcher.
type validateCompiler interface {
	CompileSchema(schema []byte, rejectUnknownFields bool) (schemaDigest [32]byte, err error)
}

// newGatewaydEdgeRules builds the matcher with the standard
// 10,000-entry LRU capacity (pkg/gateway/edgeRuleCacheCap). log
// must be non-nil so loader failures have somewhere to land.
// validate may be nil (validate kind disabled; kind=validate rules
// are silently dropped at compile time, same posture as a
// path-glob parse error).
// metrics may be nil (unit tests pass nil; production wires the
// gatewayd-internal Prometheus registry via run.go).
func newGatewaydEdgeRules(store edgeRuleStore, log *slog.Logger, validate validateCompiler, metrics *gateway.Metrics) *gatewaydEdgeRules {
	return &gatewaydEdgeRules{
		store:    store,
		cache:    gateway.NewEdgeRuleCache(gateway.EdgeRuleCacheCap),
		log:      log,
		validate: validate,
		metrics:  metrics,
	}
}

// loadHost compiles every kind's slice for host into a fresh
// hostEntry. Shared by all Match* methods so a cache miss for
// ANY kind recompiles all kinds in one pass (the SQL roundtrip
// dominates; the path.Match walks are irrelevant). Returns
// nil + nil error on an empty store response.
//
// parseErrs is the aggregated parse-error list across all compile*
// calls — the caller logs each at WARN so an operator can
// diagnose a malformed glob or CIDR. The malformed rules are
// dropped from their respective compiled slices. PR 5 widens the
// list to include malformed CIDR parse errors from compileIPRules
// (reuses the same PathGlobError tuple shape); PR-B widens it
// to include kind=validate compile errors; the maintenance
// amendment widens it again to include kind=maintenance.
func (g *gatewaydEdgeRules) loadHost(ctx context.Context, host string) (*gateway.HostEntry, error) {
	generation := g.cache.Generation()
	storeRules, err := g.store.MatchEdgeRulesForHost(ctx, host)
	if err != nil {
		return nil, err
	}
	route, routeErrs := compileRouteRules(storeRules)
	rewrite, rewriteErrs := compileRewriteRules(storeRules)
	redirect, redirectErrs := compileRedirectRules(storeRules)
	headers, headersErrs := compileHeadersRules(storeRules)
	cors, corsErrs := g.compileCORSRules(ctx, storeRules)
	jwt, jwtErrs := compileJWTRules(storeRules)
	ip, ipErrs := compileIPRules(storeRules)
	validate, validateErrs := g.compileValidateRules(storeRules)
	limit, limitErrs := compileLimitRules(storeRules)
	maintenance, maintenanceErrs := compileMaintenanceRules(storeRules)
	geo, geoErrs := compileGeoRules(storeRules)
	throttle, throttleErrs := compileThrottleRules(storeRules)
	budget, budgetErrs := compileBudgetRules(storeRules)
	cache, cacheErrs := compileCacheRules(storeRules)
	entry := &gateway.HostEntry{
		Route:       route,
		Rewrite:     rewrite,
		Redirect:    redirect,
		Headers:     headers,
		CORS:        cors,
		JWT:         jwt,
		IP:          ip,
		Validate:    validate,
		Limit:       limit,
		Maintenance: maintenance,
		Geo:         geo,
		Throttle:    throttle,
		Budget:      budget,
		Cache:       cache,
	}
	parseErrs := append(routeErrs, rewriteErrs...)
	parseErrs = append(parseErrs, redirectErrs...)
	parseErrs = append(parseErrs, headersErrs...)
	parseErrs = append(parseErrs, corsErrs...)
	parseErrs = append(parseErrs, jwtErrs...)
	parseErrs = append(parseErrs, ipErrs...)
	parseErrs = append(parseErrs, validateErrs...)
	parseErrs = append(parseErrs, limitErrs...)
	parseErrs = append(parseErrs, maintenanceErrs...)
	parseErrs = append(parseErrs, geoErrs...)
	parseErrs = append(parseErrs, throttleErrs...)
	parseErrs = append(parseErrs, budgetErrs...)
	parseErrs = append(parseErrs, cacheErrs...)
	if len(parseErrs) > 0 {
		entry.PathGlobErrs = parseErrs
	}
	// PR-B: surface per-rule compile errors to Prometheus so the
	// §12 dashboard chip "edge rule compile errors" reflects every
	// dropped rule. Incrementing once per error here keeps the
	// counter equal to the number of broken rules (not the number
	// of hosts that had any broken rules). loadHost is the natural
	// choke point — every Match* falls through it on a cache miss,
	// so we get one tick per dropped rule across the whole fleet.
	if g.metrics != nil {
		for range routeErrs {
			g.metrics.ObserveEdgeRuleCompileError("route")
		}
		for range rewriteErrs {
			g.metrics.ObserveEdgeRuleCompileError("rewrite")
		}
		for range redirectErrs {
			g.metrics.ObserveEdgeRuleCompileError("redirect")
		}
		for range headersErrs {
			g.metrics.ObserveEdgeRuleCompileError("headers")
		}
		for range corsErrs {
			g.metrics.ObserveEdgeRuleCompileError("cors")
		}
		for range jwtErrs {
			g.metrics.ObserveEdgeRuleCompileError("jwt")
		}
		for range ipErrs {
			g.metrics.ObserveEdgeRuleCompileError("ip")
		}
		for range validateErrs {
			g.metrics.ObserveEdgeRuleCompileError("validate")
		}
		for range limitErrs {
			g.metrics.ObserveEdgeRuleCompileError("limit")
		}
		for range maintenanceErrs {
			g.metrics.ObserveEdgeRuleCompileError("maintenance")
		}
		for range geoErrs {
			g.metrics.ObserveEdgeRuleCompileError("geo")
		}
		for range budgetErrs {
			g.metrics.ObserveEdgeRuleCompileError("budget")
		}
		for range throttleErrs {
			g.metrics.ObserveEdgeRuleCompileError("throttle")
		}
	}
	g.cache.PutIfGeneration(host, entry, generation)
	return entry, nil
}

// MatchRoute returns the highest-priority `kind=route` rule whose
// host, path, and method match the inbound request, or nil if no
// rule applies. The cache is checked first; a miss falls through
// to MatchEdgeRulesForHost on the store and the resulting slice is
// compiled + Put back into the cache. Compiled rules are sorted
// priority-ASC so pickFirstMatch (pkg/gateway) returns the
// lowest-numbered match — same shape as the existing
// RouteResolver/RouteCache hot path.
func (g *gatewaydEdgeRules) MatchRoute(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.Get(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Route
	}
	return gateway.PickFirstRouteMatch(rules, requestPath, method)
}

// MatchRewrite returns the highest-priority `kind=rewrite` rule
// whose host, path, and method match, or nil. Same cache primitive
// as MatchRoute — a miss for rewrite triggers the same loadHost
// pass that also recompiles route / redirect / headers.
func (g *gatewaydEdgeRules) MatchRewrite(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleRewriteResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetRewrite(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Rewrite
	}
	return gateway.PickFirstRewriteMatch(rules, requestPath, method)
}

// MatchRedirect returns the highest-priority `kind=redirect` rule
// whose host, path, and method match, or nil. Same cache primitive.
func (g *gatewaydEdgeRules) MatchRedirect(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleRedirectResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetRedirect(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Redirect
	}
	return gateway.PickFirstRedirectMatch(rules, requestPath, method)
}

// MatchHeaders returns the highest-priority `kind=headers` rule
// whose host, path, and method match, or nil. Same cache primitive.
func (g *gatewaydEdgeRules) MatchHeaders(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleHeadersResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetHeaders(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Headers
	}
	return gateway.PickFirstHeadersMatch(rules, requestPath, method)
}

// MatchCORS returns the highest-priority `kind=cors` rule whose
// host, path, and method match, or nil (ADR-091 D4). Same cache
// primitive as MatchHeaders — a cache miss recompiles all seven
// kinds in one SQL pass.
func (g *gatewaydEdgeRules) MatchCORS(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleCORSResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetCORS(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.CORS
	}
	return gateway.PickFirstCORSMatch(rules, requestPath, method)
}

// MatchJWT returns the highest-priority `kind=jwt` rule whose
// host, path, and method match, or nil (ADR-091 D4). Same cache
// primitive. The applier (handler.go::applyEdgeRuleJWT) is
// responsible for the actual token verify via pkg/edgejwks —
// this method only finds the rule; the verifier call lives
// outside the cache primitive.
func (g *gatewaydEdgeRules) MatchJWT(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleJWTResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetJWT(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.JWT
	}
	return gateway.PickFirstJWTMatch(rules, requestPath, method)
}

// MatchIP returns the highest-priority `kind=ip` rule whose host,
// path, and method match, or nil (ADR-091 D4). Same cache
// primitive. The applier (handler.go::applyEdgeRuleIP) reads the
// single trusted XFF entry (gatewayd-public's RemoteAddr) and
// evaluates against this rule's Allow/Deny CIDR lists.
func (g *gatewaydEdgeRules) MatchIP(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleIPResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetIP(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.IP
	}
	return gateway.PickFirstIPMatch(rules, requestPath, method)
}

// MatchValidate returns the highest-priority `kind=validate` rule
// whose host, path, and method match, or nil (PR-B). Same cache
// primitive as MatchIP — a cache miss recompiles all eight kinds
// in one SQL pass. The applier (handler.go::applyEdgeRuleValidate)
// buffers the request body, restores r.Body for the proxy leg,
// and consults the cmd-side validator via gateway.Validator.
// This method only finds the rule.
func (g *gatewaydEdgeRules) MatchValidate(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleValidateResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetValidate(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Validate
	}
	return gateway.PickFirstValidateMatch(rules, requestPath, method)
}

// MatchLimit returns the highest-priority `kind=limit` rule whose
// host, path, and method match, or nil (ADR-091 D24). Same cache
// primitive as MatchValidate — a miss for limit triggers the same
// loadHost pass that also recompiles route/rewrite/.../validate.
// The applier (handler.go::applyEdgeRuleLimit, §4.1.2.8c) installs
// http.MaxBytesReader on r.Body at the per-rule cap and emits 413
// request_too_large on the Content-Length fast path before any
// bytes are buffered. This method only finds the rule.
func (g *gatewaydEdgeRules) MatchLimit(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleLimitResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetLimit(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Limit
	}
	return gateway.PickFirstLimitMatch(rules, requestPath, method)
}

// MatchMaintenance returns the highest-priority `kind=maintenance`
// rule whose host, path, and method match the inbound request, or
// nil if no rule applies. Same primitive as MatchLimit — a miss for
// maintenance triggers the same loadHost pass that also recompiles
// route/rewrite/.../limit. The applier (handler.go::applyEdgeRuleMaintenance,
// §4.1.2.13) short-circuits with 503 + Retry-After BEFORE auth,
// BEFORE wake. Coarse-gate apps.maintenance_mode fires BEFORE this
// method is consulted (handler.go::applyAppsMaintenanceMode), so the
// fine-grained rule only sees requests that survived the coarse gate.
func (g *gatewaydEdgeRules) MatchMaintenance(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleMaintenanceResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetMaintenance(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Maintenance
	}
	return gateway.PickFirstMaintenanceMatch(rules, requestPath, method)
}

// MatchGeo returns the highest-priority `kind=geo` rule whose
// host, path, and method match, or nil (ADR-091 D21). Same cache
// primitive as MatchIP. The applier (handler.go::applyEdgeRuleGeo)
// reads the trusted XFF entry, looks up the country via the
// configured pkg/geoip.Reader, and evaluates against this rule's
// Allow/Deny sets. The lookup itself happens in the applier — the
// matcher stays a pure path/methods filter so the cache hit path
// stays cheap (the DB-IP mmap lookup is the expensive step).
func (g *gatewaydEdgeRules) MatchGeo(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleGeoResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetGeo(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Geo
	}
	return gateway.PickFirstGeoMatch(rules, requestPath, method)
}

// MatchThrottle returns the highest-priority `kind=throttle` rule
// whose host, path, and method match, or nil (ADR-091 D20.5
// amendment, issue #881). Same cache primitive as MatchLimit — a
// miss triggers the same loadHost pass that also recompiles the
// other 11 kinds. The applier (handler.go::applyEdgeRuleThrottle)
// constructs the rule-scoped bucket key (AppID+"\x00"+ID) and
// decrements via pkg/gateway.(*Limiter).AllowToken before the
// route hits the body — runs AFTER Limit (so the body-cap 413
// still consumes a bucket) and BEFORE Validate (so the
// schema-gate 422 does not cost a bucket).
func (g *gatewaydEdgeRules) MatchThrottle(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleThrottleResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetThrottle(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Throttle
	}
	return gateway.PickFirstThrottleMatch(rules, requestPath, method)
}

// MatchBudget is the ADR-093 matcher for the kind=budget subset.
// Same shape as MatchLimit: cache hit returns immediately; cache
// miss triggers loadHost which compiles every kind's slice in one
// SQL roundtrip (the SQL dominates; the kind-specific compile
// walks are irrelevant). On cache miss with no kind=budget rule
// the cache still records a nil Budget slice, so a future
// MatchBudget on the same host hits the cache instead of
// reloading. Returns the highest-priority matching rule or nil on
// miss; the applier (handler.go::applyEdgeRuleBudget) falls back
// to the plan-level default budget on nil.
func (g *gatewaydEdgeRules) MatchBudget(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleBudgetResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetBudget(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Budget
	}
	return gateway.PickFirstBudgetMatch(rules, requestPath, method)
}

// MatchCache is the ADR-122 matcher for the kind=cache subset.
// Same shape as MatchBudget: cache hit returns immediately;
// cache miss triggers loadHost which compiles every kind's
// slice in one SQL roundtrip. On cache miss with no
// kind=cache rule the cache still records a nil Cache slice,
// so a future MatchCache on the same host hits the cache
// instead of reloading. Returns the highest-priority matching
// rule or nil on miss; the applier
// (handler.go::applyEdgeRuleCache) falls back to "no cache
// rule → cache miss" on nil.
//
// The DeploymentID component of the cache key is NOT a
// per-rule field — the applier reads it from the live routed
// target at request time and threads it into the CacheKey
// when calling ResponseCache.Get. This keeps the matcher
// host-scoped (consistent with the rest of the per-kind
// matchers) without dragging the deployment picker into the
// rule cache.
func (g *gatewaydEdgeRules) MatchCache(ctx context.Context, host, requestPath, method string) *gateway.EdgeRuleCacheResolved {
	if g == nil || g.cache == nil {
		return nil
	}
	rules, hit := g.cache.GetCache(host)
	if !hit {
		entry, err := g.loadHost(ctx, host)
		if err != nil {
			if g.log != nil {
				g.log.Warn("edge rule loader failed; treating as miss", "host", host, "err", err)
			}
			return nil
		}
		g.warnPathGlobErrs(host, entry.PathGlobErrs)
		rules = entry.Cache
	}
	return gateway.PickFirstCacheMatch(rules, requestPath, method)
}

// Reset drops every cached entry. Called by the pg_notify loop in
// cmd/gatewayd-internal/backend.go on db.NotifyEdgeRuleChanged
// (mirrors PGBackend.FlushRoutes). Wholesale flush because the
// cache is per-host keyed and the pg_notify payload doesn't carry
// match_host (same posture as PR 3).
func (g *gatewaydEdgeRules) Reset() {
	if g == nil || g.cache == nil {
		return
	}
	g.cache.Reset()
}

// warnPathGlobErrs logs every path-glob parse error the loader
// returned, at WARN so an operator can diagnose a malformed glob.
// Errors are not surfaced to the customer (they see a clean 404).
func (g *gatewaydEdgeRules) warnPathGlobErrs(host string, errs []gateway.PathGlobError) {
	if g.log == nil || len(errs) == 0 {
		return
	}
	for _, pe := range errs {
		g.log.Warn("edge rule path glob parse error; rule dropped",
			"host", host, "rule_id", pe.RuleID, "glob", pe.Glob, "err", pe.Err)
	}
}

// compileRouteRules filters storeRules to the kind=route subset,
// compiles the per-rule filter tables, validates any path globs
// (review fix R3), and sorts priority-ASC so PickFirstRouteMatch
// returns the lowest-numbered match first. Empty rules slice in ->
// empty rules slice out (Put is a no-op per pkg/gateway/edge_rules.go).
//
// Path-glob validation: each rule's MatchPath is run through
// stdlib path.Match(glob, "") which returns an error for a
// malformed pattern (unmatched bracket, etc.) without depending
// on the specific request path. A malformed rule is dropped
// from the compiled slice AND the caller receives a parallel
// slice of (rule_id, glob, err) tuples via pathGlobErrors so
// an operator can diagnose the typo — a silent 404 (the prior
// behaviour) made the typo invisible.
//
// Returns (rules, []PathGlobError). Rules is sorted priority-ASC;
// PathGlobErrors is in input order. Nil PathGlobErrors when every
// glob parses cleanly.
func compileRouteRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindRoute {
			continue
		}
		if r.Action.Route == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		out = append(out, gateway.EdgeRuleResolved{
			ID:            r.ID,
			AccountID:     r.AccountID,
			AppID:         r.AppID,
			Priority:      r.Priority,
			PathGlob:      r.MatchPath,
			Methods:       buildMethodsMap(r.MatchMethods),
			TargetAppSlug: r.Action.Route.TargetAppSlug,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileRewriteRules mirrors compileRouteRules for kind=rewrite.
// Same filters (Enabled, Kind, action-nil), same path-glob validation,
// same priority-ASC sort. The compiled slice carries the From/To
// pair the handler's matchAndApplyRewrite uses to mutate r.URL.Path.
func compileRewriteRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleRewriteResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleRewriteResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindRewrite {
			continue
		}
		if r.Action.Rewrite == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		out = append(out, gateway.EdgeRuleRewriteResolved{
			ID:        r.ID,
			AccountID: r.AccountID,
			AppID:     r.AppID,
			Priority:  r.Priority,
			PathGlob:  r.MatchPath,
			Methods:   buildMethodsMap(r.MatchMethods),
			From:      r.Action.Rewrite.From,
			To:        r.Action.Rewrite.To,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileRedirectRules mirrors compileRouteRules for kind=redirect.
// StatusCode defaults to 302 (pkg/api/dto.go Validate already
// constrains to {301,302,307,308}, so any non-zero value reaching
// here is valid).
func compileRedirectRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleRedirectResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleRedirectResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindRedirect {
			continue
		}
		if r.Action.Redirect == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		status := r.Action.Redirect.StatusCode
		if status == 0 {
			status = 302 // http.StatusFound — pkg/api Validate already enforced the {301,302,307,308} set
		}
		out = append(out, gateway.EdgeRuleRedirectResolved{
			ID:         r.ID,
			AccountID:  r.AccountID,
			AppID:      r.AppID,
			Priority:   r.Priority,
			PathGlob:   r.MatchPath,
			Methods:    buildMethodsMap(r.MatchMethods),
			StatusCode: status,
			To:         r.Action.Redirect.To,
			Headers:    r.Action.Redirect.Headers,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileHeadersRules mirrors compileRouteRules for kind=headers.
// The compiled slice preserves the customer's declared op order
// (Cloudflare "first wins" semantics for `set`); both
// RequestHeaders and ResponseHeaders arrays are copied verbatim.
func compileHeadersRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleHeadersResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleHeadersResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindHeaders {
			continue
		}
		if r.Action.Headers == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		out = append(out, gateway.EdgeRuleHeadersResolved{
			ID:              r.ID,
			AccountID:       r.AccountID,
			AppID:           r.AppID,
			Priority:        r.Priority,
			PathGlob:        r.MatchPath,
			Methods:         buildMethodsMap(r.MatchMethods),
			RequestHeaders:  convertHeaderOps(r.Action.Headers.RequestHeaders),
			ResponseHeaders: convertHeaderOps(r.Action.Headers.ResponseHeaders),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileCORSRules mirrors compileRouteRules for kind=cors. The
// compiled slice carries the AllowOrigins / AllowMethods /
// AllowHeaders / ExposeHeaders / AllowCredentials / MaxAgeSeconds
// fields that the gateway's applyEdgeRuleCORS consults. apid-Validate
// already rejected AllowOrigins:["*"] + AllowCredentials:true
// (ADR-091 D12) so the gateway stamper can trust the input.
//
// Per-rule preset resolution (issue #975 #4 PR-B / ADR-129 D3):
// when r.Action.CORS.CorsPresetID is non-nil, the compile path
// looks up the preset via g.store.GetCorsPresetByID and resolves
// the merged action through state.MergeCorsPresetIntoRule. The
// merge helper re-validates the *+credentials footgun (defense
// in depth — the apid-Validate gate ran on create, but the
// preset may have been edited since). A missing preset (deleted
// by the customer between rule-create and rule-compile, the
// ON DELETE SET NULL FK cleared the rule's FK to NULL) is
// caught by the helper's ErrNotFound return — the rule is
// dropped from the compiled slice and the parse error is
// surfaced as cors_preset_not_found.
func (g *gatewaydEdgeRules) compileCORSRules(ctx context.Context, storeRules []state.EdgeRule) ([]gateway.EdgeRuleCORSResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleCORSResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindCORSA {
			continue
		}
		if r.Action.CORS == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		action := r.Action.CORS
		// Resolve the merged shape. The merge helper returns
		// the rule's values verbatim when no preset is stamped
		// (inline-only path), so the no-preset branch and the
		// with-preset branch share the same struct fields.
		var merged state.MergedCorsRuleAction
		if action.CorsPresetID != nil {
			preset, perr := g.store.GetCorsPresetByID(ctx, r.AccountID, *action.CorsPresetID)
			if perr != nil {
				// ErrNotFound: the preset was deleted (FK
				// ON DELETE SET NULL cleared the rule's FK
				// to NULL; this rule's JSONB mirror is
				// stale). Drop the rule from the compiled
				// slice and surface the parse error so the
				// operator can intervene.
				parseErrs = append(parseErrs, gateway.PathGlobError{
					RuleID: r.ID,
					Glob:   r.MatchPath,
					Err:    errors.New("cors_preset_not_found: the preset referenced by this rule has been deleted; re-save the rule or wire a new preset"),
				})
				continue
			}
			merged, perr = state.MergeCorsPresetIntoRule(r.AccountID, r.AppID, *action.CorsPresetID, state.CorsRuleOverride{
				AllowOrigins:     action.AllowOrigins,
				AllowMethods:     action.AllowMethods,
				AllowHeaders:     action.AllowHeaders,
				ExposeHeaders:    action.ExposeHeaders,
				AllowCredentials: action.AllowCredentials,
				MaxAgeSeconds:    action.MaxAgeSeconds,
			}, preset)
			if perr != nil {
				// ErrCorsWildcardWithCredentials: the merge
				// produced a *+credentials footgun (defense
				// in depth — the customer may have edited
				// the preset's allow_origins=["*"] without
				// the rule author seeing the change).
				// Drop the rule; the parse error surfaces
				// the same stable message the apid write
				// boundary uses (RFC 7807 code
				// cors_wildcard_with_credentials).
				parseErrs = append(parseErrs, gateway.PathGlobError{
					RuleID: r.ID,
					Glob:   r.MatchPath,
					Err:    errors.New("cors_wildcard_with_credentials: the merged preset + rule action cannot combine AllowCredentials: true with AllowOrigins: [\"*\"]"),
				})
				continue
			}
		} else {
			// Inline-only path: build the merged action
			// from the rule's fields directly.
			merged = state.MergedCorsRuleAction{
				PresetID:         "",
				AllowOrigins:     append([]string(nil), action.AllowOrigins...),
				AllowMethods:     append([]string(nil), action.AllowMethods...),
				AllowHeaders:     append([]string(nil), action.AllowHeaders...),
				ExposeHeaders:    append([]string(nil), action.ExposeHeaders...),
				AllowCredentials: action.AllowCredentials,
				MaxAgeSeconds:    action.MaxAgeSeconds,
			}
		}
		out = append(out, gateway.EdgeRuleCORSResolved{
			ID:               r.ID,
			AccountID:        r.AccountID,
			AppID:            r.AppID,
			Priority:         r.Priority,
			PathGlob:         r.MatchPath,
			Methods:          buildMethodsMap(r.MatchMethods),
			AllowOrigins:     merged.AllowOrigins,
			AllowMethods:     merged.AllowMethods,
			AllowHeaders:     merged.AllowHeaders,
			ExposeHeaders:    merged.ExposeHeaders,
			AllowCredentials: merged.AllowCredentials,
			MaxAgeSeconds:    merged.MaxAgeSeconds,
			PresetID:         merged.PresetID,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileJWTRules mirrors compileRouteRules for kind=jwt. The
// compiled slice carries the Issuer / Audience / JWKSURL /
// Algorithms / RequiredClaims fields. apid-Validate already
// ensured JWKSURL is https:// + not private/loopback/link-local
// (ADR-091 D10) and Algorithms is the closed {RS,ES}* vocabulary
// (D11). The actual token verify happens at handler.go via
// pkg/edgejwks.Verifier — this compile step only finds the rule.
func compileJWTRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleJWTResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleJWTResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindJWT {
			continue
		}
		if r.Action.JWT == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		action := r.Action.JWT
		// Defense-in-depth: apid-Validate already enforced the
		// HS* + RFC1918/loopback/link-local JWKS URL rules, but
		// the SQL hotfix path means we can't trust it. Drop
		// non-https URLs at compile so the gateway never even
		// sees them (matches PR 3 R3 path-glob re-validation).
		if !strings.HasPrefix(action.JWKSURL, "https://") {
			parseErrs = append(parseErrs, gateway.PathGlobError{
				RuleID: r.ID, Glob: action.JWKSURL,
				Err: errors.New("jwks_url must start with https://"),
			})
			continue
		}
		var audCopy []string
		if len(action.Audience) > 0 {
			audCopy = append(audCopy, action.Audience...)
		}
		var algCopy []string
		if len(action.Algorithms) > 0 {
			algCopy = append(algCopy, action.Algorithms...)
		}
		var claimsCopy map[string]string
		if len(action.RequiredClaims) > 0 {
			claimsCopy = make(map[string]string, len(action.RequiredClaims))
			for k, v := range action.RequiredClaims {
				claimsCopy[k] = v
			}
		}
		out = append(out, gateway.EdgeRuleJWTResolved{
			ID:             r.ID,
			AccountID:      r.AccountID,
			AppID:          r.AppID,
			Priority:       r.Priority,
			PathGlob:       r.MatchPath,
			Methods:        buildMethodsMap(r.MatchMethods),
			Issuer:         action.Issuer,
			Audience:       audCopy,
			JWKSURL:        action.JWKSURL,
			Algorithms:     algCopy,
			RequiredClaims: claimsCopy,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileIPRules mirrors compileRouteRules for kind=ip. The
// compiled slice carries pre-parsed *net.IPNet slices for both
// Allow and Deny so the gateway hot path never has to call
// net.ParseCIDR per-request. CIDR parse errors are surfaced via
// the PathGlobError channel so an operator sees the malformed
// row in slog (mirrors PR 3 R3 path-glob re-validation).
func compileIPRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleIPResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleIPResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindIP {
			continue
		}
		if r.Action.IP == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		action := r.Action.IP
		allow, allowErrs := parseCIDRs(action.Allow, r.ID)
		deny, denyErrs := parseCIDRs(action.Deny, r.ID)
		if len(allowErrs) > 0 || len(denyErrs) > 0 {
			parseErrs = append(parseErrs, allowErrs...)
			parseErrs = append(parseErrs, denyErrs...)
			continue
		}
		out = append(out, gateway.EdgeRuleIPResolved{
			ID:        r.ID,
			AccountID: r.AccountID,
			AppID:     r.AppID,
			Priority:  r.Priority,
			PathGlob:  r.MatchPath,
			Methods:   buildMethodsMap(r.MatchMethods),
			Allow:     allow,
			Deny:      deny,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileGeoRules mirrors compileIPRules for kind=geo (ADR-091
// D21). The action carries Allow / Deny as ISO 3166-1 alpha-2
// country codes rather than CIDR strings; the compile side
// uppercases + dedupes one more time before stashing into a
// map[string]struct{} for O(1) per-request membership checks.
//
// apid-Validate already drops reserved codes (AA, etc.) and
// enforces the ≤50-entry cardinality cap, so the compile side
// does not re-validate the wire shape — it just performs the
// shape conversion. A bad rule that slipped past the validator
// still routes correctly: an unknown country code simply never
// matches during the lookup (the DB-IP DB returns the customer's
// real country, which is not in the rule's set, so the rule's
// allow/deny both miss and the request flows through).
func compileGeoRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleGeoResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleGeoResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindGeo {
			continue
		}
		if r.Action.Geo == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		action := r.Action.Geo
		allow := compileGeoSet(action.Allow)
		deny := compileGeoSet(action.Deny)
		out = append(out, gateway.EdgeRuleGeoResolved{
			ID:        r.ID,
			AccountID: r.AccountID,
			AppID:     r.AppID,
			Priority:  r.Priority,
			PathGlob:  r.MatchPath,
			Methods:   buildMethodsMap(r.MatchMethods),
			Allow:     allow,
			Deny:      deny,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileGeoSet uppercases + dedupes a country-code list into
// a map[string]struct{} for O(1) membership checks. Returns
// nil (NOT an empty map) for an empty input so the runtime
// check `if len(rule.Allow) > 0` stays the canonical "is
// there an allowlist?" predicate.
func compileGeoSet(codes []string) map[string]struct{} {
	if len(codes) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(codes))
	for _, c := range codes {
		upper := strings.ToUpper(c)
		if upper == "" {
			continue
		}
		out[upper] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// parseCIDRs turns a []string CIDR list into a []*net.IPNet slice.
// Per-entry parse errors are returned as PathGlobError so the
// caller can log them and drop the rule. nil entries in the input
// are tolerated (an Allow: []CIDR is the canonical "no allowlist,
// only deny applies" posture).
func parseCIDRs(cidrs []string, ruleID string) ([]*net.IPNet, []gateway.PathGlobError) {
	if len(cidrs) == 0 {
		return nil, nil
	}
	out := make([]*net.IPNet, 0, len(cidrs))
	var parseErrs []gateway.PathGlobError
	for _, c := range cidrs {
		_, ipNet, err := net.ParseCIDR(c)
		if err != nil {
			parseErrs = append(parseErrs, gateway.PathGlobError{
				RuleID: ruleID, Glob: c, Err: err,
			})
			continue
		}
		out = append(out, ipNet)
	}
	if len(parseErrs) > 0 {
		return nil, parseErrs
	}
	return out, nil
}

// compileLimitRules mirrors compileIPRules for kind=limit. The
// compiled slice carries the two body caps the handler's
// applyEdgeRuleLimit consults. apid-Validate already enforced the
// non-zero + ≤-platform-cap clamps (pkg/api/dto.go::EdgeRuleLimitAction.Validate),
// but the SQL hotfix path means we can't trust the validator. This
// compile step is the defence-in-depth pass: a hot-fix that
// bypassed apid still gets clamped here, and a row with a
// non-positive MaxBodyBytes or a value > api.MaxRequestBodyBytes
// degrades to the platform cap (no 413-storm on bad input). The
// streaming cap is NOT clamped here — only the buffered cap is the
// load-bearing platform invariant.
//
// Like compileIPRules, the compile is a free function — it doesn't
// need any adapter state (unlike compileValidateRules which needs
// the schema cache). Out-of-bound values are clamped silently and
// a slog.Warn from the caller notes the rule ID; the customer
// never sees the warning (their rule still fires, just at the
// platform cap).
func compileLimitRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleLimitResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleLimitResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindLimit {
			continue
		}
		if r.Action.Limit == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		// Clamp the buffered cap to the platform ceiling.
		// Non-positive → api.MaxRequestBodyBytes (a 0 cap would
		// 413 every request, which is worse than the platform
		// default; the same defence-in-depth posture as
		// compileIPRules silently dropping malformed CIDRs).
		maxBody := r.Action.Limit.MaxBodyBytes
		if maxBody <= 0 || maxBody > api.MaxRequestBodyBytes {
			maxBody = api.MaxRequestBodyBytes
		}
		// Streaming cap: only clamp on the upper bound; 0 means
		// "no carve-out, fall back to MaxBodyBytes" (the applier
		// handles that). The negative check is defence-in-depth
		// against a malformed direct-DB write. The hard ceiling
		// matches api.RawStreamMaxRequestBytes (100 MiB) per
		// pkg/api/limits.go:1652 — the constant lands in #12.
		maxBodyStream := r.Action.Limit.MaxBodyBytesStreaming
		if maxBodyStream < 0 {
			maxBodyStream = 0
		}
		// RawStreamMaxRequestBytes is int64 (legacy raw-bridge
		// byte-count type); struct field is int — platform-int
		// width is fine, every box we ship is 64-bit and 100 MiB
		// fits comfortably.
		if int64(maxBodyStream) > api.RawStreamMaxRequestBytes {
			maxBodyStream = int(api.RawStreamMaxRequestBytes)
		}
		out = append(out, gateway.EdgeRuleLimitResolved{
			ID:                    r.ID,
			AccountID:             r.AccountID,
			AppID:                 r.AppID,
			Priority:              r.Priority,
			PathGlob:              r.MatchPath,
			Methods:               buildMethodsMap(r.MatchMethods),
			MaxBodyBytes:          maxBody,
			MaxBodyBytesStreaming: maxBodyStream,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileMaintenanceRules mirrors compileLimitRules for kind=maintenance.
// The compiled slice carries the per-rule Retry-After + optional
// Message the handler's applyEdgeRuleMaintenance consults.
// apid-Validate already enforced the
// {0 ≤ retry_after_seconds ≤ api.MaxEdgeRuleMaintenanceRetryAfterSeconds}
// + {len(message) ≤ 512} clamps (pkg/api/dto.go::EdgeRuleMaintenanceAction.Validate),
// but the SQL hotfix path means we can't trust the validator. This
// compile step is the defence-in-depth pass: a hot-fix that
// bypassed apid still gets clamped here, and a row with a
// non-positive RetryAfterSeconds degrades to the platform default
// (no 503-storm on bad input). The Message field is NOT clamped
// here — only the Retry-After is the load-bearing platform
// invariant.
//
// Like compileLimitRules, the compile is a free function — it
// doesn't need any adapter state (unlike compileValidateRules which
// needs the schema cache). Out-of-bound values are clamped silently
// and a slog.Warn from the caller notes the rule ID; the customer
// never sees the warning (their rule still fires, just at the
// platform default).
func compileMaintenanceRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleMaintenanceResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleMaintenanceResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindMaintenance {
			continue
		}
		if r.Action.Maintenance == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		// Clamp Retry-After to the platform default + ceiling.
		// Non-positive → api.EdgeRuleMaintenanceRetryAfterSeconds (a
		// 0 cap would set Retry-After: 0 which RFC 7231 forbids — the
		// apid-Validate cap already rejects > MaxEdgeRuleMaintenanceRetryAfterSeconds,
		// so the upper clamp is defence-in-depth for direct-DB writes).
		// Same defence-in-depth posture as compileLimitRules
		// silently clamping out-of-bound body caps to the platform
		// cap.
		retry := r.Action.Maintenance.RetryAfterSeconds
		if retry <= 0 {
			retry = api.EdgeRuleMaintenanceRetryAfterSeconds
		}
		if retry > api.MaxEdgeRuleMaintenanceRetryAfterSeconds {
			retry = api.MaxEdgeRuleMaintenanceRetryAfterSeconds
		}
		out = append(out, gateway.EdgeRuleMaintenanceResolved{
			ID:                r.ID,
			AccountID:         r.AccountID,
			AppID:             r.AppID,
			Priority:          r.Priority,
			PathGlob:          r.MatchPath,
			Methods:           buildMethodsMap(r.MatchMethods),
			RetryAfterSeconds: retry,
			Message:           r.Action.Maintenance.Message,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileThrottleRules mirrors compileMaintenanceRules for
// kind=throttle (ADR-091 D20.5 amendment, issue #881). The
// compiled slice carries the per-rule RequestsPerSecond + Burst
// the applier (handler.go::applyEdgeRuleThrottle) hands to
// pkg/gateway.(*Limiter).AllowToken.
//
// apid-Validate (pkg/api/dto.go::EdgeRuleThrottleAction.Validate)
// already enforces:
//
//   - requests_per_second ≥ 1
//   - burst ≥ 1
//   - requests_per_second ≤ plan.RateLimitRPS
//   - burst ≤ plan.RateLimitBurst
//
// — see the ThrottleValidationContext the validator threads.
// This compile step is the defence-in-depth pass: a direct-DB
// write (cmd/e2e/edge_rules_common_test.go::seedEdgeRuleDirect)
// that bypassed apid gets clamped here to {1, 1}, and a rule
// with invalid numerics is dropped from the slice (the customer
// sees pass-through — the rule simply does not fire).
//
// The plan-tier ceiling is NOT re-applied here without the
// limit context (the cmd-side compile doesn't carry per-call plan
// state); the apid validator is the only call that can know the
// plan. A direct-DB write of {rps:1e6, burst:1e9} simply gets
// clamped to {1, 1} so the rule still fires at the
// minimum-useful rate instead of being silently dropped — better
// a weak throttle than no throttle.
//
// Like compileMaintenanceRules + compileLimitRules, the compile
// is a free function — it doesn't need any adapter state.
// Out-of-bound values are clamped silently; the caller logs a
// slog.Warn with the rule ID.
func compileThrottleRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleThrottleResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleThrottleResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindThrottle {
			continue
		}
		if r.Action.Throttle == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		// Clamp rps/burst to the minimum-useful positive values.
		// A 0/0 from a direct-DB write would let the limiter build
		// a bucket that always-deny (burst=0 means every AllowToken
		// call consumes a token that was never refilled), and a
		// negative value would panic the limiter. Floor at {1, 1}.
		rps := r.Action.Throttle.RequestsPerSecond
		if rps < 1 {
			rps = 1
		}
		burst := r.Action.Throttle.Burst
		if burst < 1 {
			burst = 1
		}
		// Phase 3 (ADR-104, issue #881): clamp MaxKeysPerRule to
		// the plan ceiling (Hobby default 1000). The apid
		// validator already enforces per-plan value via
		// Limits.ThrottleMaxKeysPerRule (Free 100 / Hobby 1000 /
		// Pro 5000 / Scale 10000); a 0 from a direct-DB write
		// gets a sensible default here. The cmd-side compile
		// cannot know the per-call plan at this point (the
		// compile is per-rule, not per-request), so it picks
		// the Hobby plan ceiling — middle of the ladder — as
		// the safe default for any plan that didn't pre-set
		// the value. The cmd-side is the source of truth at
		// runtime; apid is the source of truth at create.
		maxKeys := r.Action.Throttle.MaxKeysPerRule
		if maxKeys <= 0 {
			maxKeys = api.ThrottleMaxKeysPerRuleDefault
		}
		if maxKeys > api.ThrottleMaxKeysPerRuleDefault*10 {
			// Scale ceiling is 10x the default; anything beyond
			// is either a bug or a malicious direct-DB write.
			// Clamp to Scale ceiling.
			maxKeys = api.ThrottleMaxKeysPerRuleDefault * 10
		}
		out = append(out, gateway.EdgeRuleThrottleResolved{
			ID:                r.ID,
			AccountID:         r.AccountID,
			AppID:             r.AppID,
			Priority:          r.Priority,
			PathGlob:          r.MatchPath,
			Methods:           buildMethodsMap(r.MatchMethods),
			RequestsPerSecond: rps,
			Burst:             burst,
			// Phase 3 (ADR-104): wire KeyBy + JWTClaimName +
			// MaxKeysPerRule to the resolved rule. Empty
			// KeyBy + empty JWTClaimName + clamped MaxKeys
			// preserve PR #887 behaviour bit-for-bit (the
			// per-consumer branch in applyEdgeRuleThrottle
			// only fires when KeyBy ∈
			// {api_key, jwt_subject, jwt_claim}).
			KeyBy:          r.Action.Throttle.KeyBy,
			JWTClaimName:   r.Action.Throttle.JWTClaimName,
			MaxKeysPerRule: maxKeys,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileBudgetRules mirrors compileLimitRules for kind=budget
// (ADR-093). The compile is a free function — it doesn't need any
// adapter state. Out-of-bound BudgetMs values are clamped silently
// to the platform ceiling, the same defence-in-depth posture as
// compileLimitRules (a hot-fix that bypassed apid still gets
// clamped here, and a row with a non-positive BudgetMs degrades
// to the platform ceiling rather than 0-deadlining every request).
// The customer never sees the warning (their rule still fires, just
// at the platform ceiling).
func compileBudgetRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleBudgetResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleBudgetResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindBudget {
			continue
		}
		if r.Action.Budget == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		// Clamp the budget to the platform ceiling. Non-positive or
		// > ceiling → api.RequestBudgetMaxMs (a 0 budget would
		// 504 every request, which is worse than the platform
		// default; the same defence-in-depth posture as
		// compileLimitRules silently dropping malformed caps).
		budgetMs := r.Action.Budget.BudgetMs
		maxBudgetMs := int(api.RequestBudgetMax.Milliseconds())
		if budgetMs <= 0 || budgetMs > maxBudgetMs {
			budgetMs = maxBudgetMs
		}
		out = append(out, gateway.EdgeRuleBudgetResolved{
			ID:                  r.ID,
			AccountID:           r.AccountID,
			AppID:               r.AppID,
			Priority:            r.Priority,
			PathGlob:            r.MatchPath,
			Methods:             buildMethodsMap(r.MatchMethods),
			BudgetMs:            budgetMs,
			AllowOverrideHeader: r.Action.Budget.AllowOverrideHeader,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileCacheRules mirrors compileBudgetRules for kind=cache
// (ADR-122 §Decision). The compiled slice carries the
// MaxAgeSeconds / StaleIfErrorSeconds / VaryOn fields the
// applier (handler.go::applyEdgeRuleCache) consults on every
// request to compute a CacheKey and consult the runtime
// ResponseCache. apid-Validate already enforced the
// closed-vocab rules on vary_on + methods and clamped
// max_age_seconds ≤ 3600 and stale_if_error_seconds ≤ 300;
// this compile step is the defence-in-depth pass — a hot-fix
// that bypassed apid still fails here, and the rule is dropped
// from the slice (the customer sees the existing pass-through
// path, no panic).
//
// MaxAgeSeconds / StaleIfErrorSeconds are clamped to the
// api.ResponseCacheMaxAgeMaxSeconds /
// ResponseCacheStaleIfErrorMaxSeconds ceilings. A non-positive
// MaxAgeSeconds is kept as 0 (no fresh hits but stale-on-error
// still applies); a negative StaleIfErrorSeconds is clamped to
// 0 (no stale-on-error). The applier treats 0/0 as "fresh
// hits disabled AND stale-on-error disabled" (cache-miss-only
// rule — the rule's only effect is to count outcome=miss
// toward the per-app cache metric).
//
// VaryOn is copied verbatim from the row (the closed-vocab
// check happened at apid-Validate). Methods is built into a set
// via buildMethodsMap so the applier's method filter is O(1).
func compileCacheRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleCacheResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleCacheResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindCache {
			continue
		}
		if r.Action.Cache == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		maxAge := r.Action.Cache.MaxAgeSeconds
		if maxAge < 0 {
			maxAge = 0
		}
		if maxAge > api.ResponseCacheMaxAgeMaxSeconds {
			maxAge = api.ResponseCacheMaxAgeMaxSeconds
		}
		stale := r.Action.Cache.StaleIfErrorSeconds
		if stale < 0 {
			stale = 0
		}
		if stale > api.ResponseCacheStaleIfErrorMaxSeconds {
			stale = api.ResponseCacheStaleIfErrorMaxSeconds
		}
		out = append(out, gateway.EdgeRuleCacheResolved{
			ID:                  r.ID,
			AccountID:           r.AccountID,
			AppID:               r.AppID,
			Priority:            r.Priority,
			PathGlob:            r.MatchPath,
			Methods:             buildMethodsMap(r.MatchMethods),
			MaxAgeSeconds:       maxAge,
			StaleIfErrorSeconds: stale,
			VaryOn:              r.Action.Cache.VaryOn,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// compileValidateRules mirrors compileIPRules for kind=validate.
// The compiled slice carries the SchemaDigest + ContentTypes +
// ApplyWhileStreaming + RejectUnknownFields + MaxBodyBytes fields
// the handler's applyEdgeRuleValidate consults. apid-Validate
// already enforced the schema is non-empty, ≤ 64 KiB, valid JSON,
// and has no external $ref/$id (PR-A); this compile step is the
// defence-in-depth pass — a hot-fix that bypassed apid still
// fails here, and the rule is dropped from the slice (the customer
// sees the existing pass-through path).
//
// The Compile call is keyed off the SHA-256 of the raw schema body,
// stashed on the resolved struct so the handler-side applier can
// look up the compiled *CompiledSchema in pkg/edgevalidate.Cache.
// A nil validate adapter compiles-then-drops the rule (we don't
// want a missing adapter to crash the load; the rule is simply
// skipped, same as a malformed CIDR).
func (g *gatewaydEdgeRules) compileValidateRules(storeRules []state.EdgeRule) ([]gateway.EdgeRuleValidateResolved, []gateway.PathGlobError) {
	if len(storeRules) == 0 {
		return nil, nil
	}
	out := make([]gateway.EdgeRuleValidateResolved, 0, len(storeRules))
	var parseErrs []gateway.PathGlobError
	for i := range storeRules {
		r := &storeRules[i]
		if !r.Enabled {
			continue
		}
		if r.Kind != state.EdgeRuleKindValidate {
			continue
		}
		if r.Action.Validate == nil {
			continue
		}
		if errs := validatePathGlob(r.ID, r.MatchPath); errs != nil {
			parseErrs = append(parseErrs, errs...)
			continue
		}
		action := r.Action.Validate
		// Compute the digest from the raw schema bytes — this
		// is what pkg/edgevalidate.Cache will key on. Doing
		// it here lets the load path surface a parse error
		// before the rule reaches the cache.
		schemaBytes := []byte(action.Schema)
		var digest [32]byte
		if g.validate == nil {
			// Adapter not wired — drop the rule. The
			// loadHost caller's parseErrs slice stays
			// empty (this is a deploy config error,
			// not a malformed rule). slog it from
			// the caller.
			continue
		}
		d, err := g.validate.CompileSchema(schemaBytes, action.RejectOnUnknown)
		if err != nil {
			parseErrs = append(parseErrs, gateway.PathGlobError{
				RuleID: r.ID, Glob: "validate", Err: err,
			})
			continue
		}
		digest = d
		var contentTypes []string
		if len(action.ContentTypes) > 0 {
			contentTypes = append(contentTypes, action.ContentTypes...)
		}
		// ValidateMode source-of-truth (ADR-128):
		//   1. Top-level column (state.EdgeRule.ValidateMode)
		//      wins. New writes land here per the store
		//      layer's empty-string→'block' coalesce.
		//   2. Fallback to action.ValidateMode when the
		//      top-level column is empty. This is the
		//      deprecation window contract (ADR-128 §D2):
		//      rows written before this PR shipped with the
		//      mode in action JSON only. After one release
		//      the fallback is removed.
		//   3. Empty string (both top-level empty AND action
		//      empty) is the strict-mode sentinel — the
		//      handler coerces to 'block' at
		//      handler.go:2694. Matches the schema-side
		//      NOT NULL DEFAULT 'block' at 00293.
		mode := r.ValidateMode
		if mode == "" {
			mode = action.ValidateMode
		}
		out = append(out, gateway.EdgeRuleValidateResolved{
			ID:                  r.ID,
			AccountID:           r.AccountID,
			AppID:               r.AppID,
			Priority:            r.Priority,
			PathGlob:            r.MatchPath,
			Methods:             buildMethodsMap(r.MatchMethods),
			SchemaDigest:        digest,
			ContentTypes:        contentTypes,
			ApplyWhileStreaming: action.ApplyWhileStreaming,
			RejectUnknownFields: action.RejectOnUnknown,
			MaxBodyBytes:        action.MaxBodyBytes,
			ValidateMode:        mode,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Priority < out[j].Priority })
	return out, parseErrs
}

// validatePathGlob runs stdlib path.Match(glob, "") to detect a
// malformed pattern (unmatched bracket, etc.) without depending
// on the specific request path. Returns a 0/1-length PathGlobError
// slice — nil when the glob is empty ("") or "*" (match-all
// sentinels that stdlib rejects on the empty-input probe) or
// parses cleanly. PR 3 review-fix R3 introduced this so a typo
// is operator-visible instead of silently 404.
func validatePathGlob(ruleID, glob string) []gateway.PathGlobError {
	if glob == "" || glob == "*" {
		return nil
	}
	if _, err := path.Match(glob, ""); err != nil {
		return []gateway.PathGlobError{{RuleID: ruleID, Glob: glob, Err: err}}
	}
	return nil
}

// buildMethodsMap upper-folds a MatchMethods slice into the
// map[string]bool the per-request filter consults. nil in -> nil out
// (the filter's "nil = any method" short-circuit handles it).
func buildMethodsMap(methods []string) map[string]bool {
	if len(methods) == 0 {
		return nil
	}
	m := make(map[string]bool, len(methods))
	for _, meth := range methods {
		m[strings.ToUpper(meth)] = true
	}
	return m
}

// convertHeaderOps copies a []state.EdgeRuleHeaderOp into the
// gateway-side subset type. Same shape; the cmd-side copies
// instead of importing state from pkg/gateway (pkg/gateway keeps
// zero-dep-on-pkg/state).
func convertHeaderOps(in []state.EdgeRuleHeaderOp) []gateway.EdgeRuleHeaderOp {
	if len(in) == 0 {
		return nil
	}
	out := make([]gateway.EdgeRuleHeaderOp, len(in))
	for i, op := range in {
		out[i] = gateway.EdgeRuleHeaderOp{Name: op.Name, Value: op.Value, Action: op.Action}
	}
	return out
}

// gatewaydEdgeRulesAud is the cmd/gatewayd-internal audit thin wrapper
// the handler's edge_rule.*_matched / edge_rule.*_blocked rows go
// through. Reuses the existing *gatewaydAuditor
// (cmd/gatewayd-internal/audit.go) so the rows land in the same events
// table every other gatewayd-scope row uses. The pkg/gateway handler
// imports `gateway.EdgeRuleAuditor` (the narrow interface), not
// *gatewaydAuditor directly, to keep the pkg/gateway ← cmd/gatewayd-internal
// dep direction one-way.
type gatewaydEdgeRulesAud struct {
	inner *gatewaydAuditor
}

// Emit forwards to the underlying gatewaydAuditor. Nil-safe on the
// receiver so unit tests can pass nil and drop the audit row.
func (a *gatewaydEdgeRulesAud) Emit(ctx context.Context, kind string, subject *string, data map[string]any) {
	if a == nil || a.inner == nil {
		return
	}
	a.inner.Emit(ctx, kind, subject, data)
}

// newGatewaydEdgeRulesAud wraps an existing *gatewaydAuditor. The
// production wiring in cmd/gatewayd-internal/run.go calls this
// next to newGatewaydAuditor.
func newGatewaydEdgeRulesAud(inner *gatewaydAuditor) *gatewaydEdgeRulesAud {
	return &gatewaydEdgeRulesAud{inner: inner}
}

// Compile-time check: gatewaydEdgeRules satisfies gateway.EdgeRuleMatcher.
// Fails to compile if the matcher interface widens in pkg/gateway
// and the impl forgets to add the new method.
var _ gateway.EdgeRuleMatcher = (*gatewaydEdgeRules)(nil)

// Compile-time check: gatewaydEdgeRulesAud satisfies gateway.EdgeRuleAuditor.
var _ gateway.EdgeRuleAuditor = (*gatewaydEdgeRulesAud)(nil)
