package main_test

import (
	"bytes"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// Production-us rc.294: `gregale metrics h14-py` printed "Requests: 1" and
// "p50=0.0ms p95=0.0ms p99=0.0ms"; the server reports a quantile without
// histogram samples as 0 (Hunt 14 H14-46).
func TestAppMetricsSaysWhenNoLatencySamplesExist(t *testing.T) {
	var out bytes.Buffer
	main.RenderAppMetrics(&out, "h14-py", api.AppMetricsResponse{RequestCount: 1})
	got := out.String()
	if strings.Contains(got, "p50=0.0ms") || !strings.Contains(got, "no latency samples in this window") {
		t.Fatalf("metrics without latency samples:\n%s", got)
	}
}
