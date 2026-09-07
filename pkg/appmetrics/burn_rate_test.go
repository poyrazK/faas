package appmetrics_test

import (
	"context"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/appmetrics"
)

type burnRateStub struct {
	short float64
	long  float64
	seen  []string
}

func (s *burnRateStub) QueryScalar(_ context.Context, query string) (float64, error) {
	s.seen = append(s.seen, query)
	if strings.Contains(query, "[1h]") {
		return s.short, nil
	}
	return s.long, nil
}

func TestFetchSLOBurnRate_UsesBothWindows(t *testing.T) {
	cases := []struct {
		name       string
		short      float64
		long       float64
		want       float64
		wantFiring bool
	}{
		{name: "both windows breach", short: 15, long: 15, want: 15, wantFiring: true},
		{name: "long window at six times", short: 15, long: 6, want: 14.4, wantFiring: false},
		{name: "long window below six times", short: 20, long: 5, want: 12, wantFiring: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stub := &burnRateStub{short: tc.short, long: tc.long}
			got, source := appmetrics.FetchSLOBurnRate(context.Background(), stub, nil, "app-1")
			if source != appmetrics.SourcePrometheus {
				t.Fatalf("source = %q; want %q", source, appmetrics.SourcePrometheus)
			}
			if got != tc.want {
				t.Fatalf("burn rate = %v; want %v", got, tc.want)
			}
			if (got > appmetrics.SLOBurnRateShortLimit) != tc.wantFiring {
				t.Fatalf("threshold verdict for %v = %v; want %v", got, got > appmetrics.SLOBurnRateShortLimit, tc.wantFiring)
			}
			if len(stub.seen) != 2 || !strings.Contains(stub.seen[0], "[1h]") || !strings.Contains(stub.seen[1], "[6h]") {
				t.Fatalf("queries = %v; want 1h and 6h windows", stub.seen)
			}
		})
	}
}
