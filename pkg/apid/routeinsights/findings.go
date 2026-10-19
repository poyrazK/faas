// Package routeinsights turns route analyses into proactive findings
// (ADR-1198). A periodic evaluator in apid derives, per app, the routes that
// keep it awake (the awake report, ADR-1187) and the routes measured safe to
// cache (learned cacheability, ADR-1188), checks existing cache rules against
// their refills (ADR-1199), records them as route_insights rows and lets a
// database trigger notify subscribed app webhooks of every detected or
// resolved transition.
package routeinsights

import (
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/routeadvisor"
)

// Insight kinds, matching the route_insights CHECK.
const (
	KindKeptAwake = "kept_awake"
	KindCacheable = "cacheable"
	// KindCacheStale and KindCacheTooShort check existing cache rules from
	// refill evidence (ADR-1199).
	KindCacheStale    = "cache_stale"
	KindCacheTooShort = "cache_too_short"
)

// Finding is one insight observed by an evaluation.
type Finding struct {
	Kind     string
	Method   string
	Route    string
	Message  string
	Evidence map[string]any
}

// KeptAwake reports the routes whose callers keep a mostly-awake app from
// parking: the awake report's routes with a suggestion, when the app was
// awake for at least RouteInsightKeptAwakeMinShare of the window.
func KeptAwake(report api.RouteAwakeResponse) []Finding {
	if report.AwakeShare < api.RouteInsightKeptAwakeMinShare {
		return nil
	}
	var out []Finding
	for _, r := range report.Routes {
		if r.Suggestion == nil {
			continue
		}
		out = append(out, Finding{
			Kind: KindKeptAwake, Method: r.Method, Route: r.Route, Message: r.Suggestion.Message,
			Evidence: map[string]any{
				"awake_share":          report.AwakeShare,
				"window_seconds":       report.WindowSeconds,
				"idle_timeout_seconds": report.IdleTimeoutSeconds,
				"interval_seconds":     r.IntervalSeconds,
				"awake_seconds_saved":  r.IfRemoved.AwakeSecondsSaved,
				"gb_hours_saved":       r.IfRemoved.GBHoursSaved,
				"wakes_change":         r.IfRemoved.WakesChange,
				"suggestion":           r.Suggestion.Kind,
			},
		})
	}
	return out
}

// Cacheable reports the routes the advisor would cache whose content
// evidence established a safe max-age. Routes already covered by a cache
// rule are skipped by the advisor.
func Cacheable(slug string, routes []routeadvisor.RouteStats, existing []routeadvisor.ExistingRule) []Finding {
	suggestions := routeadvisor.Advise(routeadvisor.Inputs{
		Routes: routes, Existing: existing,
		// Rule bodies are not used here; one host makes the advisor build them.
		Hosts:              []string{slug},
		CacheMaxAgeSeconds: api.ResponseCacheDefaultMaxAgeSeconds,
	})
	var out []Finding
	for _, s := range suggestions {
		if s.Kind != api.RouteAdviceKindCache || s.Evidence.SafeMaxAgeSeconds == 0 {
			continue
		}
		safe := time.Duration(s.Evidence.SafeMaxAgeSeconds) * time.Second
		out = append(out, Finding{
			Kind: KindCacheable, Method: s.Method, Route: s.Route,
			Message: fmt.Sprintf("%s %s returned the same body to %d of %d repeat requests up to %s apart, so an edge cache rule with a %ds lifetime would rarely serve a stale answer and never wakes the app for a cached response. `gregale routes advise %s --cache-max-age %d` drafts it.",
				s.Method, s.Route, s.Evidence.ContentRepeats, s.Evidence.ContentRepeats+s.Evidence.ContentChanges, safe, s.Evidence.SafeMaxAgeSeconds, slug, s.Evidence.SafeMaxAgeSeconds),
			Evidence: map[string]any{
				"safe_max_age_seconds":     s.Evidence.SafeMaxAgeSeconds,
				"content_repeats":          s.Evidence.ContentRepeats,
				"content_changes":          s.Evidence.ContentChanges,
				"content_observed":         s.Evidence.ContentObserved,
				"requests":                 s.Evidence.Requests,
				"estimated_cache_hits_60s": s.Impact.EstimatedCacheHits,
				"estimated_wakes_avoided":  s.Impact.EstimatedWakesAvoided,
			},
		})
	}
	return out
}

// CacheRules checks the enabled cache rule on each observed GET route against
// what its refills measured (ADR-1199): a rule that served stale bodies, or
// one far shorter than the body's measured lifetime.
func CacheRules(routes []routeadvisor.RouteStats, existing []routeadvisor.ExistingRule) []Finding {
	var out []Finding
	for _, r := range routes {
		rule, ok := routeadvisor.CacheRuleFor(existing, r.Method, r.Path)
		if !ok {
			continue
		}
		check, ok := routeadvisor.CheckCacheRule(r, rule)
		if !ok {
			continue
		}
		f := Finding{Method: r.Method, Route: r.Path, Evidence: map[string]any{
			"max_age_seconds":      int(check.MaxAge / time.Second),
			"refills":              check.Refills,
			"stale_refills":        check.Stale,
			"safe_max_age_seconds": int(check.SafeMaxAge / time.Second),
			"requests":             r.Requests,
		}}
		switch check.Kind {
		case routeadvisor.CacheRuleStale:
			f.Kind = KindCacheStale
			fix := "Shorten the rule's max-age"
			if check.SafeMaxAge > 0 && check.SafeMaxAge < check.MaxAge {
				fix += fmt.Sprintf(" to %s, the longest the gateways measured as safe,", check.SafeMaxAge)
			}
			f.Message = fmt.Sprintf("%s %s is cached for %s, but %d of %d refills (%s) found the body had changed before that ran out, so callers were served stale responses. %s or purge the cache when the content changes.",
				r.Method, r.Path, check.MaxAge, check.Stale, check.Refills, percent(check.Stale, check.Refills), fix)
		case routeadvisor.CacheRuleTooShort:
			f.Kind = KindCacheTooShort
			f.Message = fmt.Sprintf("%s %s is cached for %s, but its body stayed the same for up to %s across %d refills. A max-age of %ds would answer more requests at the edge; every refill it saves is a request that can wake the app.",
				r.Method, r.Path, check.MaxAge, check.SafeMaxAge, check.Refills, int(check.SafeMaxAge/time.Second))
		}
		out = append(out, f)
	}
	return out
}

func percent(part, total int64) string {
	if total <= 0 {
		return "0%"
	}
	return fmt.Sprintf("%.0f%%", float64(part)/float64(total)*100)
}
