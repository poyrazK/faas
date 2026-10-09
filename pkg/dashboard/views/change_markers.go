package views

import (
	"fmt"
	"html/template"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// changeMarkerColors keeps one colour per change source (ADR-741) so the
// strip reads at a glance: deploys and rollouts, configuration, incidents,
// and health each have their own hue.
var changeMarkerColors = map[string]string{
	api.ChangeSourceDeployment:    "#1a4480",
	api.ChangeSourceEdgeRule:      "#6b4fbb",
	api.ChangeSourceRuntimeConfig: "#6b4fbb",
	api.ChangeSourceActivity:      "#6b4fbb",
	api.ChangeSourceIncident:      "#c0392b",
	api.ChangeSourceHealth:        "#d49000",
}

// RenderChangeMarkerStrip draws one vertical tick per change at its position
// in [since, until), on the same width as the sparklines above it, so a
// marker lines up with the error-rate or latency movement it may explain.
// Each tick carries a <title> with the time and summary for hover.
func RenderChangeMarkerStrip(since, until time.Time, events []api.AppChangeEvent, width, height int) template.HTML {
	if width == 0 {
		width = defaultWidth
	}
	if height == 0 {
		height = defaultHeight
	}
	span := until.Sub(since)
	if span <= 0 || len(events) == 0 {
		return template.HTML("")
	}
	var b strings.Builder
	fmt.Fprintf(&b,
		`<svg viewBox="0 0 %d %d" width="%d" height="%d" role="img" aria-label="%d changes in window" preserveAspectRatio="none">`,
		width, height, width, height, len(events))
	for _, e := range events {
		if e.At.Before(since) || !e.At.Before(until) {
			continue
		}
		x := float64(width) * float64(e.At.Sub(since)) / float64(span)
		color, ok := changeMarkerColors[e.Source]
		if !ok {
			color = latencyP50
		}
		fmt.Fprintf(&b,
			`<line x1="%.1f" y1="0" x2="%.1f" y2="%d" stroke="%s" stroke-width="2"><title>%s — %s</title></line>`,
			x, x, height, color, escapeAttr(e.At.UTC().Format("2006-01-02 15:04Z")), escapeAttr(e.Summary))
	}
	b.WriteString(`</svg>`)
	//nolint:gosec // G203: fixed-shape SVG of numbers, compile-time colours,
	// and escapeAttr-escaped time and summary text.
	return template.HTML(b.String())
}
