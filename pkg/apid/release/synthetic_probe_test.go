package release

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 20261022082305 — a canary's synthetic gates join its probe round; a
// canary beside several serving predecessors is not probed.
func TestAddSyntheticGates(t *testing.T) {
	existing := []ProbeTarget{{RouteProbeTarget: state.RouteProbeTarget{AppID: "a1", CandidateID: "c1", StableID: "s1"}, Default: true}}
	gates := []state.SyntheticGateProbeTarget{
		{AppID: "a1", CandidateID: "c1", StableID: "s1", GateID: "g1", Path: "/checkout"},
		{AppID: "a1", CandidateID: "c1", StableID: "s1", GateID: "g2", Path: "/login"},
		{AppID: "a2", Slug: "two", CandidateID: "c2", StableID: "s2", GateID: "g3", Path: "/"},
		{AppID: "a3", CandidateID: "c3", StableID: "s3a", GateID: "g4", Path: "/"},
		{AppID: "a3", CandidateID: "c3", StableID: "s3b", GateID: "g4", Path: "/"},
	}
	got := AddSyntheticGates(existing, gates)
	if len(got) != 2 {
		t.Fatalf("targets = %+v, want the existing round and a new one", got)
	}
	if !got[0].Default || len(got[0].Synthetic) != 2 || got[0].Synthetic[1].GateID != "g2" {
		t.Fatalf("existing round = %+v", got[0])
	}
	if got[1].AppID != "a2" || got[1].Slug != "two" || got[1].StableID != "s2" || got[1].Default || got[1].Gated || len(got[1].Synthetic) != 1 {
		t.Fatalf("new round = %+v", got[1])
	}
}
