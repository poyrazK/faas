package faas_test

import (
	"encoding/json"
	"testing"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestDifferentialProfileWirePreservesUnknownAndZeroRates(t *testing.T) {
	var out faas.ProfileCompareResponse
	data := []byte(`{"comparable":true,"functions":[{"name":"new","baseline_cpu_per_second":0,"candidate_cpu_per_second":0.3,"delta_cpu_per_second":0,"baseline_observed":false,"candidate_observed":true,"delta_known":false}],"flamegraph":{"name":"all","baseline_cpu_per_second":0.2,"candidate_cpu_per_second":0.3,"delta_cpu_per_second":0.1,"width_cpu_per_second":0.5,"children":[{"name":"new","candidate_cpu_per_second":0.3,"width_cpu_per_second":0.3,"children":[]},{"name":"observedZero","candidate_cpu_per_second":0,"width_cpu_per_second":0,"children":[]}]}}`)
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	newPath, zero := out.Flamegraph.Children[0], out.Flamegraph.Children[1]
	if newPath.BaselineCPUPerSecond != nil || newPath.DeltaCPUPerSecond != nil || zero.CandidateCPUPerSecond == nil || *zero.CandidateCPUPerSecond != 0 || out.Functions[0].DeltaKnown || out.Functions[0].BaselineObserved {
		t.Fatal("unknown and observed zero became indistinguishable")
	}
	encoded, err := json.Marshal(newPath)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(encoded, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["baseline_cpu_per_second"]; ok {
		t.Fatal("unknown baseline serialized as zero")
	}
}
