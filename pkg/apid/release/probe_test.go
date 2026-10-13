package release

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1160
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
		if got[i] != want[i] {
			t.Fatalf("target %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

// adr: 1160
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
