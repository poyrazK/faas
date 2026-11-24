package mcpprotocol

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/promql"
)

type performanceReader struct {
	app    string
	now    time.Time
	failed bool
	sparse bool
}

func (r performanceReader) QueryVector(_ context.Context, q string) ([]promql.VectorSample, error) {
	if !strings.Contains(q, `app_id="`+r.app+`"`) {
		return nil, errors.New("unscoped query")
	}
	if r.failed {
		return nil, errors.New("backend failed")
	}
	if strings.Contains(q, "last_event") {
		return []promql.VectorSample{{Value: float64(r.now.Unix())}}, nil
	}
	value := 50.0
	if strings.Contains(q, "histogram_quantile") {
		value = .25
	}
	if strings.Contains(q, "_count") && r.sparse {
		value = 19
	}
	return []promql.VectorSample{{Labels: map[string]string{"tool": "greet"}, Value: value}, {Labels: map[string]string{"tool": "private-unreviewed-name"}, Value: 900}}, nil
}
func (r performanceReader) QueryRange(_ context.Context, q, start, end, step string) ([]struct {
	Metric map[string]string
	Values []promql.QueryRangeSample
}, error) {
	if !strings.Contains(q, `app_id="`+r.app+`"`) {
		return nil, errors.New("unscoped query")
	}
	if r.failed {
		return nil, errors.New("backend failed")
	}
	return []struct {
		Metric map[string]string
		Values []promql.QueryRangeSample
	}{{Values: []promql.QueryRangeSample{{Timestamp: r.now.Unix(), Value: 1}}}}, nil
}
func TestPerformanceQueriesScopeAndTrafficGate(t *testing.T) {
	id := uuid.NewString()
	now := time.Now()
	reader := performanceReader{app: id, now: now}
	snapshot, err := Fetch(context.Background(), reader, id, now)
	if err != nil {
		t.Fatal(err)
	}
	// Observed tools, most calls first (ADR-20261122194324).
	if len(snapshot.Tools) != 2 || snapshot.Tools[0].Tool != "private-unreviewed-name" || snapshot.Tools[1].Tool != "greet" ||
		snapshot.Tools[1].P95 == nil || *snapshot.Tools[1].P95 != .25 || snapshot.Tools[1].Mismatches != 50 || len(snapshot.Traffic) != 1 {
		t.Fatalf("bad projection: %+v", snapshot)
	}
	reader.sparse = true
	snapshot, err = Fetch(context.Background(), reader, id, now)
	if err != nil || snapshot.Tools[1].P50 != nil || snapshot.Tools[1].P95 != nil || snapshot.Tools[1].P99 != nil || snapshot.Tools[1].StreamP95 != nil {
		t.Fatal("sparse traffic produced percentiles")
	}
	reader.failed = true
	if snapshot, err = Fetch(context.Background(), reader, id, now); err == nil || len(snapshot.Tools) != 0 {
		t.Fatal("backend failure reported as healthy")
	}
	if _, err = Fetch(context.Background(), reader, `bad"} or vector(1)`, now); err == nil {
		t.Fatal("invalid app selector accepted")
	}
}

// adr: 20261122194324 — compute estimate at billed memory and the overage rate.
func TestEstimateToolCompute(t *testing.T) {
	// One hour at 1016 MB of plan RAM is exactly 1 GB-hour billed.
	got := EstimateToolCompute(3600, 1016)
	if got.GBSeconds != 3600 || got.Millicents != 5000 {
		t.Fatalf("one GB-hour = %+v", got)
	}
	if small := EstimateToolCompute(0.25, 248); small.Millicents != 0 || small.GBSeconds != 0.0625 {
		t.Fatalf("quarter second = %+v", small)
	}
	for _, bad := range []ToolCompute{EstimateToolCompute(-1, 512), EstimateToolCompute(1, 0)} {
		if bad != (ToolCompute{}) {
			t.Fatalf("invalid input estimated %+v", bad)
		}
	}
}
