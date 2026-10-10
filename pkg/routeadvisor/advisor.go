// Package routeadvisor derives edge-rule suggestions from retained request
// telemetry (ADR-955). It is pure: callers supply aggregated route and
// consumer statistics, existing rules and plan capabilities, and receive
// suggestions with evidence, a what-if estimate and disabled rule bodies.
package routeadvisor

import (
	"cmp"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// RouteStats aggregates one observed route over the advice window.
// CacheHits and WakesAvoided are estimates for Inputs.CacheMaxAgeSeconds.
type RouteStats struct {
	Method, Path      string
	Requests          int64
	AnonymousRequests int64
	AnonymousSuccess  int64
	ServerErrors      int64
	Timeouts          int64
	ColdBoots         int64
	P95LatencyMs      int
	CacheHits         int64
	WakesAvoided      int64
}

// ConsumerPeak is one identified consumer's traffic on a route.
type ConsumerPeak struct {
	ID            string
	Requests      int64
	PeakPerMinute int64
}

// ConsumerStats holds the two busiest identified consumers of a route.
type ConsumerStats struct {
	Method, Path string
	Consumers    int64
	Top, Next    ConsumerPeak
}

// ExistingRule is an app edge rule, enabled or not, used to avoid suggesting
// a rule the customer already has or has already applied for review.
type ExistingRule struct {
	Kind    string
	Path    string
	Methods []string
}

// Key identifies a route by method and path.
type Key struct{ Method, Path string }

// Inputs is everything Advise needs. ThrottleExcess holds, per candidate
// from ThrottleCandidates, the estimated requests above the candidate limit.
type Inputs struct {
	Routes             []RouteStats
	Consumers          []ConsumerStats
	Existing           []ExistingRule
	Hosts              []string
	CacheMaxAgeSeconds int
	AsyncAllowed       bool
	PlanMaxRPS         int
	PlanMaxBurst       int
	ThrottleExcess     map[Key]int64
}

// ThrottleLimit is the per-consumer limit proposed for a route.
type ThrottleLimit struct {
	ConsumerID         string
	RequestsPerSecond  float64
	Burst              int
	AllowancePerMinute int64
}

// ThrottleCandidates returns the routes where one consumer dominates and a
// limit exists that leaves every other observed consumer unthrottled. The
// caller estimates each candidate's excess and passes it back in Inputs.
func ThrottleCandidates(in Inputs) map[Key]ThrottleLimit {
	routes := map[Key]RouteStats{}
	for _, r := range in.Routes {
		routes[Key{r.Method, r.Path}] = r
	}
	out := map[Key]ThrottleLimit{}
	for _, c := range in.Consumers {
		k := Key{c.Method, c.Path}
		r, ok := routes[k]
		if !ok || r.Requests < api.RouteAdviceMinRequests || c.Consumers < 2 || c.Top.ID == "" || c.Next.ID == "" {
			continue
		}
		if share(c.Top.Requests, r.Requests) < api.RouteAdviceThrottleMinTopShare ||
			share(r.AnonymousRequests, r.Requests) > api.RouteAdviceThrottleMaxAnonymousShare {
			continue
		}
		if covered(in.Existing, api.RouteAdviceKindThrottle, r.Method, r.Path) {
			continue
		}
		limit, ok := throttleLimit(c, in.PlanMaxRPS, in.PlanMaxBurst)
		if !ok || c.Top.PeakPerMinute <= limit.AllowancePerMinute {
			continue
		}
		out[k] = limit
	}
	return out
}

// throttleLimit sizes a limit at RouteAdviceThrottleHeadroom times the next
// busiest consumer's peak minute. It fails when the plan ceiling would cut
// below that, because the limit would then throttle other consumers too.
func throttleLimit(c ConsumerStats, planRPS, planBurst int) (ThrottleLimit, bool) {
	perMinute := max(int64(1), c.Next.PeakPerMinute*api.RouteAdviceThrottleHeadroom)
	rps := math.Ceil(float64(perMinute)/60*10) / 10
	if planRPS > 0 && rps > float64(planRPS) {
		return ThrottleLimit{}, false
	}
	burst := max(1, int(math.Ceil(rps*10)))
	if planBurst > 0 && burst > planBurst {
		burst = planBurst
	}
	return ThrottleLimit{
		ConsumerID:         c.Top.ID,
		RequestsPerSecond:  rps,
		Burst:              burst,
		AllowancePerMinute: int64(math.Ceil(rps*60)) + int64(burst),
	}, true
}

// Advise returns suggestions ordered by observed route volume.
func Advise(in Inputs) []api.RouteAdviceSuggestion {
	consumers := map[Key]ConsumerStats{}
	for _, c := range in.Consumers {
		consumers[Key{c.Method, c.Path}] = c
	}
	throttles := ThrottleCandidates(in)
	out := []api.RouteAdviceSuggestion{}
	for _, r := range in.Routes {
		if r.Requests < api.RouteAdviceMinRequests {
			continue
		}
		if s, ok := cacheSuggestion(in, r); ok {
			out = append(out, s)
		}
		if s, ok := asyncSuggestion(in, r); ok {
			out = append(out, s)
		}
		k := Key{r.Method, r.Path}
		if limit, ok := throttles[k]; ok {
			if s, ok := throttleSuggestion(in, r, consumers[k], limit, in.ThrottleExcess[k]); ok {
				out = append(out, s)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b api.RouteAdviceSuggestion) int {
		if c := cmp.Compare(b.Evidence.Requests, a.Evidence.Requests); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
	})
	return out
}

func cacheSuggestion(in Inputs, r RouteStats) (api.RouteAdviceSuggestion, bool) {
	if r.Method != "GET" || strings.Contains(r.Path, "{") || in.CacheMaxAgeSeconds <= 0 {
		return api.RouteAdviceSuggestion{}, false
	}
	if share(r.AnonymousSuccess, r.Requests) < api.RouteAdviceCacheMinAnonymousShare ||
		share(r.CacheHits, r.Requests) < api.RouteAdviceCacheMinHitShare {
		return api.RouteAdviceSuggestion{}, false
	}
	glob, ok := PathGlob(r.Path)
	if !ok || covered(in.Existing, api.RouteAdviceKindCache, r.Method, r.Path) {
		return api.RouteAdviceSuggestion{}, false
	}
	action := api.EdgeRuleCacheAction{MaxAgeSeconds: in.CacheMaxAgeSeconds, StaleIfErrorSeconds: api.ResponseCacheDefaultStaleIfErrorSeconds}
	summary := fmt.Sprintf("about %d of %d requests (%s) would be served from the edge cache", r.CacheHits, r.Requests, percent(r.CacheHits, r.Requests))
	if r.WakesAvoided > 0 {
		summary += fmt.Sprintf(", avoiding %d of %d wakes", r.WakesAvoided, r.ColdBoots)
	}
	return suggestion(in, r, api.RouteAdviceKindCache, glob, []string{"GET", "HEAD"}, action, api.RouteAdviceSuggestion{
		Title:     "Cache " + r.Method + " " + r.Path,
		Rationale: fmt.Sprintf("%s of requests are anonymous and succeed, so a shared %ds cache can answer repeats without waking the app.", percent(r.AnonymousSuccess, r.Requests), in.CacheMaxAgeSeconds),
		Impact:    api.RouteAdviceImpact{Summary: summary, EstimatedCacheHits: r.CacheHits, EstimatedWakesAvoided: r.WakesAvoided},
		Cautions: []string{
			fmt.Sprintf("Responses can be up to %ds old. Apply only if the response is the same for every caller and changes less often than that.", in.CacheMaxAgeSeconds),
			"Requests with Authorization or Cookie headers always bypass the cache, and the estimate treats all query strings as one response, so it is an upper bound.",
		},
	})
}

func asyncSuggestion(in Inputs, r RouteStats) (api.RouteAdviceSuggestion, bool) {
	if !in.AsyncAllowed || r.Method != "POST" {
		return api.RouteAdviceSuggestion{}, false
	}
	timeouts := r.Timeouts >= api.RouteAdviceAsyncMinTimeouts && share(r.Timeouts, r.Requests) >= api.RouteAdviceAsyncMinTimeoutShare
	slow := int64(r.P95LatencyMs) >= api.RouteAdviceAsyncMinP95.Milliseconds()
	if !timeouts && !slow {
		return api.RouteAdviceSuggestion{}, false
	}
	glob, ok := PathGlob(r.Path)
	if !ok || covered(in.Existing, api.RouteAdviceKindAsync, r.Method, r.Path) {
		return api.RouteAdviceSuggestion{}, false
	}
	summary := fmt.Sprintf("%d timed-out requests would run as durable invocations with retries instead of failing the caller", r.Timeouts)
	if r.Timeouts == 0 {
		summary = fmt.Sprintf("callers would get 202 Accepted at once instead of waiting a p95 of %dms", r.P95LatencyMs)
	}
	return suggestion(in, r, api.RouteAdviceKindAsync, glob, []string{"POST"}, api.EdgeRuleAsyncAction{}, api.RouteAdviceSuggestion{
		Title:     "Run " + r.Method + " " + r.Path + " asynchronously",
		Rationale: fmt.Sprintf("%d of %d requests timed out and the p95 latency is %dms.", r.Timeouts, r.Requests, r.P95LatencyMs),
		Impact:    api.RouteAdviceImpact{Summary: summary, TimeoutsAffected: r.Timeouts},
		Cautions: []string{
			"Callers receive 202 Accepted with an invocation ID instead of the response body. Update clients to poll or receive the result before enabling the rule.",
		},
	})
}

func throttleSuggestion(in Inputs, r RouteStats, c ConsumerStats, limit ThrottleLimit, excess int64) (api.RouteAdviceSuggestion, bool) {
	if excess <= 0 || share(excess, r.Requests) < api.RouteAdviceThrottleMinExcessShare {
		return api.RouteAdviceSuggestion{}, false
	}
	glob, ok := PathGlob(r.Path)
	if !ok {
		return api.RouteAdviceSuggestion{}, false
	}
	action := api.EdgeRuleThrottleAction{RequestsPerSecond: limit.RequestsPerSecond, Burst: limit.Burst, KeyBy: api.ThrottleKeyByConsumerID}
	s, ok := suggestion(in, r, api.RouteAdviceKindThrottle, glob, []string{r.Method}, action, api.RouteAdviceSuggestion{
		Title:     "Limit each consumer of " + r.Method + " " + r.Path,
		Rationale: fmt.Sprintf("Consumer %s sent %s of requests, peaking at %d a minute; the next busiest consumer peaked at %d.", c.Top.ID, percent(c.Top.Requests, r.Requests), c.Top.PeakPerMinute, c.Next.PeakPerMinute),
		Impact: api.RouteAdviceImpact{
			Summary:                    fmt.Sprintf("about %d requests from %s above %.1f req/s would get 429; no other observed consumer reaches the limit", excess, c.Top.ID, limit.RequestsPerSecond),
			EstimatedThrottledRequests: excess,
		},
		Cautions: []string{
			fmt.Sprintf("Limits every consumer of this route to %.1f req/s with a burst of %d. Requests without a consumer identity share one bucket.", limit.RequestsPerSecond, limit.Burst),
		},
	})
	s.Evidence.Consumers = c.Consumers
	s.Evidence.TopConsumerID = c.Top.ID
	s.Evidence.TopConsumerRequests = c.Top.Requests
	s.Evidence.TopConsumerPeakPerMinute = c.Top.PeakPerMinute
	s.Evidence.NextConsumerPeakPerMin = c.Next.PeakPerMinute
	return s, ok
}

// suggestion fills the fields every kind shares and one disabled rule per
// host. It fails when there is no host to attach a rule to.
func suggestion(in Inputs, r RouteStats, kind, glob string, methods []string, action any, s api.RouteAdviceSuggestion) (api.RouteAdviceSuggestion, bool) {
	raw, err := json.Marshal(action)
	if err != nil || len(in.Hosts) == 0 {
		return api.RouteAdviceSuggestion{}, false
	}
	s.ID, s.Kind, s.Method, s.Route = SuggestionID(kind, r.Method, r.Path), kind, r.Method, r.Path
	s.Evidence = api.RouteAdviceEvidence{
		Requests: r.Requests, AnonymousRequests: r.AnonymousRequests, ServerErrors: r.ServerErrors,
		Timeouts: r.Timeouts, ColdBoots: r.ColdBoots, P95LatencyMs: r.P95LatencyMs,
	}
	disabled := false
	for _, host := range in.Hosts {
		s.Rules = append(s.Rules, api.CreateEdgeRuleRequest{
			MatchHost: host, MatchPath: glob, MatchMethods: slices.Clone(methods),
			Enabled: &disabled, Kind: kind, Action: raw,
		})
	}
	return s, true
}

// SuggestionID is stable for one kind, method and route.
func SuggestionID(kind, method, path string) string {
	sum := sha256.Sum256([]byte(kind + "\n" + method + "\n" + path))
	return hex.EncodeToString(sum[:6])
}

// PathGlob converts a route template to an edge-rule path glob: each
// "{param}" segment becomes "*". Literal segments containing glob syntax
// cannot be expressed exactly and are rejected.
func PathGlob(route string) (string, bool) {
	if !strings.HasPrefix(route, "/") {
		return "", false
	}
	parts := strings.Split(route, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") && len(p) > 2 {
			parts[i] = "*"
			continue
		}
		if strings.ContainsAny(p, "*?[]\\{}") {
			return "", false
		}
	}
	return strings.Join(parts, "/"), true
}

// covered reports whether an existing rule of the kind already matches the
// route, using a sample path with every parameter filled in.
func covered(rules []ExistingRule, kind, method, route string) bool {
	parts := strings.Split(route, "/")
	for i, p := range parts {
		if strings.HasPrefix(p, "{") && strings.HasSuffix(p, "}") {
			parts[i] = "x"
		}
	}
	sample := strings.Join(parts, "/")
	for _, rule := range rules {
		if rule.Kind != kind || len(rule.Methods) > 0 && !slices.Contains(rule.Methods, method) {
			continue
		}
		if ok, err := api.MatchEdgeRulePath(rule.Path, sample); err == nil && ok {
			return true
		}
	}
	return false
}

func share(part, total int64) float64 {
	if total <= 0 {
		return 0
	}
	return float64(part) / float64(total)
}

func percent(part, total int64) string {
	return fmt.Sprintf("%.0f%%", share(part, total)*100)
}
