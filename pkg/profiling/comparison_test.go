package profiling

import (
	"strings"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestViewBoundsRepeatedLongSymbols(t *testing.T) {
	p := cpuFixture(time.Now(), 1e8)
	p.Function[0].Name = strings.Repeat("x", api.ProfileMaxSymbolBytes)
	locations := make([]*profile.Location, 100)
	for i := range locations {
		locations[i] = p.Sample[0].Location[0]
	}
	p.Sample[0].Location = locations
	if _, err := View(p, api.ProfileQuery{}); err == nil {
		t.Fatal("small profile expanded into an unbounded JSON symbol tree")
	}
}

func TestComparisonMatchesFunctionAfterSourceLineMove(t *testing.T) {
	now := time.Now()
	q := api.ProfileQuery{Runtime: "node24", Start: now.Add(-10 * time.Second), End: now}
	a := api.ProfileResponse{Query: q, Functions: []api.ProfileFunction{
		{Name: "hot", File: "app.js", Line: 10, SelfCPUSeconds: 1},
		{Name: "hot", File: "app.js", Line: 11, SelfCPUSeconds: 1},
	}}
	b := api.ProfileResponse{Query: q, Functions: []api.ProfileFunction{{Name: "hot", File: "app.js", Line: 30, SelfCPUSeconds: 4}}}
	result := Compare(a, b)
	if !result.Comparable || len(result.Functions) != 1 {
		t.Fatalf("comparison: %+v", result)
	}
	f := result.Functions[0]
	if f.BaselineCPUPerSecond != .2 || f.CandidateCPUPerSecond != .4 || f.DeltaCPUPerSecond != .2 || f.Line != 30 {
		t.Fatalf("moved function produced a false new/removed delta: %+v", f)
	}
}
