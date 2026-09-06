// render.go — Issue #696 / ADR-082 dashboard follow-up PR:
// the inline-SVG sparkline renderer the dashboard uses for the
// per-app SLO card. Pure functions; no JS dependency; the
// template calls them via `{{ call .RenderLatencySparkline .Data.SLOApp.LatencySparkline ... }}`
// or directly via the pre-computed HTML attribute.
//
// The renderer is deliberately minimal: width 120, height 30
// (the plan calls out ~120×30px — a fill row on the existing
// Metrics card). Three pre-decided colour tokens for the
// latency triple (p50 / p95 / p99) match the public status
// page palette the existing dashboard uses; the area charts
// re-use the warning-fill for error rate and the info-fill
// for cold-boot rate.
//
// The SVG output is a template.HTML so it sidesteps Go's
// html/template escaping — we are rendering a fixed-shape
// artefact (no user input) and the SVG strings are constructed
// from the package-level constants. The renderer's ONLY inputs
// are float64 sequences and ints; no string concatenation of
// user-supplied content. This is the load-bearing reason
// template.HTML is safe here (the alternative — passing
// template.JS — would require letting the rendering engine
// parse an inline script, which would defeat the "no JS
// dependency" goal).
//
// Accessibility: each render emits role="img" + an aria-label
// describing the trend (rising / flat / falling / no-data),
// so a screen reader can speak the trend without parsing the
// shape. The dashboard's CSP accepts inline SVG via the
// page-level nonce (the page templates already propagate
// Nonce to inline <svg> tags in the existing metrics card).
package views

import (
	"fmt"
	"html/template"
	"math"
	"strings"

	"github.com/onebox-faas/faas/pkg/appmetrics"
)

// Default sparkline dimensions. Overridable by the Renders
// that take explicit width / height — the dashboard's
// existing metrics card uses 120×30, so the per-app SLO
// card matches for layout parity.
const (
	defaultWidth  = 120
	defaultHeight = 30
	// padLeft + padRight reserve a few px on each side so the
	// polyline doesn't kiss the SVG edge. The line chart
	// uses the same padding for parity.
	padLeft   = 2
	padRight  = 2
	padTop    = 4
	padBottom = 4
)

// Palette tokens — match the existing dashboard metrics card.
// Latency p50 / p95 / p99 use three shades of the accent
// (light / medium / dark) so the eye can disambiguate the
// three lines without a legend. Error rate uses the warning
// shade; cold-boot rate uses the info shade. The project's
// dataviz palette pin (light/dark parity, no rainbow
// categorical) is preserved.
const (
	latencyP50   = "#3a6ea5" // light  — closest to the chart background
	latencyP95   = "#1a4480" // medium
	latencyP99   = "#0a2c5e" // dark  — the heaviest line
	areaError    = "#c0392b" // warning — error rate
	areaColdBoot = "#d49000" // info    — cold-boot rate
	areaUsage    = "#4f46e5" // accent — account usage
	areaOpacity  = "0.18"    // the fill is a hint, not a bar
)

// escapeAttr is a stdlib-free XML attribute escaper. The
// renderer only ever emits fixed-shape SVG (no user input
// flows into the attribute strings), but the function is
// kept as a defensive measure against a future maintainer
// accidentally passing a user-supplied string through.
// xml.EscapeText would also work but only on io.Writer.
func escapeAttr(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// emptySeriesLabel is the aria-label the renderer emits when
// a series has zero data points. The template renders the
// visible "no data" badge alongside the SVG so the label's
// "no data" wording never silently diverges from the visual.
const emptySeriesLabel = "no data"

// renderPoints projects a SparklinePoint slice into an
// SVG <polyline> path. width / height are the viewport
// dimensions (px). Empty input → empty string (caller
// emits the empty-state badge).
//
// The x-axis is the time-domain (chronological buckets);
// the y-axis is the value-domain. min/max are scaled to
// the inner viewport (after padding) so a single high
// outlier doesn't squash the rest of the chart. Reasonable
// defaults: min = 0 (latency, error rate and cold-boot
// rate are all non-negative), max = peak value.
func renderPoints(points []appmetrics.SparklinePoint, width, height int, color string) string {
	if len(points) == 0 {
		return ""
	}
	minY := 0.0
	maxY := points[0].Value
	for _, p := range points {
		if p.Value > maxY {
			maxY = p.Value
		}
	}
	if maxY == minY {
		// All values identical. Spread over a thin strip so the
		// line is visible — a degenerate flat line at a single
		// y-coordinate is invisible in a 1px stroke.
		maxY = minY + 1
	}
	innerW := float64(width - padLeft - padRight)
	innerH := float64(height - padTop - padBottom)
	if len(points) == 1 {
		// Single point — render as a centred dot rather than a
		// polyline (the path would be a single coord with no
		// adjoining line, which is invisible at 1px stroke).
		cx := float64(width) / 2
		cy := float64(padTop) + innerH*(1-(points[0].Value-minY)/(maxY-minY))
		return fmt.Sprintf(`<circle cx="%.2f" cy="%.2f" r="1.5" fill=%q stroke="none"/>`, cx, cy, color)
	}
	var b strings.Builder
	// step is the horizontal interval between buckets. For a
	// single-bucket series we already short-circuited; here
	// len(points) >= 2 so step > 0.
	step := innerW / float64(len(points)-1)
	for i, p := range points {
		x := float64(padLeft) + step*float64(i)
		y := float64(padTop) + innerH*(1-(p.Value-minY)/(maxY-minY))
		if i == 0 {
			fmt.Fprintf(&b, "%.2f,%.2f", x, y)
		} else {
			fmt.Fprintf(&b, " %.2f,%.2f", x, y)
		}
	}
	return fmt.Sprintf(`<polyline fill="none" stroke=%q stroke-width="1.25" stroke-linejoin="round" points="%s"/>`, color, b.String())
}

// renderArea projects a SparklinePoint slice into a
// filled-area SVG path. The line is the same as the
// line-chart renderer; the fill is the line closed back
// to the bottom edge, painted with the configured opacity.
// Empty input → empty string.
func renderArea(points []appmetrics.SparklinePoint, width, height int, color, opacity string) string {
	if len(points) == 0 {
		return ""
	}
	minY := 0.0
	maxY := points[0].Value
	for _, p := range points {
		if p.Value > maxY {
			maxY = p.Value
		}
	}
	if maxY == minY {
		maxY = minY + 1
	}
	innerW := float64(width - padLeft - padRight)
	innerH := float64(height - padTop - padBottom)
	bottom := float64(height - padBottom)
	if len(points) == 1 {
		cx := float64(width) / 2
		cy := float64(padTop) + innerH*(1-(points[0].Value-minY)/(maxY-minY))
		return fmt.Sprintf(`<circle cx="%.2f" cy="%.2f" r="1.5" fill=%q fill-opacity=%q stroke="none"/>`,
			cx, cy, color, opacity)
	}
	step := innerW / float64(len(points)-1)
	coords := make([]string, 0, len(points))
	for i, p := range points {
		x := float64(padLeft) + step*float64(i)
		y := float64(padTop) + innerH*(1-(p.Value-minY)/(maxY-minY))
		coords = append(coords, fmt.Sprintf("%.2f,%.2f", x, y))
	}
	// The path: M (first x, bottom) L (each point) L (last x, bottom) Z.
	// We compute the first/last x from the same step so the
	// closed edge lines up with the polyline ends.
	firstX := float64(padLeft)
	lastX := float64(padLeft) + step*float64(len(points)-1)
	path := fmt.Sprintf("M %.2f,%.2f L %s L %.2f,%.2f Z",
		firstX, bottom, strings.Join(coords, " L "), lastX, bottom)
	return fmt.Sprintf(`<path d=%q fill=%q fill-opacity=%q stroke="none"/>`,
		path, color, opacity)
}

// trendLabel returns a coarse trend descriptor for the
// aria-label: "rising" / "falling" / "flat" / "no data".
// The threshold is 5% of the series' own range — small
// jitter reads as "flat" so the screen reader doesn't
// mis-describe a noisy-but-stationary series as "rising".
func trendLabel(points []appmetrics.SparklinePoint) string {
	if len(points) == 0 {
		return emptySeriesLabel
	}
	if len(points) == 1 {
		return "single point"
	}
	first := points[0].Value
	last := points[len(points)-1].Value
	if math.IsNaN(first) || math.IsNaN(last) {
		return emptySeriesLabel
	}
	minY := first
	maxY := first
	for _, p := range points {
		if p.Value < minY {
			minY = p.Value
		}
		if p.Value > maxY {
			maxY = p.Value
		}
	}
	rng := maxY - minY
	if rng == 0 {
		return "flat"
	}
	delta := last - first
	if math.Abs(delta) < 0.05*rng {
		return "flat"
	}
	if delta > 0 {
		return "rising"
	}
	return "falling"
}

// RenderLatencySparkline draws the 3-line sparkline (p50 / p95 / p99)
// the per-app SLO card uses for request_duration. Returns
// template.HTML so the template can embed it directly. Empty
// input returns an empty string (the template renders the
// empty-state badge in that case).
func RenderLatencySparkline(s LatencySparklineView, width, height int) template.HTML {
	if width == 0 {
		width = defaultWidth
	}
	if height == 0 {
		height = defaultHeight
	}
	all := len(s.P50) + len(s.P95) + len(s.P99)
	if all == 0 {
		return template.HTML("")
	}
	label := "p50/p95/p99 latency, " + trendLabel(s.P95) + // p95 is the headline number; trend reads off it
		" (p50/p95/p99 lines)"
	// aria-label is escaped because the trendLabel output is
	// pre-vetted (closed vocabulary: rising/falling/flat/no-data/single-point),
	// but escapeAttr is the right defensive move in case a
	// future change accepts a wider input.
	aria := escapeAttr(label)
	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label=%q preserveAspectRatio="none">`,
		width, height, width, height, aria)
	if line := renderPoints(s.P50, width, height, latencyP50); line != "" {
		b.WriteString(line)
	}
	if line := renderPoints(s.P95, width, height, latencyP95); line != "" {
		b.WriteString(line)
	}
	if line := renderPoints(s.P99, width, height, latencyP99); line != "" {
		b.WriteString(line)
	}
	b.WriteString(`</svg>`)
	//nolint:gosec // G203: b.String() is a fixed-shape SVG built from
	// floats + ints (sparkline points) + escaped aria-label + compile-time
	// colour constants. No user-supplied string flows through — see
	// package docstring. The template.HTML cast is load-bearing: it
	// sidesteps html/template escaping for the pre-rendered SVG.
	return template.HTML(b.String())
}

// RenderAreaSparkline draws a single-series filled-area
// sparkline. Used for both the error rate and cold-boot
// rate rows in the per-app SLO card.
func RenderAreaSparkline(points []appmetrics.SparklinePoint, width, height int, color, opacity string) template.HTML {
	if width == 0 {
		width = defaultWidth
	}
	if height == 0 {
		height = defaultHeight
	}
	if opacity == "" {
		opacity = areaOpacity
	}
	if len(points) == 0 {
		return template.HTML("")
	}
	label := trendLabel(points)
	aria := escapeAttr(label)
	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label=%q preserveAspectRatio="none">`,
		width, height, width, height, aria)
	b.WriteString(renderArea(points, width, height, color, opacity))
	// The line on top of the fill is a 1px stroke from the
	// same colour so the trend is visible at small sizes.
	if line := renderPoints(points, width, height, color); line != "" {
		b.WriteString(line)
	}
	b.WriteString(`</svg>`)
	//nolint:gosec // G203: b.String() is a fixed-shape SVG built from
	// floats + ints (sparkline points) + escaped aria-label + compile-time
	// colour constants. No user-supplied string flows through — see
	// package docstring. The template.HTML cast is load-bearing: it
	// sidesteps html/template escaping for the pre-rendered SVG.
	return template.HTML(b.String())
}

// RenderErrorRateSparkline is the convenience helper
// RenderAreaSparkline is wrapped in for the per-app SLO card.
// The colour / opacity are pre-decided so the call site
// stays one line.
func RenderErrorRateSparkline(points []appmetrics.SparklinePoint, width, height int) template.HTML {
	return RenderAreaSparkline(points, width, height, areaError, areaOpacity)
}

// RenderColdBootRateSparkline is the convenience helper for
// the cold-boot rate row of the per-app SLO card.
func RenderColdBootRateSparkline(points []appmetrics.SparklinePoint, width, height int) template.HTML {
	return RenderAreaSparkline(points, width, height, areaColdBoot, areaOpacity)
}

// RenderUsageSparkline draws the account's daily GB-hour trend. It shares the
// fixed-shape SVG renderer and accessibility contract with the SLO sparklines,
// while using a wider viewport because the usage page has room for 30 days.
func RenderUsageSparkline(points []appmetrics.SparklinePoint, width, height int) template.HTML {
	if width == 0 {
		width = 480
	}
	if height == 0 {
		height = 100
	}
	if len(points) == 0 {
		return template.HTML("")
	}
	label := "daily GB-hours, " + trendLabel(points)
	aria := escapeAttr(label)
	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label=%q preserveAspectRatio="none">`,
		width, height, width, height, aria)
	b.WriteString(renderArea(points, width, height, areaUsage, areaOpacity))
	if line := renderPoints(points, width, height, areaUsage); line != "" {
		b.WriteString(line)
	}
	b.WriteString(`</svg>`)
	//nolint:gosec // G203: b.String() is a fixed-shape SVG built from
	// floats + ints (sparkline points) + escaped aria-label + compile-time
	// colour constants. No user-supplied string flows through. The
	// template.HTML cast is load-bearing for embedding the SVG.
	return template.HTML(b.String())
}
