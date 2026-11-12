package views

import (
	"math"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/appmetrics"
)

func TestSparklinesKeepNonFiniteGaps(t *testing.T) {
	points := []appmetrics.SparklinePoint{{Value: 10}, {Value: 20}, {Value: math.NaN()}, {Value: math.Inf(1)}, {Value: 30}, {Value: 40}}
	for _, area := range []bool{false, true} {
		svg := renderFiniteSparkline(points, 104, 40, "blue", "0.2", area)
		if strings.Contains(svg, "NaN") || strings.Contains(svg, "Inf") {
			t.Fatalf("nonfinite coordinates: %s", svg)
		}
		element := "<polyline"
		if area {
			element = "<path"
		}
		if strings.Count(svg, element) != 2 {
			t.Fatalf("want two finite segments: %s", svg)
		}
		if !strings.Contains(svg, "82.00,") || !strings.Contains(svg, "102.00,") {
			t.Fatalf("lost original x positions: %s", svg)
		}
	}
	missing := []appmetrics.SparklinePoint{{Value: math.NaN()}, {Value: math.Inf(-1)}}
	if got := RenderAreaSparkline(missing, 100, 40, "blue", "0.2"); got != "" {
		t.Fatalf("all-missing chart=%s", got)
	}
	if got := RenderLatencySparkline(LatencySparklineView{P95: missing}, 100, 40); got != "" {
		t.Fatalf("all-missing latency=%s", got)
	}
}
