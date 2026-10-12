// adr: 1131
package servicemap

import (
	"context"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestSuggestWakeAhead(t *testing.T) {
	edge := func(calls, cold int64) api.ServiceMapEdge { return api.ServiceMapEdge{Calls: calls, ColdCalls: cold} }
	for _, tc := range []struct {
		name    string
		edge    api.ServiceMapEdge
		optedIn bool
		want    bool
	}{
		{name: "mostly cold, enough evidence", edge: edge(40, 30), want: true},
		{name: "exactly at the bar", edge: edge(40, 20), want: true},
		{name: "already opted in", edge: edge(40, 30), optedIn: true, want: false},
		{name: "too few cold calls", edge: edge(20, 19), want: false},
		{name: "mostly warm", edge: edge(100, 30), want: false},
	} {
		if got := suggestWakeAhead(tc.edge, tc.optedIn); got != tc.want {
			t.Errorf("%s: suggest = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// The opt-in decorates the map; a failed lookup must not fail it or suggest
// wake-ahead for a caller whose setting is unknown.
func TestMarkWakeAheadToleratesLookupFailure(t *testing.T) {
	s := &Service{Host: &fakeHost{wakeAhead: map[string]bool{paymentsID: true}, wakeErr: map[string]bool{publicAPIID: true}}}
	nodes := []api.ServiceMapNode{{AppID: publicAPIID}, {AppID: paymentsID}}
	edges := []api.ServiceMapEdge{
		{CallerAppID: publicAPIID, TargetAppID: paymentsID, Calls: 50, ColdCalls: 50},
		{CallerAppID: paymentsID, TargetAppID: ledgerID, Calls: 50, ColdCalls: 50},
	}
	s.markWakeAhead(context.Background(), nodes, edges)
	if nodes[0].WakeAheadEnabled || !nodes[1].WakeAheadEnabled {
		t.Fatalf("nodes = %+v, want only payments opted in", nodes)
	}
	if edges[0].SuggestWakeAhead {
		t.Fatal("suggested wake-ahead for a caller whose opt-in could not be read")
	}
	if edges[1].SuggestWakeAhead {
		t.Fatal("suggested wake-ahead for a caller that already opted in")
	}
}
