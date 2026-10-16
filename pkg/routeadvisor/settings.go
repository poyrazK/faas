package routeadvisor

import (
	"fmt"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// SettingInputs is the app-level evidence for setting suggestions (ADR-1152).
type SettingInputs struct {
	App              string
	WakeAheadEnabled bool
	Learned          api.ServiceWakeAheadLearned
	PrioritySource   string
	Queue            api.RoutePriorityQueueStats
	Routes           []RouteStats
}

// writeMethods are proposed as critical when the queue sheds requests: they
// are the routes that change state for the caller, such as checkout or login.
var writeMethods = map[string]bool{"POST": true, "PUT": true, "PATCH": true, "DELETE": true}

// SettingSuggestions proposes turning on service wake-ahead when the gateways
// measured qualifying edges, and route priorities when the warm-capacity
// queue turned requests away while none are set.
func SettingSuggestions(in SettingInputs) []api.RouteAdviceSuggestion {
	out := []api.RouteAdviceSuggestion{}
	if s, ok := wakeAheadSuggestion(in); ok {
		out = append(out, s)
	}
	if s, ok := prioritySuggestion(in); ok {
		out = append(out, s)
	}
	return out
}

func wakeAheadSuggestion(in SettingInputs) (api.RouteAdviceSuggestion, bool) {
	if in.WakeAheadEnabled || in.Learned.Source != api.RouteEvidenceSourceLive {
		return api.RouteAdviceSuggestion{}, false
	}
	var names, evidence []string
	for _, t := range in.Learned.Targets {
		if t.Qualifies {
			names = append(names, t.Slug)
			evidence = append(evidence, fmt.Sprintf("%s (called after %.0f%% of cold wakes, parked on %.0f%% of those calls)", t.Slug, t.CallShare*100, t.ColdShare*100))
		}
	}
	if len(names) == 0 {
		return api.RouteAdviceSuggestion{}, false
	}
	on := true
	return api.RouteAdviceSuggestion{
		ID:        SuggestionID(api.RouteAdviceKindWakeAhead, "", ""),
		Kind:      api.RouteAdviceKindWakeAhead,
		Title:     "Turn on service wake-ahead for " + in.App,
		Rationale: fmt.Sprintf("Over %d cold wakes, %s usually called %s right after waking, and found it parked.", in.Learned.CallerWakes, in.App, strings.Join(evidence, ", ")),
		Impact:    api.RouteAdviceImpact{Summary: fmt.Sprintf("on a cold wake, %s would start restoring alongside %s instead of after its first call", strings.Join(names, ", "), in.App)},
		Cautions:  []string{"A service woken ahead runs, and is billed, from the moment it is woken; an unused one parks again after its idle timeout."},
		Rules:     []api.CreateEdgeRuleRequest{},
		Setting:   &api.RouteAdviceSetting{WakeAhead: &on},
	}, true
}

func prioritySuggestion(in SettingInputs) (api.RouteAdviceSuggestion, bool) {
	if in.PrioritySource != api.RoutePrioritySourceNone || in.Queue.Source != api.RouteEvidenceSourceLive {
		return api.RouteAdviceSuggestion{}, false
	}
	var shed int64
	for _, c := range in.Queue.Classes {
		shed += c.Rejected + c.Displaced
	}
	if shed < api.RouteAdviceMinShed {
		return api.RouteAdviceSuggestion{}, false
	}
	writes := []RouteStats{}
	for _, r := range in.Routes {
		if writeMethods[r.Method] {
			if _, ok := api.RoutePriorityGlob(r.Path); ok {
				writes = append(writes, r)
			}
		}
	}
	if len(writes) == 0 {
		return api.RouteAdviceSuggestion{}, false
	}
	slices.SortStableFunc(writes, func(a, b RouteStats) int {
		if a.Requests != b.Requests {
			if a.Requests > b.Requests {
				return -1
			}
			return 1
		}
		return strings.Compare(a.Method+" "+a.Path, b.Method+" "+b.Path)
	})
	if len(writes) > api.RouteAdviceMaxCriticalRoutes {
		writes = writes[:api.RouteAdviceMaxCriticalRoutes]
	}
	rules := make([]api.RoutePriorityRule, 0, len(writes))
	names := make([]string, 0, len(writes))
	for _, r := range writes {
		rules = append(rules, api.RoutePriorityRule{Method: r.Method, Path: r.Path, Class: api.RoutePriorityCritical})
		names = append(names, r.Method+" "+r.Path)
	}
	return api.RouteAdviceSuggestion{
		ID:        SuggestionID(api.RouteAdviceKindPriority, "", ""),
		Kind:      api.RouteAdviceKindPriority,
		Title:     "Set route priorities for " + in.App,
		Rationale: fmt.Sprintf("%d requests were turned away or lost their queue place in the last %s while every instance was busy, and no route priorities are set.", shed, in.Queue.Window),
		Impact:    api.RouteAdviceImpact{Summary: fmt.Sprintf("%s would be served first and turned away last when the app is saturated", strings.Join(names, ", "))},
		Cautions:  []string{"These are the busiest routes that change data; mark the routes your callers depend on most before applying."},
		Rules:     []api.CreateEdgeRuleRequest{},
		Setting:   &api.RouteAdviceSetting{RoutePriorities: rules},
	}, true
}
