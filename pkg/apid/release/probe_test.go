package release

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1446
func TestMergeProbeTargets(t *testing.T) {
	gated := []state.RouteProbeTarget{
		{AppID: "a", CandidateID: "a2"},
		{AppID: "b", CandidateID: "b2"},
	}
	defaults := []state.RouteProbeTarget{
		{AppID: "b", CandidateID: "b2", StableID: "b1"},
		{AppID: "c", CandidateID: "c2", StableID: "c1"},
	}
	got := MergeProbeTargets(gated, defaults)
	want := []ProbeTarget{
		{RouteProbeTarget: state.RouteProbeTarget{AppID: "a", CandidateID: "a2"}, Gated: true},
		{RouteProbeTarget: state.RouteProbeTarget{AppID: "b", CandidateID: "b2", StableID: "b1"}, Gated: true, Default: true},
		{RouteProbeTarget: state.RouteProbeTarget{AppID: "c", CandidateID: "c2", StableID: "c1"}, Default: true},
	}
	if len(got) != len(want) {
		t.Fatalf("targets = %+v, want %+v", got, want)
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Fatalf("target %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// adr: 1446
// TestDefaultProbeRoutes pins ADR-1449: the app's paths replace "/", invalid
// or surplus paths are dropped, and a round's probes split so the total still
// meets the request floor.
func TestDefaultProbeRoutes(t *testing.T) {
	for _, tc := range []struct {
		paths []string
		want  []string
	}{
		{nil, []string{"/"}},
		{[]string{"/healthz"}, []string{"/healthz"}},
		{[]string{"/healthz", "bad", "/api/orders"}, []string{"/healthz", "/api/orders"}},
		{[]string{"bad"}, []string{"/"}},
		{[]string{"/a", "/b", "/c", "/d"}, []string{"/a", "/b", "/c"}},
	} {
		routes := DefaultProbeRoutes(tc.paths)
		if len(routes) != len(tc.want) {
			t.Fatalf("DefaultProbeRoutes(%q) = %d routes, want %q", tc.paths, len(routes), tc.want)
		}
		for i, r := range routes {
			if r.Method != "GET" || r.Path != tc.want[i] || r.Probe == nil || r.Probe.Path != tc.want[i] {
				t.Fatalf("DefaultProbeRoutes(%q)[%d] = %+v, want GET %s", tc.paths, i, r, tc.want[i])
			}
		}
		if total := DefaultProbeRequests(len(routes)) * len(routes); total < api.DefaultRouteProbeRequestsPerRound {
			t.Fatalf("%d routes get %d probes in total, below the %d floor", len(routes), total, api.DefaultRouteProbeRequestsPerRound)
		}
	}
}

func TestDefaultProbesFollowTheSafeDefaultKillSwitch(t *testing.T) {
	if !DefaultProbesEnabled() {
		t.Fatal("default probes off without the kill switch")
	}
	t.Setenv(DefaultEnabledEnv, "false")
	if DefaultProbesEnabled() {
		t.Fatal("default probes on with the safe default turned off")
	}
	if r := DefaultProbeRoute(); r.Method != "GET" || r.Path != DefaultProbePath || r.Probe == nil || r.Probe.Path != DefaultProbePath {
		t.Fatalf("default probe route = %+v", r)
	}
}
