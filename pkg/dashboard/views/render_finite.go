package views

import (
	"fmt"
	"math"
	"strings"

	"github.com/onebox-faas/faas/pkg/appmetrics"
)

func finiteSparklineValue(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func finiteSparklineCount(points []appmetrics.SparklinePoint) int {
	n := 0
	for _, p := range points {
		if finiteSparklineValue(p.Value) {
			n++
		}
	}
	return n
}

// Each finite run is drawn separately, with its original positions and a
// shared scale. Missing samples remain gaps rather than joining across them
// or emitting SVG coordinates that a browser cannot parse.
func renderFiniteSparkline(points []appmetrics.SparklinePoint, width, height int, color, opacity string, area bool) string {
	maxY := 0.0
	for _, p := range points {
		if finiteSparklineValue(p.Value) && p.Value > maxY {
			maxY = p.Value
		}
	}
	if maxY == 0 {
		maxY = 1
	}
	projection := sparklineProjection{width: width, height: height, total: len(points), maxY: maxY}
	var out strings.Builder
	for start := 0; start < len(points); {
		if !finiteSparklineValue(points[start].Value) {
			start++
			continue
		}
		end := start + 1
		for end < len(points) && finiteSparklineValue(points[end].Value) {
			end++
		}
		out.WriteString(projection.segment(points[start:end], start, color, opacity, area))
		start = end
	}
	return out.String()
}

type sparklineProjection struct {
	width, height, total int
	maxY                 float64
}

func (p sparklineProjection) xy(index int, value float64) (float64, float64) {
	x := float64(p.width) / 2
	if p.total > 1 {
		x = float64(padLeft) + float64(p.width-padLeft-padRight)*float64(index)/float64(p.total-1)
	}
	y := float64(padTop) + float64(p.height-padTop-padBottom)*(1-value/p.maxY)
	return x, y
}

func (p sparklineProjection) segment(points []appmetrics.SparklinePoint, offset int, color, opacity string, area bool) string {
	if len(points) == 1 {
		x, y := p.xy(offset, points[0].Value)
		if area {
			return fmt.Sprintf(`<circle cx="%.2f" cy="%.2f" r="1.5" fill=%q fill-opacity=%q stroke="none"/>`, x, y, color, opacity)
		}
		return fmt.Sprintf(`<circle cx="%.2f" cy="%.2f" r="1.5" fill=%q stroke="none"/>`, x, y, color)
	}
	coords := make([]string, 0, len(points))
	for i, point := range points {
		x, y := p.xy(offset+i, point.Value)
		coords = append(coords, fmt.Sprintf("%.2f,%.2f", x, y))
	}
	if !area {
		return fmt.Sprintf(`<polyline fill="none" stroke=%q stroke-width="1.25" stroke-linejoin="round" points="%s"/>`, color, strings.Join(coords, " "))
	}
	firstX, _ := p.xy(offset, 0)
	lastX, _ := p.xy(offset+len(points)-1, 0)
	bottom := float64(p.height - padBottom)
	path := fmt.Sprintf("M %.2f,%.2f L %s L %.2f,%.2f Z", firstX, bottom, strings.Join(coords, " L "), lastX, bottom)
	return fmt.Sprintf(`<path d=%q fill=%q fill-opacity=%q stroke="none"/>`, path, color, opacity)
}
