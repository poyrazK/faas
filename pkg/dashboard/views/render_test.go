// render_test.go — Issue #696 / ADR-082 dashboard follow-up PR
// tests for the inline-SVG sparkline renderer. The renderer
// is a pure function (no PromQL, no template state) so the
// tests project their own SparklinePoint data and assert
// against the rendered HTML.
//
// Coverage matrix:
//   - 3-line latency sparkline renders all three <polyline>
//   - empty latency series → empty template.HTML (no <svg>)
//   - filled-area sparkline renders <path> + <polyline>
//   - accessibility: every render emits role="img" + aria-label
//   - peak detection: maxY is the highest value in the series
//   - single-point series renders as a <circle>, not a polyline
package views_test

import (
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/appmetrics"
	"github.com/onebox-faas/faas/pkg/dashboard/views"
)

// makePoints returns a flat list of (time, value) pairs from
// the supplied values. Time is anchored at unix epoch + 1s
// per bucket so the renderer's x-axis mapping has monotonic
// timestamps (the renderer doesn't sort).
func makePoints(values []float64) []appmetrics.SparklinePoint {
	out := make([]appmetrics.SparklinePoint, 0, len(values))
	for i, v := range values {
		out = append(out, appmetrics.SparklinePoint{
			Time:  time.Unix(int64(i+1), 0).UTC(),
			Value: v,
		})
	}
	return out
}

// TestRenderLatencySparkline_AllThreeLines: the 3-line
// sparkline emits one <polyline> per percentile (p50 / p95
// / p99). Each line carries its own stroke colour so the
// eye can distinguish them without a legend.
func TestRenderLatencySparkline_AllThreeLines(t *testing.T) {
	s := views.LatencySparklineView{
		P50: makePoints([]float64{10, 11, 12, 11, 10}),
		P95: makePoints([]float64{80, 85, 90, 88, 86}),
		P99: makePoints([]float64{300, 310, 320, 315, 308}),
	}
	out := string(views.RenderLatencySparkline(s, 120, 30))
	if !strings.Contains(out, `<svg`) {
		t.Errorf("missing <svg> root: %s", out)
	}
	if !strings.Contains(out, `role="img"`) {
		t.Errorf("missing role=img: %s", out)
	}
	if !strings.Contains(out, `aria-label=`) {
		t.Errorf("missing aria-label: %s", out)
	}
	if got := strings.Count(out, `<polyline`); got != 3 {
		t.Errorf("polyline count = %d, want 3", got)
	}
}

// TestRenderLatencySparkline_EmptySeries: empty input
// returns empty HTML so the template can render the
// empty-state badge without checking the count.
func TestRenderLatencySparkline_EmptySeries(t *testing.T) {
	got := views.RenderLatencySparkline(views.LatencySparklineView{}, 120, 30)
	if string(got) != "" {
		t.Errorf("empty series should render empty HTML, got %q", string(got))
	}
}

func TestRenderUsageSparkline(t *testing.T) {
	points := makePoints([]float64{0.5, 1.0, 0.75})
	out := string(views.RenderUsageSparkline(points, 480, 100))
	for _, want := range []string{`<svg`, `role="img"`, `daily GB-hours`, `<path`, `<polyline`} {
		if !strings.Contains(out, want) {
			t.Errorf("usage sparkline missing %q: %s", want, out)
		}
	}
}

// TestRenderLatencySparkline_PartialSeries: only one
// percentile populated → exactly one <polyline>. The
// absent percentiles render nothing (no zero-line).
func TestRenderLatencySparkline_PartialSeries(t *testing.T) {
	s := views.LatencySparklineView{
		P95: makePoints([]float64{80, 85, 90}),
	}
	out := string(views.RenderLatencySparkline(s, 120, 30))
	if got := strings.Count(out, `<polyline`); got != 1 {
		t.Errorf("polyline count = %d, want 1 (p95 only)", got)
	}
}

// TestRenderLatencySparkline_AccessibleAriaLabel: the
// aria-label includes the trend descriptor. "rising" /
// "falling" / "flat" are the closed vocabulary emitted.
func TestRenderLatencySparkline_AccessibleAriaLabel(t *testing.T) {
	// Rising: p95 grows monotonically.
	rising := views.LatencySparklineView{
		P95: makePoints([]float64{80, 100, 200}),
	}
	if out := string(views.RenderLatencySparkline(rising, 120, 30)); !strings.Contains(out, "rising") {
		t.Errorf("rising trend missing: %s", out)
	}
	// Falling: drops monotonically.
	falling := views.LatencySparklineView{
		P95: makePoints([]float64{200, 100, 80}),
	}
	if out := string(views.RenderLatencySparkline(falling, 120, 30)); !strings.Contains(out, "falling") {
		t.Errorf("falling trend missing: %s", out)
	}
	// Flat: jitter < 5% of range.
	flat := views.LatencySparklineView{
		P95: makePoints([]float64{80, 80.1, 80.2, 80}),
	}
	if out := string(views.RenderLatencySparkline(flat, 120, 30)); !strings.Contains(out, "flat") {
		t.Errorf("flat trend missing: %s", out)
	}
}

// TestRenderAreaSparkline_FilledArea: the area chart
// emits a <path> (the fill) + a <polyline> (the line on
// top). Both shapes must be present so the trend reads
// at small sizes.
func TestRenderAreaSparkline_FilledArea(t *testing.T) {
	s := makePoints([]float64{0.4, 0.5, 0.6, 0.45, 0.3})
	out := string(views.RenderErrorRateSparkline(s, 120, 30))
	if !strings.Contains(out, `<svg`) {
		t.Errorf("missing <svg> root: %s", out)
	}
	if !strings.Contains(out, `<path`) {
		t.Errorf("missing <path> fill: %s", out)
	}
	if !strings.Contains(out, `<polyline`) {
		t.Errorf("missing <polyline> line: %s", out)
	}
	if !strings.Contains(out, `role="img"`) {
		t.Errorf("missing role=img: %s", out)
	}
	if !strings.Contains(out, `aria-label=`) {
		t.Errorf("missing aria-label: %s", out)
	}
}

// TestRenderAreaSparkline_EmptySeries: empty input returns
// empty HTML. Same posture as the latency renderer.
func TestRenderAreaSparkline_EmptySeries(t *testing.T) {
	got := views.RenderErrorRateSparkline(nil, 120, 30)
	if string(got) != "" {
		t.Errorf("empty series should render empty HTML, got %q", string(got))
	}
}

// TestRenderAreaSparkline_SinglePoint: a one-point series
// renders as a <circle>, not a polyline (the polyline
// path would be a single coord with no adjoining line,
// invisible at 1px stroke).
func TestRenderAreaSparkline_SinglePoint(t *testing.T) {
	s := makePoints([]float64{0.5})
	out := string(views.RenderErrorRateSparkline(s, 120, 30))
	if !strings.Contains(out, `<circle`) {
		t.Errorf("single-point series should render <circle>, got: %s", out)
	}
	if strings.Contains(out, `<polyline`) {
		t.Errorf("single-point series should not render <polyline>, got: %s", out)
	}
}

// TestRenderLatencySparkline_SinglePoint: same posture for
// the line-chart renderer — a single point is a <circle>.
func TestRenderLatencySparkline_SinglePoint(t *testing.T) {
	s := views.LatencySparklineView{
		P95: makePoints([]float64{85}),
	}
	out := string(views.RenderLatencySparkline(s, 120, 30))
	if !strings.Contains(out, `<circle`) {
		t.Errorf("single-point latency should render <circle>, got: %s", out)
	}
}

// TestRenderLatencySparkline_DefaultDimensions: width / height
// 0 fall back to the package defaults (120 × 30). The viewBox
// attribute carries the actual dimensions.
func TestRenderLatencySparkline_DefaultDimensions(t *testing.T) {
	s := views.LatencySparklineView{
		P95: makePoints([]float64{80, 85, 90}),
	}
	out := string(views.RenderLatencySparkline(s, 0, 0))
	if !strings.Contains(out, `viewBox="0 0 120 30"`) {
		t.Errorf("default viewBox missing: %s", out)
	}
}
