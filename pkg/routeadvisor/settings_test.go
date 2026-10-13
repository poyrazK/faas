package routeadvisor

// adr: 1152

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func settingInputs() SettingInputs {
	return SettingInputs{
		App: "api",
		Learned: api.ServiceWakeAheadLearned{Source: api.RouteEvidenceSourceLive, Window: "24h", CallerWakes: 40, Targets: []api.ServiceWakeAheadTarget{
			{Slug: "auth", CallShare: 0.9, ColdShare: 0.8, Qualifies: true},
			{Slug: "search", CallShare: 0.3, ColdShare: 0.9},
		}},
		PrioritySource: api.RoutePrioritySourceNone,
		Queue: api.RoutePriorityQueueStats{Source: api.RouteEvidenceSourceLive, Window: "24h", Classes: []api.RoutePriorityQueueClass{
			{Class: "normal", Rejected: 12, Displaced: 3},
		}},
		Routes: []RouteStats{
			{Method: "GET", Path: "/catalog", Requests: 9000},
			{Method: "POST", Path: "/checkout", Requests: 800},
			{Method: "POST", Path: "/login", Requests: 600},
			{Method: "DELETE", Path: "/cart/{id}", Requests: 300},
			{Method: "PUT", Path: "/profile", Requests: 100},
		},
	}
}

func kinds(s []api.RouteAdviceSuggestion) []string {
	out := []string{}
	for _, x := range s {
		out = append(out, x.Kind)
	}
	return out
}

func TestSettingSuggestions(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*SettingInputs)
		want   []string
	}{
		{"both", func(*SettingInputs) {}, []string{api.RouteAdviceKindWakeAhead, api.RouteAdviceKindPriority}},
		{"already opted in", func(in *SettingInputs) { in.WakeAheadEnabled = true }, []string{api.RouteAdviceKindPriority}},
		{"no qualifying edge", func(in *SettingInputs) { in.Learned.Targets[0].Qualifies = false }, []string{api.RouteAdviceKindPriority}},
		{"no wake-ahead evidence", func(in *SettingInputs) { in.Learned.Source = api.RouteEvidenceSourceUnavailable }, []string{api.RouteAdviceKindPriority}},
		{"priorities already set", func(in *SettingInputs) { in.PrioritySource = api.RoutePrioritySourceConfigured }, []string{api.RouteAdviceKindWakeAhead}},
		{"route-health default applies", func(in *SettingInputs) { in.PrioritySource = api.RoutePrioritySourceRouteHealth }, []string{api.RouteAdviceKindWakeAhead}},
		{"too little shedding", func(in *SettingInputs) { in.Queue.Classes[0].Rejected = 2 }, []string{api.RouteAdviceKindWakeAhead}},
		{"no write routes", func(in *SettingInputs) { in.Routes = in.Routes[:1] }, []string{api.RouteAdviceKindWakeAhead}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := settingInputs()
			tt.mutate(&in)
			got := kinds(SettingSuggestions(in))
			if len(got) != len(tt.want) {
				t.Fatalf("kinds = %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("kinds = %v, want %v", got, tt.want)
				}
			}
		})
	}
}

func TestSettingSuggestionContents(t *testing.T) {
	got := SettingSuggestions(settingInputs())
	wake, prio := got[0], got[1]
	if wake.Setting == nil || wake.Setting.WakeAhead == nil || !*wake.Setting.WakeAhead || len(wake.Rules) != 0 || wake.ID != SuggestionID(api.RouteAdviceKindWakeAhead, "", "") {
		t.Fatalf("wake-ahead suggestion = %+v", wake)
	}
	rules := prio.Setting.RoutePriorities
	if len(rules) != api.RouteAdviceMaxCriticalRoutes || rules[0] != (api.RoutePriorityRule{Method: "POST", Path: "/checkout", Class: api.RoutePriorityCritical}) ||
		rules[2].Path != "/cart/{id}" {
		t.Fatalf("priority rules = %+v; want the busiest write routes, critical", rules)
	}
	if err := api.ValidateRoutePriorities(rules); err != nil {
		t.Fatalf("proposed priorities invalid: %v", err)
	}
}
