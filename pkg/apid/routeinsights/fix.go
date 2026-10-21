package routeinsights

// Fix planning (ADR-20261020161310): the edge-rule change that resolves an
// insight, planned from its evidence and the app's current rules so it always
// describes what applying it would do now.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeadvisor"
	"github.com/onebox-faas/faas/pkg/state"
)

// cacheMethods are the methods a fix's cache rule covers.
var cacheMethods = []string{"GET", "HEAD"}

// Plan is a planned fix and the writes that apply it. Exactly one of Create
// and Update is set.
type Plan struct {
	Fix    api.RouteInsightFix
	Create *api.CreateEdgeRuleRequest
	Update *api.UpdateEdgeRuleRequest
	// Kinds are the insight kinds on the route the fix resolves.
	Kinds []string
	// PurgeGlob is the request-path glob whose cached responses the fix
	// purges after the rule change; empty for none.
	PurgeGlob string
}

// PlanFix returns the fix that applying insight would make now, or nil and
// why there is none. active is the app's active insights; rules its edge
// rules.
func PlanFix(insight api.RouteInsight, active []api.RouteInsight, rules []state.EdgeRule, now time.Time) (*Plan, string) {
	if insight.State != api.RouteInsightActive {
		return nil, "the insight is resolved"
	}
	glob, ok := routeadvisor.PathGlob(insight.Route)
	if !ok {
		return nil, "the route cannot be expressed as an edge-rule path"
	}
	serving, servingOK := servingCacheRule(rules, insight.Method, insight.Route, now)
	switch insight.Kind {
	case api.RouteInsightCacheable:
		return planCreate(insight, insight, glob, serving, servingOK)
	case api.RouteInsightKeptAwake:
		i := slices.IndexFunc(active, func(o api.RouteInsight) bool {
			return o.Kind == api.RouteInsightCacheable && o.Method == insight.Method && o.Route == insight.Route && o.State == api.RouteInsightActive
		})
		if i < 0 {
			return nil, "only the caller can change how often it calls this route; it was not measured safe to cache"
		}
		return planCreate(insight, active[i], glob, serving, servingOK)
	case api.RouteInsightCacheTooShort, api.RouteInsightCacheStale:
		if !servingOK {
			return nil, "no enabled cache rule serves this route any more"
		}
		return planRuleChange(insight, glob, serving)
	}
	return nil, "this kind of insight has no fix"
}

// planCreate caches the route with the safe max-age the cacheable insight
// measured. from is the insight that carries the evidence.
func planCreate(insight, from api.RouteInsight, glob string, serving state.EdgeRule, servingOK bool) (*Plan, string) {
	if servingOK {
		return nil, "a cache rule already serves this route"
	}
	safe := evidenceInt(from.Evidence, "safe_max_age_seconds")
	if safe <= 0 {
		return nil, "no max-age was measured safe for this route"
	}
	action, _ := json.Marshal(api.EdgeRuleCacheAction{MaxAgeSeconds: safe, StaleIfErrorSeconds: api.ResponseCacheDefaultStaleIfErrorSeconds})
	enabled := true
	create := &api.CreateEdgeRuleRequest{
		MatchHost: api.EdgeRuleAppHosts, MatchPath: glob, MatchMethods: slices.Clone(cacheMethods), Enabled: &enabled,
		Kind: string(state.EdgeRuleKindCache), Action: action,
		Name:        truncateRunes("Cache "+insight.Method+" "+insight.Route, api.EdgeRuleNameMaxChars),
		Description: truncateRunes(fmt.Sprintf("Created from a route insight: responses stayed the same for at least %ds.", safe), api.EdgeRuleDescriptionMaxChars),
	}
	fix := api.RouteInsightFix{
		Action: api.RouteInsightFixCreateCacheRule, MaxAgeSeconds: safe,
		Summary: fmt.Sprintf("Create an enabled cache rule for GET and HEAD %s on every app host with a %ds max-age.", insight.Route, safe),
	}
	fix.ID = fixID(insight.ID, fix, "", "", glob)
	return &Plan{Fix: fix, Create: create, Kinds: []string{api.RouteInsightCacheable, api.RouteInsightKeptAwake}}, ""
}

// planRuleChange moves the serving rule's max-age to the measured safe one,
// or disables it when a stale route had none. A rule that also covers other
// routes, or that the app's manifest owns, gets a narrower override instead
// of an edit.
func planRuleChange(insight api.RouteInsight, glob string, rule state.EdgeRule) (*Plan, string) {
	if rule.Action.Cache == nil {
		return nil, "the serving rule has no cache action"
	}
	current := rule.Action.Cache.MaxAgeSeconds
	safe := evidenceInt(insight.Evidence, "safe_max_age_seconds")
	target := 0
	switch {
	case insight.Kind == api.RouteInsightCacheTooShort && safe > current:
		target = safe
	case insight.Kind == api.RouteInsightCacheTooShort:
		return nil, "the measured safe max-age is no longer above the rule's"
	case safe > 0 && safe < current:
		target = safe
	}
	exact := rule.MatchPath == glob && rule.ManifestKey == ""
	purge := ""
	if insight.Kind == api.RouteInsightCacheStale {
		purge = glob
	}
	label := ruleLabel(rule)
	plan := &Plan{Kinds: []string{insight.Kind}, PurgeGlob: purge}
	switch {
	case target > 0 && exact:
		next := *rule.Action.Cache
		next.MaxAgeSeconds = target
		raw, _ := json.Marshal(next)
		action := json.RawMessage(raw)
		plan.Update = &api.UpdateEdgeRuleRequest{Action: &action}
		plan.Fix = api.RouteInsightFix{
			Action: api.RouteInsightFixSetCacheMaxAge, MaxAgeSeconds: target, RuleID: rule.ID,
			Summary: fmt.Sprintf("Change the max-age of cache rule %s from %ds to %ds.", label, current, target),
		}
	case target > 0:
		if rule.Priority <= 0 {
			return nil, fmt.Sprintf("cache rule %s also covers other routes and has priority 0, so no rule can take precedence over it; narrow it on the edge-rules page", label)
		}
		plan.Create = overrideRule(insight, glob, rule, target)
		why := "also covers other routes"
		if rule.ManifestKey != "" {
			why = "comes from the app's manifest"
		}
		plan.Fix = api.RouteInsightFix{
			Action: api.RouteInsightFixAddCacheOverride, MaxAgeSeconds: target, RuleID: rule.ID,
			Summary: fmt.Sprintf("Add a cache rule for %s %s with a %ds max-age ahead of cache rule %s, which %s.", insight.Method, insight.Route, target, label, why),
		}
	case exact:
		disabled := false
		plan.Update = &api.UpdateEdgeRuleRequest{Enabled: &disabled}
		plan.Fix = api.RouteInsightFix{
			Action: api.RouteInsightFixDisableCacheRule, RuleID: rule.ID,
			Summary: fmt.Sprintf("Disable cache rule %s: no max-age was measured safe for %s %s, so every request will reach the app.", label, insight.Method, insight.Route),
		}
	default:
		return nil, fmt.Sprintf("no max-age was measured safe and cache rule %s also covers other routes or comes from the app's manifest; change it on the edge-rules page", label)
	}
	if purge != "" {
		plan.Fix.PurgeCache = true
		plan.Fix.Summary += " Then purge the route's cached responses."
	}
	plan.Fix.ID = fixID(insight.ID, plan.Fix, rule.ID, rule.UpdatedAt.UTC().Format(time.RFC3339Nano), glob)
	return plan, ""
}

// overrideRule matches a subset of what rule matches: the same host,
// headers, condition and mode, GET/HEAD only, on the route's path, one
// priority ahead.
func overrideRule(insight api.RouteInsight, glob string, rule state.EdgeRule, maxAge int) *api.CreateEdgeRuleRequest {
	next := *rule.Action.Cache
	next.MaxAgeSeconds = maxAge
	raw, _ := json.Marshal(next)
	methods := slices.Clone(cacheMethods)
	if len(rule.MatchMethods) > 0 {
		methods = slices.DeleteFunc(methods, func(m string) bool { return !slices.Contains(rule.MatchMethods, m) })
	}
	priority, enabled := rule.Priority-1, true
	return &api.CreateEdgeRuleRequest{
		MatchHost: rule.MatchHost, MatchPath: glob, MatchMethods: methods, MatchHeaders: rule.MatchHeaders,
		Priority: &priority, Enabled: &enabled, Kind: string(state.EdgeRuleKindCache), Action: raw,
		Match: rule.Match, Mode: rule.Mode,
		Name:        truncateRunes("Cache "+insight.Method+" "+insight.Route, api.EdgeRuleNameMaxChars),
		Description: truncateRunes(fmt.Sprintf("Created from a route insight to set this route's max-age to %ds ahead of rule %s.", maxAge, rule.ID), api.EdgeRuleDescriptionMaxChars),
	}
}

// servingCacheRule returns the enabled, unexpired, enforcing cache rule the
// gateway would apply to the route.
func servingCacheRule(rules []state.EdgeRule, method, route string, now time.Time) (state.EdgeRule, bool) {
	existing := make([]routeadvisor.ExistingRule, 0, len(rules))
	byID := make(map[string]state.EdgeRule, len(rules))
	for _, r := range rules {
		if r.Kind != state.EdgeRuleKindCache || r.Mode == state.EdgeRuleModeLog || r.EdgeRuleExpired(now) {
			continue
		}
		e := routeadvisor.ExistingRule{ID: r.ID, Kind: string(r.Kind), Path: r.MatchPath, Methods: r.MatchMethods, Enabled: r.Enabled, Priority: r.Priority}
		existing = append(existing, e)
		byID[r.ID] = r
	}
	found, ok := routeadvisor.CacheRuleFor(existing, method, route)
	if !ok {
		return state.EdgeRule{}, false
	}
	return byID[found.ID], true
}

// fixID covers the insight and every value the fix writes, including the
// target rule's last update, so a stale id is refused.
func fixID(insightID string, fix api.RouteInsightFix, ruleID, ruleVersion, glob string) string {
	parts := []string{insightID, fix.Action, strconv.Itoa(fix.MaxAgeSeconds), ruleID, ruleVersion, glob, strconv.FormatBool(fix.PurgeCache)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\n")))
	return hex.EncodeToString(sum[:8])
}

func ruleLabel(rule state.EdgeRule) string {
	if rule.Name != "" {
		return fmt.Sprintf("%q (%s)", rule.Name, rule.ID)
	}
	return rule.ID
}

// evidenceInt reads one integer from an insight's evidence object; 0 when
// it is missing or not a number.
func evidenceInt(raw json.RawMessage, key string) int {
	var ev map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if dec.Decode(&ev) != nil {
		return 0
	}
	n, ok := ev[key].(json.Number)
	if !ok {
		return 0
	}
	if i, err := n.Int64(); err == nil {
		return int(i)
	}
	f, _ := n.Float64()
	return int(f)
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
