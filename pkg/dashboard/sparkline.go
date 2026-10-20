package dashboard

import (
	"strconv"
	"strings"
)

// Sparkline size for the edge-rules table (ADR-20261020105001).
const (
	SparklineWidth  = 84
	SparklineHeight = 20
)

// SparklinePoints renders values as an SVG polyline "points" attribute in a
// SparklineWidth×SparklineHeight box, scaled to the largest value. An empty
// or all-zero series is a flat line along the bottom.
func SparklinePoints(values []int64) string {
	if len(values) == 0 {
		return "0," + strconv.Itoa(SparklineHeight-1) + " " + strconv.Itoa(SparklineWidth) + "," + strconv.Itoa(SparklineHeight-1)
	}
	var peak int64
	for _, v := range values {
		peak = max(peak, v)
	}
	step := float64(SparklineWidth) / float64(max(len(values)-1, 1))
	usable := float64(SparklineHeight - 2)
	var b strings.Builder
	for i, v := range values {
		y := float64(SparklineHeight - 1)
		if peak > 0 {
			y -= usable * float64(v) / float64(peak)
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(strconv.FormatFloat(float64(i)*step, 'f', 1, 64))
		b.WriteByte(',')
		b.WriteString(strconv.FormatFloat(y, 'f', 1, 64))
	}
	return b.String()
}
