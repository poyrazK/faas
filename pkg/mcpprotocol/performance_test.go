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
	if len(snapshot.Tools) != 1 || snapshot.Tools[0].Tool != "greet" || snapshot.Tools[0].P95 == nil || *snapshot.Tools[0].P95 != .25 || len(snapshot.Traffic) != 1 {
		t.Fatalf("bad projection: %+v", snapshot)
	}
	reader.sparse = true
	snapshot, err = Fetch(context.Background(), reader, id, now)
	if err != nil || snapshot.Tools[0].P50 != nil || snapshot.Tools[0].P95 != nil || snapshot.Tools[0].P99 != nil || snapshot.Tools[0].StreamP95 != nil {
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
